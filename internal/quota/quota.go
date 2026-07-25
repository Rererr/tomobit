// Package quota reads each provider's own usage endpoint with the user's own
// OAuth token (ADR-0044 Decision 1). The vendor counts the utilization —
// including consumption tomobit never saw — so a Snapshot is an observation,
// not an estimate. Nothing here invents a value: a fetch that cannot observe
// returns an error with its reason, and the caller shows 不明 (ADR-0044
// Decision 5). Both seams (HTTP, credentials) are injectable so every test
// runs against fakes — no test touches real credentials or the network.
//
// The endpoints are unofficial but live-verified: on 2026-07-24, with the
// owner's explicit approval, both returned HTTP 200 and the fixtures here pin
// the measured schema (structure only — no real values; ADR-0044 Context).
// Unofficial means unwarranted, so the parsers still treat a schema mismatch
// as an error worth reading, never as "0%".
package quota

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Doer is the injectable HTTP seam; *http.Client satisfies it.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// TokenSource yields the bearer token for one provider. Implementations read
// the credentials file at call time, never caching: a token the provider CLI
// refreshed mid-session must win over anything this process saw earlier.
type TokenSource interface {
	Token() (string, error)
}

// Window is one vendor-measured rate-limit window.
type Window struct {
	// Label names the window by its span in one vocabulary across vendors
	// ("5h", "7d") — codex derives it from limit_window_seconds
	// (codexWindowLabel), claude restates its spelled-out keys into the same
	// form (spanLabelFromKey). ADR-0044 改訂 2026-07-25: the earlier rule kept
	// each vendor's own spelling to avoid "renaming a key we have not seen
	// live", but that showed one screen "five_hour" next to "7d" for the same
	// idea. The restatement is faithful to a span we actually read, and
	// anything unreadable still comes back as the vendor's own word — the
	// guess the old rule guarded against is still not made.
	Label string
	// UsedPercent is the vendor-reported utilization, assumed 0–100. The live
	// verification (ADR-0044 Consequences) pins the unit.
	UsedPercent float64
	// ResetsAt is when the window refills. Zero means the vendor didn't say.
	ResetsAt time.Time
}

// Snapshot is one provider's vendor-measured utilization at ObservedAt.
// There is no "unknown" Snapshot — a fetch that cannot observe returns an
// error instead, so a Snapshot in hand is always a real observation.
type Snapshot struct {
	Provider   string
	Windows    []Window
	ObservedAt time.Time
}

// Fetcher observes one provider's quota.
type Fetcher interface {
	Provider() string
	Fetch(ctx context.Context) (Snapshot, error)
}

// fetchTimeout caps the default clients built by the New*Fetcher
// constructors: a status view must degrade to 不明, not hang, when the
// unofficial endpoint stops answering.
const fetchTimeout = 10 * time.Second

// maxBodyBytes bounds how much of a response is read — a usage payload is a
// few hundred bytes; anything near this limit is not the endpoint we think.
const maxBodyBytes = 1 << 20

// getJSON is the one request shape both vendors share: an authenticated GET
// expected to return a JSON body. The error text never includes the token.
func getJSON(ctx context.Context, doer Doer, url, bearer string, extra http.Header) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/json")
	for k, vs := range extra {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("GET %s: read body: %w", url, err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// The fix is the user's, not tomobit's: tomobit reads credentials but
		// never refreshes them (ADR-0044 Decision 1), so point at the CLI.
		return nil, fmt.Errorf("GET %s: HTTP %d: token rejected — run the provider CLI once to re-login or refresh", url, resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		// Measured 2026-07-24: a token from a DIFFERENT profile also comes
		// back as 429 rate_limit_error, not 401, so this message must not
		// assert which of the two happened — claiming "rate-limited" alone
		// would be the quiet lie ADR-0044 Decision 5 forbids.
		return nil, fmt.Errorf("GET %s: HTTP 429: rate-limited — or the token belongs to a different profile (a mixed-up profile surfaces as 429, not 401): %s", url, bodySnippet(body))
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GET %s: HTTP %d: %s", url, resp.StatusCode, bodySnippet(body))
	}
	return body, nil
}

// bodySnippet keeps enough of an error body to diagnose without flooding the
// status line. Runes, not bytes, so a Japanese error message is not cut
// mid-character.
func bodySnippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "(empty body)"
	}
	r := []rune(s)
	if len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return s
}

func nowOf(now func() time.Time) time.Time {
	if now == nil {
		return time.Now()
	}
	return now()
}
