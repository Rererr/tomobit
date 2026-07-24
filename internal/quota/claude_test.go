package quota

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// claudeUsageFixture is built from the structure the CodexBar implementation
// survey reported (ADR-0044 Context) — hand-written, no real data.
const claudeUsageFixture = `{
	"five_hour":      {"utilization": 34.5, "resets_at": "2026-07-24T12:00:00Z"},
	"seven_day":      {"utilization": 61.0, "resets_at": "2026-07-28T00:00:00Z"},
	"seven_day_opus": {"utilization": 12.0, "resets_at": "2026-07-28T00:00:00Z"},
	"extra_usage":    {"utilization": 0.0},
	"account":        {"email_hash": "irrelevant"}
}`

func TestClaudeFetchSendsBearerAndOAuthBetaToTheVendorEndpointOnly(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(200, claudeUsageFixture)}
	f := &ClaudeFetcher{Tokens: staticToken("tok-123"), HTTP: doer}

	if _, err := f.Fetch(context.Background()); err != nil {
		t.Fatal(err)
	}
	req := doer.lastReq
	if req.Method != "GET" || req.URL.String() != "https://api.anthropic.com/api/oauth/usage" {
		t.Errorf("must GET the vendor's own usage endpoint, got %s %s", req.Method, req.URL)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok-123" {
		t.Errorf("the user's own token must ride as the bearer, got %q", got)
	}
	if got := req.Header.Get("anthropic-beta"); got != "oauth-2025-04-20" {
		t.Errorf("the endpoint's beta gate must be sent, got %q", got)
	}
}

func TestClaudeFetchParsesEveryUtilizationWindowAndSkipsNonWindowKeys(t *testing.T) {
	f := &ClaudeFetcher{
		Tokens: staticToken("tok"),
		HTTP:   &fakeDoer{resp: jsonResponse(200, claudeUsageFixture)},
		Now:    func() time.Time { return time.UnixMilli(1000) },
	}
	snap, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Provider != "claude-code" {
		t.Errorf("provider = %q", snap.Provider)
	}
	if snap.ObservedAt != time.UnixMilli(1000) {
		t.Errorf("ObservedAt must be the fetch moment, got %v", snap.ObservedAt)
	}
	var labels []string
	for _, w := range snap.Windows {
		labels = append(labels, w.Label)
	}
	// five_hour/seven_day lead, the rest lexical; "account" is not a window.
	want := []string{"five_hour", "seven_day", "extra_usage", "seven_day_opus"}
	if fmt.Sprint(labels) != fmt.Sprint(want) {
		t.Fatalf("windows = %v, want %v", labels, want)
	}
	if snap.Windows[0].UsedPercent != 34.5 {
		t.Errorf("five_hour utilization = %v", snap.Windows[0].UsedPercent)
	}
	if want := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC); !snap.Windows[0].ResetsAt.Equal(want) {
		t.Errorf("five_hour resets_at = %v, want %v", snap.Windows[0].ResetsAt, want)
	}
	if !snap.Windows[2].ResetsAt.IsZero() {
		t.Errorf("a window without resets_at must stay zero (the vendor didn't say), got %v", snap.Windows[2].ResetsAt)
	}
}

func TestClaudeFetchWithoutAnyUtilizationWindowIsASchemaErrorNotAnEmptySnapshot(t *testing.T) {
	f := &ClaudeFetcher{Tokens: staticToken("tok"), HTTP: &fakeDoer{resp: jsonResponse(200, `{"account": {}}`)}}
	_, err := f.Fetch(context.Background())
	if err == nil {
		t.Fatal("an empty snapshot would read as 'no limits' — must error instead")
	}
	if !strings.Contains(err.Error(), "schema") {
		t.Errorf("the error must name the schema mismatch: %v", err)
	}
}

func TestClaudeFetchRejectedTokenNamesTheUsersFixWithoutEchoingTheToken(t *testing.T) {
	f := &ClaudeFetcher{Tokens: staticToken("SECRET-TOKEN"), HTTP: &fakeDoer{resp: jsonResponse(401, `{"error":"unauthorized"}`)}}
	_, err := f.Fetch(context.Background())
	if err == nil {
		t.Fatal("401 must be an error")
	}
	if !strings.Contains(err.Error(), "token rejected") {
		t.Errorf("must say the token was rejected and point at the CLI: %v", err)
	}
	if strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Fatalf("the token must never appear in an error: %v", err)
	}
}

func TestClaudeFetchNon200IsAnErrorCarryingTheStatus(t *testing.T) {
	f := &ClaudeFetcher{Tokens: staticToken("tok"), HTTP: &fakeDoer{resp: jsonResponse(503, "overloaded")}}
	_, err := f.Fetch(context.Background())
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("a failed fetch must carry its reason, got %v", err)
	}
}

func TestClaudeFetchMakesNoRequestWhenTheTokenIsUnavailable(t *testing.T) {
	f := &ClaudeFetcher{
		Tokens: tokenFunc(func() (string, error) { return "", errors.New("not logged in") }),
		HTTP:   neverDoer{t},
	}
	if _, err := f.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("the credential failure must surface as-is, got %v", err)
	}
}

func TestClaudeFileTokenReadsTheCliMaintainedAccessToken(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".credentials.json")
	if err := os.WriteFile(p, []byte(`{"claudeAiOauth":{"accessToken":"tok-abc","expiresAt":0}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := ClaudeFileToken{Path: p}.Token()
	if err != nil {
		t.Fatal(err)
	}
	if tok != "tok-abc" {
		t.Errorf("token = %q", tok)
	}
}

func TestClaudeFileTokenExpiredTellsTheUserToRunClaudeInsteadOfRefreshing(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".credentials.json")
	if err := os.WriteFile(p, []byte(`{"claudeAiOauth":{"accessToken":"tok","expiresAt":1000}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ClaudeFileToken{Path: p, Now: func() time.Time { return time.UnixMilli(2000) }}.Token()
	if err == nil {
		t.Fatal("an expired token must not be sent")
	}
	if !strings.Contains(err.Error(), "run `claude`") {
		t.Errorf("tomobit never refreshes credentials — the fix is the user's: %v", err)
	}
}

func TestClaudeFileTokenMissingFileIsAnErrorNotAnEmptyBearer(t *testing.T) {
	_, err := ClaudeFileToken{Path: filepath.Join(t.TempDir(), "absent.json")}.Token()
	if err == nil {
		t.Fatal("a missing credentials file must error")
	}
}

func TestClaudeFileTokenLoggedOutFileIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".credentials.json")
	if err := os.WriteFile(p, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ClaudeFileToken{Path: p}.Token()
	if err == nil || !strings.Contains(err.Error(), "accessToken") {
		t.Fatalf("a file without a token must say what is missing, got %v", err)
	}
}

func TestClaudeCredentialsPathIsTheClaudeHomeFile(t *testing.T) {
	p, err := ClaudeCredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p, filepath.Join(".claude", ".credentials.json")) {
		t.Errorf("path = %q", p)
	}
}
