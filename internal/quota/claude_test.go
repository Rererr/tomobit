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

// claudeUsageFixture mirrors the schema measured live on 2026-07-24 (HTTP
// 200) — structure from the real response, every value a hand-written dummy:
// utilization is 0–100, resets_at is RFC3339 with microseconds and a numeric
// offset, the model/kind keys can be null, and limits/spend/extra_usage/
// member_dashboard_available are not utilization windows.
const claudeUsageFixture = `{
	"five_hour": {"utilization": 34.5, "resets_at": "2026-07-24T12:00:00.123456+00:00", "limit_dollars": null, "used_dollars": null, "remaining_dollars": null},
	"seven_day": {"utilization": 61.0, "resets_at": "2026-07-28T00:00:00+00:00", "limit_dollars": null, "used_dollars": null, "remaining_dollars": null},
	"seven_day_opus": null,
	"seven_day_sonnet": null,
	"seven_day_oauth_apps": null,
	"limits": [
		{"kind": "session", "group": "default", "percent": 35, "severity": "none", "resets_at": "2026-07-24T12:00:00+00:00", "scope": null, "is_active": false},
		{"kind": "weekly_scoped", "group": "default", "percent": 3, "severity": "none", "resets_at": "2026-07-28T00:00:00+00:00", "scope": {"model": {"id": null, "display_name": "Opus"}, "surface": null}, "is_active": false}
	],
	"extra_usage": {"is_enabled": false, "monthly_limit": null, "used_credits": null, "utilization": null},
	"spend": {"used": {"amount_minor": 0, "currency": "USD", "exponent": 2}, "limit": null, "percent": 0},
	"member_dashboard_available": true
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

func TestClaudeFetchReadsTheMeasuredSchemaNullKeysAndNonWindowsSkipped(t *testing.T) {
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
	// Null model keys and limits/spend/extra_usage(null utilization) are not
	// windows; only the two populated ones remain. Labels come back in the
	// span vocabulary codex already speaks (ADR-0044 改訂 2026-07-25).
	want := []string{"5h", "7d"}
	if fmt.Sprint(labels) != fmt.Sprint(want) {
		t.Fatalf("windows = %v, want %v", labels, want)
	}
	if snap.Windows[0].UsedPercent != 34.5 {
		t.Errorf("utilization is a 0-100 percent taken as-is, got %v", snap.Windows[0].UsedPercent)
	}
	// The measured resets_at form: microseconds plus a numeric offset.
	wantReset := time.Date(2026, 7, 24, 12, 0, 0, 123456000, time.UTC)
	if !snap.Windows[0].ResetsAt.Equal(wantReset) {
		t.Errorf("5h resets_at = %v, want %v", snap.Windows[0].ResetsAt, wantReset)
	}
}

func TestClaudeParsePopulatedModelWeekliesJoinAfterTheSharedWindows(t *testing.T) {
	windows, err := parseClaudeUsage([]byte(`{
		"seven_day_opus": {"utilization": 12.0, "resets_at": "2026-07-28T00:00:00+00:00"},
		"seven_day":      {"utilization": 61.0, "resets_at": "2026-07-28T00:00:00+00:00"},
		"five_hour":      {"utilization": 34.5, "resets_at": "2026-07-24T12:00:00+00:00"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, w := range windows {
		labels = append(labels, w.Label)
	}
	// モデル別の週次は span + 修飾子に畳まれ、共有の2枠の後ろに続く。
	want := []string{"5h", "7d", "7d opus"}
	if fmt.Sprint(labels) != fmt.Sprint(want) {
		t.Fatalf("windows = %v, want %v", labels, want)
	}
}

// ADR-0044 改訂 2026-07-25: 画面に "five_hour" と "7d" が並ぶのをやめる。
// 読めた分だけを言い換え、読めないものはベンダーの語のまま返す — 旧規則が
// 恐れた「見たことのないキーの改名」は依然として行わない。
func TestSpanLabelFromKeyRestatesOnlyWhatItCanRead(t *testing.T) {
	cases := map[string]string{
		"five_hour":       "5h",
		"seven_day":       "7d",
		"seven_days":      "7d",
		"seven_day_opus":  "7d opus",
		"one_minute":      "1m",
		"twelve_month":    "12mo",
		"thirty_day":      "thirty_day",      // 数詞の表に無い = 触らない
		"weekly":          "weekly",          // 区切りが無い = 触らない
		"seven_fortnight": "seven_fortnight", // 単位の表に無い = 触らない
		"":                "",
	}
	for in, want := range cases {
		if got := spanLabelFromKey(in); got != want {
			t.Errorf("spanLabelFromKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClaudeParseWindowWithoutResetsAtStaysZeroNotInvented(t *testing.T) {
	windows, err := parseClaudeUsage([]byte(`{"five_hour": {"utilization": 5.0}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !windows[0].ResetsAt.IsZero() {
		t.Errorf("a missing resets_at means 'the vendor didn't say', got %v", windows[0].ResetsAt)
	}
}

func TestClaudeFetchWithoutAnyUtilizationWindowIsASchemaErrorNotAnEmptySnapshot(t *testing.T) {
	f := &ClaudeFetcher{Tokens: staticToken("tok"), HTTP: &fakeDoer{resp: jsonResponse(200, `{"member_dashboard_available": true}`)}}
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

func TestClaudeFetch429NamesTheCredentialOriginAndBothPossibleCauses(t *testing.T) {
	// Measured 2026-07-24: a token from a different profile surfaces as HTTP
	// 429 rate_limit_error, not 401 — so the error must carry which
	// credentials were read and must not claim "rate-limited" as the sole
	// cause.
	f := &ClaudeFetcher{
		Tokens:           staticToken("tok"),
		HTTP:             &fakeDoer{resp: jsonResponse(429, `{"type":"error","error":{"type":"rate_limit_error"}}`)},
		CredentialOrigin: `keychain "Claude Code-credentials-5034c31c"`,
	}
	_, err := f.Fetch(context.Background())
	if err == nil {
		t.Fatal("429 must be an error")
	}
	if !strings.Contains(err.Error(), `keychain "Claude Code-credentials-5034c31c"`) {
		t.Errorf("the error must say which profile's credentials were read: %v", err)
	}
	if !strings.Contains(err.Error(), "different profile") {
		t.Errorf("the error must admit the wrong-profile cause, not just 'rate-limited': %v", err)
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

func TestClaudeKeychainServiceNameDefaultProfileIsTheBareName(t *testing.T) {
	if got := ClaudeKeychainServiceName(""); got != "Claude Code-credentials" {
		t.Errorf("service = %q", got)
	}
}

func TestClaudeKeychainServiceNameDerivesTheMeasuredSuffixFromConfigDir(t *testing.T) {
	// Pinned against the real machine (2026-07-24): the derived name matched
	// the existing Keychain item for this profile.
	if got := ClaudeKeychainServiceName("/Users/example/.claude-personal"); got != "Claude Code-credentials-5034c31c" {
		t.Errorf("service = %q, want Claude Code-credentials-5034c31c", got)
	}
}

func TestClaudeKeychainTokenReadsViaTheInjectedReaderOnly(t *testing.T) {
	var askedService string
	src := ClaudeKeychainToken{
		Service: "Claude Code-credentials-5034c31c",
		Read: func(service string) ([]byte, error) {
			askedService = service
			return []byte(`{"claudeAiOauth":{"accessToken":"tok-kc","expiresAt":0}}`), nil
		},
	}
	tok, err := src.Token()
	if err != nil {
		t.Fatal(err)
	}
	if tok != "tok-kc" {
		t.Errorf("token = %q", tok)
	}
	if askedService != "Claude Code-credentials-5034c31c" {
		t.Errorf("must ask the Keychain for exactly the derived service, got %q", askedService)
	}
}

func TestClaudeKeychainTokenErrorsNameTheServiceSoTheProfileIsDiagnosable(t *testing.T) {
	src := ClaudeKeychainToken{
		Service: "Claude Code-credentials-5034c31c",
		Read:    func(string) ([]byte, error) { return nil, errors.New("item not found") },
	}
	_, err := src.Token()
	if err == nil || !strings.Contains(err.Error(), "Claude Code-credentials-5034c31c") {
		t.Fatalf("the error must say which Keychain item was read, got %v", err)
	}
}

func TestClaudeKeychainTokenWithoutAReaderIsAnErrorNotAKeychainAccess(t *testing.T) {
	_, err := ClaudeKeychainToken{Service: "Claude Code-credentials"}.Token()
	if err == nil || !strings.Contains(err.Error(), "no keychain reader wired") {
		t.Fatalf("an unwired reader must fail loudly, got %v", err)
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
