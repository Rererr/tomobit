package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	codexProvider = "codex"
	codexUsageURL = "https://chatgpt.com/backend-api/wham/usage"
)

// CodexAuthPath prefers $CODEX_HOME/auth.json (the CLI's own override), else
// ~/.codex/auth.json. The file is the codex CLI's; tomobit only ever reads it
// (ADR-0044 Decision 1).
func CodexAuthPath() (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "auth.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("codex auth: resolve home: %w", err)
	}
	return filepath.Join(home, ".codex", "auth.json"), nil
}

// CodexFileToken reads the access token the codex CLI keeps on disk.
// Read-only, same line as ClaudeFileToken: tomobit never refreshes.
type CodexFileToken struct {
	Path string
}

// codexAuth is the auth.json shape (live-verified 2026-07-24: tokens with
// access_token under auth_mode "chatgpt", last_refresh in RFC3339).
// last_refresh is decoded to document the schema but never gates the request:
// measured 2026-07-24, a logged-in user's auth.json carried a last_refresh 9
// days old because the codex CLI refreshes the access token in memory without
// rewriting the file. The endpoint — not this reading of a disk timestamp — is
// the authority on whether the token still works (a stale-looking last_refresh
// with a valid token is exactly the false negative a pre-network cutoff caused).
type codexAuth struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
	} `json:"tokens"`
	LastRefresh string `json:"last_refresh"`
}

func (s CodexFileToken) Token() (string, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return "", fmt.Errorf("codex auth: %w", err)
	}
	var a codexAuth
	if err := json.Unmarshal(data, &a); err != nil {
		return "", fmt.Errorf("codex auth %s: %w", s.Path, err)
	}
	if a.Tokens.AccessToken == "" {
		return "", fmt.Errorf("codex auth %s: no tokens.access_token (not logged in, or the schema drifted)", s.Path)
	}
	// No staleness cutoff: a present token is handed to the request, and a
	// genuinely expired one comes back as the endpoint's 401 ("run the provider
	// CLI once to re-login", getJSON) — the honest reason, not a guess from the
	// file's age.
	return a.Tokens.AccessToken, nil
}

// CodexFetcher observes codex's rate-limit windows via the vendor's own
// usage endpoint (ADR-0044 route A).
type CodexFetcher struct {
	Tokens TokenSource
	HTTP   Doer
	Now    func() time.Time
	// CredentialOrigin names the auth file Tokens reads, for the same
	// honesty as ClaudeFetcher's: a fetch error must say whose credentials
	// failed, especially with $CODEX_HOME able to point elsewhere.
	CredentialOrigin string
}

// NewCodexFetcher wires the default seams: the CLI's own auth file and an
// HTTP client that times out instead of hanging a status view.
func NewCodexFetcher() (*CodexFetcher, error) {
	p, err := CodexAuthPath()
	if err != nil {
		return nil, err
	}
	return &CodexFetcher{
		Tokens:           CodexFileToken{Path: p},
		HTTP:             &http.Client{Timeout: fetchTimeout},
		CredentialOrigin: p,
	}, nil
}

func (f *CodexFetcher) Provider() string { return codexProvider }

func (f *CodexFetcher) Fetch(ctx context.Context) (Snapshot, error) {
	tok, err := f.Tokens.Token()
	if err != nil {
		return Snapshot{}, f.fetchError(err)
	}
	body, err := getJSON(ctx, f.HTTP, codexUsageURL, tok, nil)
	if err != nil {
		return Snapshot{}, f.fetchError(err)
	}
	now := nowOf(f.Now)
	windows, err := parseCodexUsage(body, now)
	if err != nil {
		return Snapshot{}, f.fetchError(err)
	}
	return Snapshot{Provider: codexProvider, Windows: windows, ObservedAt: now}, nil
}

func (f *CodexFetcher) fetchError(err error) error {
	if f.CredentialOrigin != "" {
		return fmt.Errorf("%s quota (credentials: %s): %w", codexProvider, f.CredentialOrigin, err)
	}
	return fmt.Errorf("%s quota: %w", codexProvider, err)
}

// codexWindow is one rate-limit window as measured 2026-07-24 (HTTP 200):
// used_percent 0–100, spans in seconds, reset_at in unix epoch seconds —
// unlike Claude's RFC3339 string. parseCodexUsage normalizes both dialects
// into Window.ResetsAt so no caller ever tells the vendors' clocks apart.
type codexWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

// parseCodexUsage reads the primary (5h) and secondary (weekly) windows from
// rate_limit; either may be null. The same response carries user_id /
// account_id / email / plan_type — deliberately not decoded: email is
// personal data with no seat in a Snapshot, and a field never decoded cannot
// leak into an error or a view. additional_rate_limits (per-model windows)
// and credits are likewise left undecoded until a view needs them. Zero
// readable windows is a schema mismatch worth a loud error, because silently
// showing nothing would read as "no limits".
func parseCodexUsage(body []byte, now time.Time) ([]Window, error) {
	var raw struct {
		RateLimit struct {
			Primary   *codexWindow `json:"primary_window"`
			Secondary *codexWindow `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse usage response: %w", err)
	}
	var windows []Window
	appendWindow := func(w *codexWindow, fallbackLabel string) {
		if w == nil {
			return
		}
		var resetsAt time.Time
		switch {
		case w.ResetAt > 0:
			resetsAt = time.Unix(w.ResetAt, 0)
		case w.ResetAfterSeconds > 0:
			resetsAt = now.Add(time.Duration(w.ResetAfterSeconds) * time.Second)
		}
		windows = append(windows, Window{
			Label:       codexWindowLabel(w.LimitWindowSeconds, fallbackLabel),
			UsedPercent: w.UsedPercent,
			ResetsAt:    resetsAt,
		})
	}
	appendWindow(raw.RateLimit.Primary, "primary")
	appendWindow(raw.RateLimit.Secondary, "secondary")
	if len(windows) == 0 {
		return nil, errors.New("no rate_limit windows in the response — the schema drifted from the one measured 2026-07-24; re-pin it against the live endpoint (ADR-0044 Consequences)")
	}
	return windows, nil
}

// codexWindowLabel names a window by its span ("5h", "7d") because the vendor
// keys carry no meaning to a human (primary/secondary). Falls back to the key
// when the span is absent, so a schema drift degrades to a vaguer label, not
// a wrong one.
func codexWindowLabel(seconds int64, fallback string) string {
	const (
		secondsPerHour = 60 * 60
		secondsPerDay  = 24 * secondsPerHour
	)
	switch {
	case seconds <= 0:
		return fallback
	case seconds%secondsPerDay == 0:
		return fmt.Sprintf("%dd", seconds/secondsPerDay)
	case seconds%secondsPerHour == 0:
		return fmt.Sprintf("%dh", seconds/secondsPerHour)
	case seconds%60 == 0:
		return fmt.Sprintf("%dm", seconds/60)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}
