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
	// codexAuthStaleAfter mirrors the CLI's own refresh horizon: a token whose
	// last_refresh is older than this has long expired, so the request is
	// doomed — fail before the network, with the user's fix in the message.
	codexAuthStaleAfter = 8 * 24 * time.Hour
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
	Now  func() time.Time // nil = time.Now; injected so staleness is testable
}

// codexAuth is the CodexBar-derived shape of auth.json. Unverified against a
// live file in this repo (ADR-0044 Context).
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
	if a.LastRefresh != "" {
		// An unparseable last_refresh falls through to the request: the
		// endpoint, not this guess at the file schema, is the authority on
		// whether the token still works.
		if t, err := time.Parse(time.RFC3339, a.LastRefresh); err == nil {
			if nowOf(s.Now).Sub(t) > codexAuthStaleAfter {
				return "", fmt.Errorf("codex token is stale (last_refresh %s) — run `codex` once to refresh it", a.LastRefresh)
			}
		}
	}
	return a.Tokens.AccessToken, nil
}

// CodexFetcher observes codex's rate-limit windows via the vendor's own
// usage endpoint (ADR-0044 route A).
type CodexFetcher struct {
	Tokens TokenSource
	HTTP   Doer
	Now    func() time.Time
}

// NewCodexFetcher wires the default seams, mirroring NewClaudeFetcher.
func NewCodexFetcher() (*CodexFetcher, error) {
	p, err := CodexAuthPath()
	if err != nil {
		return nil, err
	}
	return &CodexFetcher{
		Tokens: CodexFileToken{Path: p},
		HTTP:   &http.Client{Timeout: fetchTimeout},
	}, nil
}

func (f *CodexFetcher) Provider() string { return codexProvider }

func (f *CodexFetcher) Fetch(ctx context.Context) (Snapshot, error) {
	tok, err := f.Tokens.Token()
	if err != nil {
		return Snapshot{}, fmt.Errorf("%s quota: %w", codexProvider, err)
	}
	body, err := getJSON(ctx, f.HTTP, codexUsageURL, tok, nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%s quota: %w", codexProvider, err)
	}
	now := nowOf(f.Now)
	windows, err := parseCodexUsage(body, now)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%s quota: %w", codexProvider, err)
	}
	return Snapshot{Provider: codexProvider, Windows: windows, ObservedAt: now}, nil
}

// codexRateWindow is the CodexBar-derived shape of one window: the same
// triple the codex CLI streams internally (used_percent / window_minutes /
// resets_in_seconds).
type codexRateWindow struct {
	UsedPercent     float64 `json:"used_percent"`
	WindowMinutes   int64   `json:"window_minutes"`
	ResetsInSeconds int64   `json:"resets_in_seconds"`
}

// parseCodexUsage reads the primary (5h) and secondary (weekly) windows.
// Absent both is a schema mismatch worth a loud error, for the same reason as
// parseClaudeUsage: an empty snapshot would read as "no limits".
func parseCodexUsage(body []byte, now time.Time) ([]Window, error) {
	var raw struct {
		RateLimits struct {
			Primary   *codexRateWindow `json:"primary"`
			Secondary *codexRateWindow `json:"secondary"`
		} `json:"rate_limits"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse usage response: %w", err)
	}
	var windows []Window
	appendWindow := func(w *codexRateWindow, fallbackLabel string) {
		if w == nil {
			return
		}
		var resetsAt time.Time
		if w.ResetsInSeconds > 0 {
			resetsAt = now.Add(time.Duration(w.ResetsInSeconds) * time.Second)
		}
		windows = append(windows, Window{
			Label:       codexWindowLabel(w.WindowMinutes, fallbackLabel),
			UsedPercent: w.UsedPercent,
			ResetsAt:    resetsAt,
		})
	}
	appendWindow(raw.RateLimits.Primary, "primary")
	appendWindow(raw.RateLimits.Secondary, "secondary")
	if len(windows) == 0 {
		return nil, errors.New("no rate_limits windows in the response — the live schema differs from the CodexBar-derived expectation; pin it against the real endpoint (ADR-0044 Consequences)")
	}
	return windows, nil
}

// codexWindowLabel names a window by its span ("5h", "7d") because the vendor
// keys carry no meaning to a human (primary/secondary). Falls back to the key
// when the span is absent, so a schema drift degrades to a vaguer label, not
// a wrong one.
func codexWindowLabel(minutes int64, fallback string) string {
	const minutesPerDay = 24 * 60
	switch {
	case minutes <= 0:
		return fallback
	case minutes%minutesPerDay == 0:
		return fmt.Sprintf("%dd", minutes/minutesPerDay)
	case minutes%60 == 0:
		return fmt.Sprintf("%dh", minutes/60)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}
