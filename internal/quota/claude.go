package quota

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	// claudeOAuthBeta gates the unofficial usage endpoint (live-verified
	// 2026-07-24, HTTP 200). Unversioned guarantee: the day it vanishes,
	// Fetch errors and the view shows 不明 (ADR-0044 Decision 5) — no
	// fallback route replaces it.
	claudeOAuthBeta = "oauth-2025-04-20"
)

// ClaudeKeychainServiceName derives the macOS Keychain service holding one
// profile's credentials. Measured 2026-07-24: the default profile uses the
// bare name; a profile selected via CLAUDE_CONFIG_DIR appends the first 8 hex
// chars of sha256(configDir) — /Users/example/.claude-personal derived
// "Claude Code-credentials-5034c31c" and the real item matched. tomobit
// already holds configDir (config.json claude_config_dir, the value the
// claude-code adapter launches with), so the reader targets the profile that
// actually runs instead of silently grabbing the default one. That targeting
// matters more than it looks: a token from the WRONG profile does not fail as
// 401 — it came back HTTP 429 rate_limit_error (measured), indistinguishable
// from a real rate limit.
func ClaudeKeychainServiceName(configDir string) string {
	if configDir == "" {
		return "Claude Code-credentials"
	}
	sum := sha256.Sum256([]byte(configDir))
	return "Claude Code-credentials-" + hex.EncodeToString(sum[:])[:8]
}

// ClaudeCredentialsPath is ~/.claude/.credentials.json — the on-disk fallback
// for installs that keep a file. The dev Mac (measured 2026-07-24) has no
// such file: there the credentials live only in the Keychain, under
// ClaudeKeychainServiceName. tomobit only ever reads either place.
func ClaudeCredentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("claude credentials: resolve home: %w", err)
	}
	return filepath.Join(home, ".claude", ".credentials.json"), nil
}

// claudeCredentials is the credentials JSON shape, identical in the file and
// in the Keychain item's payload.
type claudeCredentials struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
		ExpiresAt   int64  `json:"expiresAt"` // unix ms; 0 = not stated
	} `json:"claudeAiOauth"`
}

// parseClaudeToken is the one reading of the credentials JSON, shared by the
// file and Keychain sources. origin names where the bytes came from — every
// error carries it, because "which profile's credentials did we read" is
// exactly the fact a 429 misdiagnosis needs (ADR-0044 Decision 5).
func parseClaudeToken(data []byte, origin string, now func() time.Time) (string, error) {
	var c claudeCredentials
	if err := json.Unmarshal(data, &c); err != nil {
		return "", fmt.Errorf("claude credentials (%s): %w", origin, err)
	}
	if c.ClaudeAiOauth.AccessToken == "" {
		return "", fmt.Errorf("claude credentials (%s): no claudeAiOauth.accessToken (not logged in, or the wrong profile)", origin)
	}
	if exp := c.ClaudeAiOauth.ExpiresAt; exp > 0 && !nowOf(now).Before(time.UnixMilli(exp)) {
		return "", fmt.Errorf("claude token (%s) expired at %s — run `claude` once to refresh it", origin, time.UnixMilli(exp).UTC().Format(time.RFC3339))
	}
	return c.ClaudeAiOauth.AccessToken, nil
}

// ClaudeFileToken reads the access token from the on-disk credentials file.
// Read-only: refreshing or rewriting credentials would make tomobit a
// credential manager, exactly the line ADR-0044 Decision 1 refuses to cross,
// so an expired token is returned as an error naming the user's fix.
type ClaudeFileToken struct {
	Path string
	Now  func() time.Time // nil = time.Now; injected so expiry is testable
}

func (s ClaudeFileToken) Token() (string, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return "", fmt.Errorf("claude credentials: %w", err)
	}
	return parseClaudeToken(data, s.Path, s.Now)
}

// ClaudeKeychainToken reads the same credentials JSON from a macOS Keychain
// item. Read is injected and stays a fake in every test: the real reader (a
// `security find-generic-password` shell-out) arrives with the wiring, so
// this package never touches a Keychain on its own.
type ClaudeKeychainToken struct {
	Service string // from ClaudeKeychainServiceName(configDir)
	Read    func(service string) ([]byte, error)
	Now     func() time.Time
}

func (s ClaudeKeychainToken) Token() (string, error) {
	if s.Read == nil {
		return "", fmt.Errorf("claude credentials (keychain %q): no keychain reader wired", s.Service)
	}
	data, err := s.Read(s.Service)
	if err != nil {
		return "", fmt.Errorf("claude credentials (keychain %q): %w", s.Service, err)
	}
	return parseClaudeToken(data, fmt.Sprintf("keychain %q", s.Service), s.Now)
}

// ClaudeFetcher observes claude-code's rate-limit windows via the vendor's
// own OAuth usage endpoint (ADR-0044 route A). No default constructor: the
// right credential source is platform- and profile-dependent (Keychain on
// macOS, file elsewhere) and the Keychain reader is deliberately unwired
// here, so construction belongs to the wiring that knows both.
type ClaudeFetcher struct {
	Tokens TokenSource
	HTTP   Doer
	Now    func() time.Time
	// CredentialOrigin names which profile's credentials Tokens reads
	// (keychain service or file path). It rides every fetch error because a
	// wrong-profile token surfaces as HTTP 429, not 401 (measured
	// 2026-07-24): without the origin, a mixed-up profile masquerades as a
	// rate limit and the 不明 explanation lies (ADR-0044 Decision 5).
	CredentialOrigin string
}

func (f *ClaudeFetcher) Provider() string { return claudeProvider }

func (f *ClaudeFetcher) Fetch(ctx context.Context) (Snapshot, error) {
	tok, err := f.Tokens.Token()
	if err != nil {
		return Snapshot{}, f.fetchError(err)
	}
	extra := http.Header{}
	extra.Set("anthropic-beta", claudeOAuthBeta)
	body, err := getJSON(ctx, f.HTTP, claudeUsageURL, tok, extra)
	if err != nil {
		return Snapshot{}, f.fetchError(err)
	}
	windows, err := parseClaudeUsage(body)
	if err != nil {
		return Snapshot{}, f.fetchError(err)
	}
	return Snapshot{Provider: claudeProvider, Windows: windows, ObservedAt: nowOf(f.Now)}, nil
}

func (f *ClaudeFetcher) fetchError(err error) error {
	if f.CredentialOrigin != "" {
		return fmt.Errorf("%s quota (credentials: %s): %w", claudeProvider, f.CredentialOrigin, err)
	}
	return fmt.Errorf("%s quota: %w", claudeProvider, err)
}

// claudeWindowOrder puts the two windows every plan shares first; anything
// else (model weeklies, when the vendor populates them) follows lexically.
var claudeWindowOrder = map[string]int{"five_hour": 0, "seven_day": 1}

// parseClaudeUsage reads every top-level object carrying a numeric
// utilization as one window, keyed by the vendor's own name. The live schema
// (measured 2026-07-24, HTTP 200): five_hour/seven_day always carry
// {utilization: 0–100, resets_at: RFC3339 with microseconds+offset}; the
// model/kind-specific keys (seven_day_opus, seven_day_sonnet, …) may be
// null; limits[] / spend / extra_usage / member_dashboard_available are not
// utilization windows. limits[] does carry scoped windows
// (kind/percent/severity/is_active) — deliberately not ingested until a view
// needs that granularity, so nothing here depends on its shape. A response
// with no window at all is a schema mismatch worth a loud error, because
// silently showing nothing would read as "no limits".
func parseClaudeUsage(body []byte) ([]Window, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse usage response: %w", err)
	}
	var windows []Window
	for key, val := range raw {
		var w struct {
			Utilization *float64 `json:"utilization"`
			ResetsAt    string   `json:"resets_at"`
		}
		if err := json.Unmarshal(val, &w); err != nil || w.Utilization == nil {
			continue
		}
		windows = append(windows, Window{
			Label:       key,
			UsedPercent: *w.Utilization,
			ResetsAt:    parseClaudeResetsAt(w.ResetsAt),
		})
	}
	if len(windows) == 0 {
		return nil, errors.New("no utilization windows in the response — the schema drifted from the one measured 2026-07-24; re-pin it against the live endpoint (ADR-0044 Consequences)")
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

// parseClaudeResetsAt reads the measured RFC3339 form (microseconds and a
// numeric offset, e.g. …T05:09:59.817927+00:00). Unreadable stays zero ("the
// vendor didn't say") rather than failing the fetch: discarding a utilization
// we did observe over a timestamp format would lose the more valuable half of
// the observation.
func parseClaudeResetsAt(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
