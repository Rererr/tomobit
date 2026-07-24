package quota

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// codexUsageFixture mirrors the schema measured live on 2026-07-24 (HTTP
// 200) — structure from the real response, every value a hand-written dummy:
// rate_limit (singular) holds nullable primary/secondary windows with
// used_percent 0–100, spans in seconds, and reset_at in unix epoch seconds;
// the response also carries user_id / account_id / email / plan_type and
// additional_rate_limits / credits, none of which may enter a Snapshot.
const codexUsageFixture = `{
	"user_id": "user-dummy",
	"account_id": "account-dummy",
	"email": "dummy-owner@example.com",
	"plan_type": "plus",
	"rate_limit": {
		"allowed": true,
		"limit_reached": false,
		"primary_window":   {"used_percent": 42.0, "limit_window_seconds": 18000,  "reset_after_seconds": 3600,  "reset_at": 1786000000},
		"secondary_window": {"used_percent": 80.5, "limit_window_seconds": 604800, "reset_after_seconds": 86400, "reset_at": 1786500000}
	},
	"additional_rate_limits": [
		{"limit_name": "gpt-5", "metered_feature": "codex", "rate_limit": {"allowed": true, "limit_reached": false, "primary_window": null, "secondary_window": null}}
	],
	"credits": {"has_credits": false, "unlimited": false, "overage_limit_reached": false, "balance": "0", "approx_local_messages": [0, 0], "approx_cloud_messages": [0, 0]},
	"rate_limit_reset_credits": {"available_count": 0, "applicable_available_count": 0}
}`

func TestCodexFetchSendsBearerToTheVendorEndpointOnly(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(200, codexUsageFixture)}
	f := &CodexFetcher{Tokens: staticToken("tok-xyz"), HTTP: doer}

	if _, err := f.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	req := doer.lastReq
	if req.Method != "GET" || req.URL.String() != "https://chatgpt.com/backend-api/wham/usage" {
		t.Errorf("must GET the vendor's own usage endpoint, got %s %s", req.Method, req.URL)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok-xyz" {
		t.Errorf("the user's own token must ride as the bearer, got %q", got)
	}
}

func TestCodexFetchNormalizesEpochResetAtAndNamesWindowsByTheirSpan(t *testing.T) {
	f := &CodexFetcher{
		Tokens: staticToken("tok"),
		HTTP:   &fakeDoer{resp: jsonResponse(200, codexUsageFixture)},
		Now:    func() time.Time { return time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC) },
	}
	snap, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Provider != "codex" {
		t.Errorf("provider = %q", snap.Provider)
	}
	if len(snap.Windows) != 2 {
		t.Fatalf("windows = %+v", snap.Windows)
	}
	// reset_at is epoch seconds (measured) — Claude speaks RFC3339; both
	// must land in the same Window.ResetsAt clock.
	if w := snap.Windows[0]; w.Label != "5h" || w.UsedPercent != 42.0 || !w.ResetsAt.Equal(time.Unix(1786000000, 0)) {
		t.Errorf("primary window = %+v", w)
	}
	if w := snap.Windows[1]; w.Label != "7d" || w.UsedPercent != 80.5 || !w.ResetsAt.Equal(time.Unix(1786500000, 0)) {
		t.Errorf("secondary window = %+v", w)
	}
}

func TestCodexFetchNeverCarriesEmailOrAccountIdentityIntoTheSnapshot(t *testing.T) {
	f := &CodexFetcher{Tokens: staticToken("tok"), HTTP: &fakeDoer{resp: jsonResponse(200, codexUsageFixture)}}
	snap, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rendered := fmt.Sprintf("%+v", snap)
	for _, personal := range []string{"dummy-owner@example.com", "user-dummy", "account-dummy"} {
		if strings.Contains(rendered, personal) {
			t.Errorf("personal data %q has no seat in a Snapshot: %s", personal, rendered)
		}
	}
}

func TestCodexParseMissingResetAtFallsBackToNowPlusResetAfter(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	windows, err := parseCodexUsage([]byte(`{
		"rate_limit": {"primary_window": {"used_percent": 1.0, "limit_window_seconds": 18000, "reset_after_seconds": 3600, "reset_at": 0}}
	}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if !windows[0].ResetsAt.Equal(now.Add(time.Hour)) {
		t.Errorf("resets_at = %v, want now+1h", windows[0].ResetsAt)
	}
}

func TestCodexParseANullWindowIsSkippedNotZeroed(t *testing.T) {
	windows, err := parseCodexUsage([]byte(`{
		"rate_limit": {"primary_window": {"used_percent": 9.0, "limit_window_seconds": 18000, "reset_after_seconds": 0, "reset_at": 0}, "secondary_window": null}
	}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 1 || windows[0].Label != "5h" {
		t.Fatalf("a null window must vanish, not become a 0%% row: %+v", windows)
	}
}

func TestCodexFetchWithoutRateLimitWindowsIsASchemaErrorNotAnEmptySnapshot(t *testing.T) {
	f := &CodexFetcher{Tokens: staticToken("tok"), HTTP: &fakeDoer{resp: jsonResponse(200, `{"plan_type": "plus"}`)}}
	_, err := f.Fetch(context.Background())
	if err == nil {
		t.Fatal("an empty snapshot would read as 'no limits' — must error instead")
	}
	if !strings.Contains(err.Error(), "schema") {
		t.Errorf("the error must name the schema mismatch: %v", err)
	}
}

func TestCodexFetchErrorsNameTheCredentialOrigin(t *testing.T) {
	f := &CodexFetcher{
		Tokens:           staticToken("tok"),
		HTTP:             &fakeDoer{resp: jsonResponse(429, `{"detail": "rate limited"}`)},
		CredentialOrigin: "/custom/codex/auth.json",
	}
	_, err := f.Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "/custom/codex/auth.json") {
		t.Fatalf("the error must say which auth file was read, got %v", err)
	}
}

func TestCodexFileTokenStaleLastRefreshFailsBeforeAnyNetwork(t *testing.T) {
	p := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(p, []byte(`{"tokens":{"access_token":"tok"},"last_refresh":"2026-07-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC) // 23 days later, past the 8-day horizon
	_, err := CodexFileToken{Path: p, Now: func() time.Time { return now }}.Token()
	if err == nil {
		t.Fatal("a stale token is a doomed request — must fail before the network")
	}
	if !strings.Contains(err.Error(), "run `codex`") {
		t.Errorf("tomobit never refreshes credentials — the fix is the user's: %v", err)
	}
}

func TestCodexFileTokenFreshLastRefreshYieldsTheToken(t *testing.T) {
	p := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(p, []byte(`{"tokens":{"access_token":"tok-1"},"last_refresh":"2026-07-23T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	tok, err := CodexFileToken{Path: p, Now: func() time.Time { return now }}.Token()
	if err != nil {
		t.Fatal(err)
	}
	if tok != "tok-1" {
		t.Errorf("token = %q", tok)
	}
}

func TestCodexFileTokenUnparseableLastRefreshDefersToTheEndpoint(t *testing.T) {
	p := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(p, []byte(`{"tokens":{"access_token":"tok-2"},"last_refresh":"not-a-time"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := CodexFileToken{Path: p}.Token()
	if err != nil {
		t.Fatalf("an unreadable last_refresh must not block a present token: %v", err)
	}
	if tok != "tok-2" {
		t.Errorf("token = %q", tok)
	}
}

func TestCodexFileTokenLoggedOutFileIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(p, []byte(`{"tokens":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := CodexFileToken{Path: p}.Token()
	if err == nil || !strings.Contains(err.Error(), "access_token") {
		t.Fatalf("a file without a token must say what is missing, got %v", err)
	}
}

func TestCodexAuthPathPrefersCodexHomeOverTheDefault(t *testing.T) {
	t.Setenv("CODEX_HOME", "/custom/codex")
	p, err := CodexAuthPath()
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join("/custom/codex", "auth.json") {
		t.Errorf("path = %q", p)
	}

	t.Setenv("CODEX_HOME", "")
	p, err = CodexAuthPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p, filepath.Join(".codex", "auth.json")) {
		t.Errorf("default path = %q", p)
	}
}

func TestCodexWindowLabelFallsBackToTheKeyWhenTheSpanIsAbsent(t *testing.T) {
	cases := []struct {
		seconds int64
		want    string
	}{
		{18000, "5h"},
		{604800, "7d"},
		{5400, "90m"},
		{90, "90s"},
		{0, "primary"},
	}
	for _, c := range cases {
		if got := codexWindowLabel(c.seconds, "primary"); got != c.want {
			t.Errorf("codexWindowLabel(%d) = %q, want %q", c.seconds, got, c.want)
		}
	}
}
