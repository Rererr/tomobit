package quota

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// codexUsageFixture is built from the structure the CodexBar implementation
// survey reported (ADR-0044 Context) — hand-written, no real data.
const codexUsageFixture = `{
	"rate_limits": {
		"primary":   {"used_percent": 42.0, "window_minutes": 300,   "resets_in_seconds": 3600},
		"secondary": {"used_percent": 80.5, "window_minutes": 10080, "resets_in_seconds": 86400}
	}
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

func TestCodexFetchNamesWindowsByTheirSpanAndDerivesResetTimes(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	f := &CodexFetcher{
		Tokens: staticToken("tok"),
		HTTP:   &fakeDoer{resp: jsonResponse(200, codexUsageFixture)},
		Now:    func() time.Time { return now },
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
	if w := snap.Windows[0]; w.Label != "5h" || w.UsedPercent != 42.0 || !w.ResetsAt.Equal(now.Add(time.Hour)) {
		t.Errorf("primary window = %+v", w)
	}
	if w := snap.Windows[1]; w.Label != "7d" || w.UsedPercent != 80.5 || !w.ResetsAt.Equal(now.Add(24*time.Hour)) {
		t.Errorf("secondary window = %+v", w)
	}
}

func TestCodexFetchWithoutRateLimitsIsASchemaErrorNotAnEmptySnapshot(t *testing.T) {
	f := &CodexFetcher{Tokens: staticToken("tok"), HTTP: &fakeDoer{resp: jsonResponse(200, `{"plan":"plus"}`)}}
	_, err := f.Fetch(context.Background())
	if err == nil {
		t.Fatal("an empty snapshot would read as 'no limits' — must error instead")
	}
	if !strings.Contains(err.Error(), "schema") {
		t.Errorf("the error must name the schema mismatch: %v", err)
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
		minutes int64
		want    string
	}{
		{300, "5h"},
		{10080, "7d"},
		{90, "90m"},
		{0, "primary"},
	}
	for _, c := range cases {
		if got := codexWindowLabel(c.minutes, "primary"); got != c.want {
			t.Errorf("codexWindowLabel(%d) = %q, want %q", c.minutes, got, c.want)
		}
	}
}
