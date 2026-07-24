package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	claudeProvider = "claude-code"
	claudeUsageURL = "https://api.anthropic.com/api/oauth/usage"
	// claudeOAuthBeta gates the unofficial usage endpoint. Unversioned
	// guarantee: the day it vanishes, Fetch errors and the view shows 不明
	// (ADR-0044 Decision 5) — no fallback route replaces it.
	claudeOAuthBeta = "oauth-2025-04-20"
)

// ClaudeKeychainService is where macOS installs may keep the same credentials
// when the file below is absent. Recorded but deliberately never read:
// shelling out to `security` is a new surface, and the default install writes
// the file (ADR-0044 Decision 1). If a real machine ever lacks the file, the
// revisit starts from this name.
const ClaudeKeychainService = "Claude Code-credentials"

// ClaudeCredentialsPath is ~/.claude/.credentials.json — the file the Claude
// Code CLI itself maintains. tomobit only ever reads it (ADR-0044 Decision 1).
func ClaudeCredentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("claude credentials: resolve home: %w", err)
	}
	return filepath.Join(home, ".claude", ".credentials.json"), nil
}

// ClaudeFileToken reads the access token the Claude Code CLI keeps on disk.
// Read-only: refreshing or rewriting credentials would make tomobit a
// credential manager, exactly the line ADR-0044 Decision 1 refuses to cross,
// so an expired token is returned as an error naming the user's fix.
type ClaudeFileToken struct {
	Path string
	Now  func() time.Time // nil = time.Now; injected so expiry is testable
}

// claudeCredentials is the CodexBar-derived shape of the credentials file.
// Unverified against a live file in this repo (ADR-0044 Context): a mismatch
// surfaces as "no accessToken", not as a silent empty bearer.
type claudeCredentials struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
		ExpiresAt   int64  `json:"expiresAt"` // unix ms; 0 = not stated
	} `json:"claudeAiOauth"`
}

func (s ClaudeFileToken) Token() (string, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return "", fmt.Errorf("claude credentials: %w", err)
	}
	var c claudeCredentials
	if err := json.Unmarshal(data, &c); err != nil {
		return "", fmt.Errorf("claude credentials %s: %w", s.Path, err)
	}
	if c.ClaudeAiOauth.AccessToken == "" {
		return "", fmt.Errorf("claude credentials %s: no claudeAiOauth.accessToken (not logged in, or the schema drifted)", s.Path)
	}
	if exp := c.ClaudeAiOauth.ExpiresAt; exp > 0 && !nowOf(s.Now).Before(time.UnixMilli(exp)) {
		return "", fmt.Errorf("claude token expired at %s — run `claude` once to refresh it", time.UnixMilli(exp).UTC().Format(time.RFC3339))
	}
	return c.ClaudeAiOauth.AccessToken, nil
}

// ClaudeFetcher observes claude-code's rate-limit windows via the vendor's
// own OAuth usage endpoint (ADR-0044 route A).
type ClaudeFetcher struct {
	Tokens TokenSource
	HTTP   Doer
	Now    func() time.Time
}

// NewClaudeFetcher wires the default seams: the on-disk credentials file and
// an HTTP client that times out instead of hanging a status view.
func NewClaudeFetcher() (*ClaudeFetcher, error) {
	p, err := ClaudeCredentialsPath()
	if err != nil {
		return nil, err
	}
	return &ClaudeFetcher{
		Tokens: ClaudeFileToken{Path: p},
		HTTP:   &http.Client{Timeout: fetchTimeout},
	}, nil
}

func (f *ClaudeFetcher) Provider() string { return claudeProvider }

func (f *ClaudeFetcher) Fetch(ctx context.Context) (Snapshot, error) {
	tok, err := f.Tokens.Token()
	if err != nil {
		return Snapshot{}, fmt.Errorf("%s quota: %w", claudeProvider, err)
	}
	extra := http.Header{}
	extra.Set("anthropic-beta", claudeOAuthBeta)
	body, err := getJSON(ctx, f.HTTP, claudeUsageURL, tok, extra)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%s quota: %w", claudeProvider, err)
	}
	windows, err := parseClaudeUsage(body)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%s quota: %w", claudeProvider, err)
	}
	return Snapshot{Provider: claudeProvider, Windows: windows, ObservedAt: nowOf(f.Now)}, nil
}

// claudeWindowOrder puts the two windows every plan shares first; the
// model-specific weeklies and extra_usage follow lexically.
var claudeWindowOrder = map[string]int{"five_hour": 0, "seven_day": 1}

// parseClaudeUsage reads every top-level object carrying a utilization as one
// window ("five_hour", "seven_day", model weeklies, "extra_usage"), keyed by
// the vendor's own name. Keys without a utilization (account metadata) are
// not windows and not errors; a response with no window at all is a schema
// mismatch worth a loud error, because silently showing nothing would read as
// "no limits".
func parseClaudeUsage(body []byte) ([]Window, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse usage response: %w", err)
	}
	var windows []Window
	for key, val := range raw {
		var w struct {
			Utilization *float64        `json:"utilization"`
			ResetsAt    json.RawMessage `json:"resets_at"`
		}
		if err := json.Unmarshal(val, &w); err != nil || w.Utilization == nil {
			continue
		}
		windows = append(windows, Window{
			Label:       key,
			UsedPercent: *w.Utilization,
			ResetsAt:    parseResetsAt(w.ResetsAt),
		})
	}
	if len(windows) == 0 {
		return nil, errors.New("no utilization windows in the response — the live schema differs from the CodexBar-derived expectation; pin it against the real endpoint (ADR-0044 Consequences)")
	}
	sort.SliceStable(windows, func(i, j int) bool {
		oi, iKnown := claudeWindowOrder[windows[i].Label]
		oj, jKnown := claudeWindowOrder[windows[j].Label]
		if iKnown && jKnown {
			return oi < oj
		}
		if iKnown != jKnown {
			return iKnown
		}
		return windows[i].Label < windows[j].Label
	})
	return windows, nil
}

// parseResetsAt reads the refill time from whichever shape the vendor uses —
// RFC3339 string or epoch (seconds or ms). Unreadable stays zero ("the vendor
// didn't say") rather than failing the fetch: discarding a utilization we did
// observe over a timestamp format would violate least-loss honesty.
func parseResetsAt(raw json.RawMessage) time.Time {
	if len(raw) == 0 {
		return time.Time{}
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
		return time.Time{}
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil && n > 0 {
		if n > 1e12 { // epoch ms territory; epoch seconds stay < 1e12 until year 33658
			return time.UnixMilli(int64(n))
		}
		return time.Unix(int64(n), 0)
	}
	return time.Time{}
}
