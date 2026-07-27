package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rererr/tomobit/internal/config"
	"github.com/Rererr/tomobit/internal/quota"
)

// tokenFuncForTest / staticTokenForTest are the cmd-package equivalents of the
// quota package's own test helpers (unreachable across the package boundary):
// a TokenSource that hands back a fixed token.
type tokenFuncForTest func() (string, error)

func (f tokenFuncForTest) Token() (string, error) { return f() }

func staticTokenForTest(tok string) quota.TokenSource {
	return tokenFuncForTest(func() (string, error) { return tok, nil })
}

// doer429ForTest is the wrong-profile / rate-limit shape measured 2026-07-24:
// a Doer that always answers HTTP 429, never opening a socket.
type doer429ForTest struct{}

func (doer429ForTest) Do(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"rate_limit_error"}}`)),
		Header:     http.Header{},
	}, nil
}

// TestMain keeps every test in package main offline for quota (ADR-0044's
// discipline: no test touches a real credential or the network). The
// production collector reads real endpoints; here it is replaced with one that
// returns nothing, so the existing cmdStatus tests see no quota and the
// quota-specific tests install their own fixtures locally.
func TestMain(m *testing.M) {
	collectQuotaFn = func(context.Context) []quotaStatus { return nil }
	// この機械の実 config を、テストに持ち込ませない。
	//
	// cfg は main.go の**パッケージ変数の初期化子**（`var cfg, cfgErr =
	// config.Load()`）なので、テストバイナリでも走って ~/.tomobit/config.json を
	// 読む。ここを空にしないと、開発者が自分の機械に何を配線したかでテストの
	// 挙動が変わる。
	//
	// 実害を1つ踏んだ (2026-07-27): ADR-0052 の test_commands を tomobit 自身の
	// リポジトリへ向けると、**第1層が再帰する** — 境界がテストを走らせ、その
	// テストが finishTask に届いて第1層を起動し、それがまたテストを走らせる。
	// `go test ./cmd/tomobit/` が10分のタイムアウトで落ちた。
	//
	// 配線を読むテストは自分で cfg を建てて後片付けする（wireFirstLayer など）。
	cfg = config.Config{}
	os.Exit(m.Run())
}

// fakeQuotaFetcher is an offline quota.Fetcher: it yields a canned snapshot or
// error without any network or credential read.
type fakeQuotaFetcher struct {
	provider string
	snap     quota.Snapshot
	err      error
}

func (f fakeQuotaFetcher) Provider() string { return f.provider }
func (f fakeQuotaFetcher) Fetch(context.Context) (quota.Snapshot, error) {
	return f.snap, f.err
}

// TestCollectQuotaKeepsBothProvidersAndSortsThem guards the Decision 5 shape:
// a failed Provider is not dropped (silence would read as "no limits") — it
// becomes a row with an Error and no Windows, sorted deterministically beside
// the observed one.
func TestCollectQuotaKeepsBothProvidersAndSortsThem(t *testing.T) {
	reset := time.UnixMilli(1_700_000_000_000)
	observed := time.UnixMilli(1_699_999_000_000)
	fetchers := []quota.Fetcher{
		fakeQuotaFetcher{provider: "codex", err: errors.New("token rejected (credentials: /home/x/.codex/auth.json)")},
		fakeQuotaFetcher{provider: "claude-code", snap: quota.Snapshot{
			Provider:   "claude-code",
			ObservedAt: observed,
			Windows: []quota.Window{
				{Label: "five_hour", UsedPercent: 12.5, ResetsAt: reset},
				{Label: "seven_day", UsedPercent: 40},
			},
		}},
	}

	rows := collectQuota(context.Background(), fetchers)

	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (no Provider dropped)", len(rows))
	}
	if rows[0].Provider != "claude-code" || rows[1].Provider != "codex" {
		t.Fatalf("rows not sorted by provider: %q, %q", rows[0].Provider, rows[1].Provider)
	}

	claude := rows[0]
	if claude.Error != "" {
		t.Errorf("observed row carries an Error: %q", claude.Error)
	}
	if len(claude.Windows) != 2 {
		t.Fatalf("claude windows = %d, want 2", len(claude.Windows))
	}
	if claude.Windows[0].Label != "five_hour" || claude.Windows[0].UsedPercent != 12.5 {
		t.Errorf("window[0] = %+v, want five_hour 12.5%%", claude.Windows[0])
	}
	if claude.Windows[0].ResetsAt != reset.UnixMilli() {
		t.Errorf("window[0].ResetsAt = %d, want %d (unix ms)", claude.Windows[0].ResetsAt, reset.UnixMilli())
	}
	if claude.Windows[1].ResetsAt != 0 {
		t.Errorf("window[1].ResetsAt = %d, want 0 (vendor didn't say)", claude.Windows[1].ResetsAt)
	}
	if claude.ObservedAt != observed.UnixMilli() {
		t.Errorf("ObservedAt = %d, want %d", claude.ObservedAt, observed.UnixMilli())
	}

	codex := rows[1]
	if len(codex.Windows) != 0 {
		t.Errorf("failed row must carry no windows, got %v", codex.Windows)
	}
	if !strings.Contains(codex.Error, "token rejected") {
		t.Errorf("failed row Error = %q, want the fetcher's reason", codex.Error)
	}
	if codex.ObservedAt != 0 {
		t.Errorf("failed row ObservedAt = %d, want 0", codex.ObservedAt)
	}
}

// TestCollectQuotaErrorCarriesOriginNotToken guards Decision 5 追補 at the wire
// layer: the fetcher strips the token and leaves the credential origin, and
// collectQuota passes that through verbatim — the token never reaches a row.
func TestCollectQuotaErrorCarriesOriginNotToken(t *testing.T) {
	// A real ClaudeFetcher with a token source that hands back a secret, and an
	// HTTP seam that 429s — the exact wrong-profile shape measured 2026-07-24.
	secret := "sk-ant-oat-SECRET-TOKEN-DO-NOT-LEAK"
	f := &quota.ClaudeFetcher{
		Tokens:           staticTokenForTest(secret),
		HTTP:             doer429ForTest{},
		CredentialOrigin: `keychain "Claude Code-credentials-5034c31c"`,
	}
	rows := collectQuota(context.Background(), []quota.Fetcher{f})
	if len(rows) != 1 || rows[0].Error == "" {
		t.Fatalf("want one errored row, got %+v", rows)
	}
	if strings.Contains(rows[0].Error, secret) {
		t.Fatalf("row Error leaked the token: %q", rows[0].Error)
	}
	if !strings.Contains(rows[0].Error, "5034c31c") {
		t.Errorf("row Error = %q, want it to name the credential origin", rows[0].Error)
	}
	if !strings.Contains(rows[0].Error, "429") {
		t.Errorf("row Error = %q, want the 429 diagnostic (rate-limit or wrong profile)", rows[0].Error)
	}
}

// TestStatusJSONCarriesQuotaWhenLedgerExists guards the ADR-0039 path to the
// GUI: the machine view carries the quota rows the collector produced, one per
// Provider, with the observed/不明 split intact.
func TestStatusJSONCarriesQuotaWhenLedgerExists(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")
	now := time.Now().UnixMilli()
	statusJSONFixture(t, dbPath, now)

	prev := collectQuotaFn
	defer func() { collectQuotaFn = prev }()
	collectQuotaFn = func(context.Context) []quotaStatus {
		return []quotaStatus{
			{Provider: "claude-code", Windows: []quotaWindow{{Label: "five_hour", UsedPercent: 20}}, ObservedAt: now},
			{Provider: "codex", Error: "codex quota (credentials: /x/auth.json): stale"},
		}
	}

	out, _ := captureStdoutStderr(t, func() {
		if err := cmdStatus([]string{"--db", dbPath, "--view", "json"}); err != nil {
			t.Fatalf("cmdStatus: %v", err)
		}
	})
	got := decodeStatusJSON(t, out)

	q, ok := got["quota"].([]any)
	if !ok || len(q) != 2 {
		t.Fatalf("quota = %v, want two rows", got["quota"])
	}
	claude, _ := q[0].(map[string]any)
	if claude["provider"] != "claude-code" {
		t.Errorf("quota[0].provider = %v, want claude-code", claude["provider"])
	}
	windows, _ := claude["windows"].([]any)
	if len(windows) != 1 {
		t.Fatalf("quota[0].windows = %v, want one window", claude["windows"])
	}
	codex, _ := q[1].(map[string]any)
	if codex["error"] == nil || !strings.Contains(codex["error"].(string), "stale") {
		t.Errorf("quota[1].error = %v, want the 不明 reason", codex["error"])
	}
	if _, hasWindows := codex["windows"]; hasWindows {
		t.Errorf("a 不明 row must omit windows, got %v", codex["windows"])
	}
}

// TestStatusJSONOmitsQuotaWhenAbsent guards the omitempty contract: an empty
// collector result leaves the key entirely absent, not a null or [].
func TestStatusJSONOmitsQuotaWhenAbsent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")
	now := time.Now().UnixMilli()
	statusJSONFixture(t, dbPath, now)

	out, _ := captureStdoutStderr(t, func() {
		if err := cmdStatus([]string{"--db", dbPath, "--view", "json"}); err != nil {
			t.Fatalf("cmdStatus: %v", err)
		}
	})
	got := decodeStatusJSON(t, out)
	if _, has := got["quota"]; has {
		t.Errorf("quota = %v, want the key absent when the collector yields nothing", got["quota"])
	}
}

// TestStatusJSONNoLedgerOmitsQuota guards the absent-ledger contract:
// exists:false carries no other fields, quota included — even though quota is
// ledger-independent, the machine view attaches it only alongside a real
// ledger, matching providers/growth.
func TestStatusJSONNoLedgerOmitsQuota(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "nonexistent", "t.db")

	prev := collectQuotaFn
	defer func() { collectQuotaFn = prev }()
	collectQuotaFn = func(context.Context) []quotaStatus {
		return []quotaStatus{{Provider: "claude-code", Error: "boom"}}
	}

	out, _ := captureStdoutStderr(t, func() {
		if err := cmdStatus([]string{"--db", dbPath, "--view", "json"}); err != nil {
			t.Fatalf("cmdStatus: %v", err)
		}
	})
	got := decodeStatusJSON(t, out)
	if got["exists"] != false {
		t.Fatalf("exists = %v, want false", got["exists"])
	}
	if _, has := got["quota"]; has {
		t.Errorf("quota = %v, want absent when exists:false", got["quota"])
	}
}

// TestPrintQuotaRendersObservedAndUnknown guards the human 残量 block: the
// honesty header, the vendor figure with its reset, and a 不明（理由）for a
// Provider that could not be observed — never a 0%.
func TestPrintQuotaRendersObservedAndUnknown(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	reset := now.Add(3 * time.Hour).UnixMilli()
	var b strings.Builder
	rows := []quotaStatus{
		{Provider: "claude-code", Windows: []quotaWindow{
			{Label: "five_hour", UsedPercent: 12, ResetsAt: reset},
			{Label: "seven_day", UsedPercent: 40},
		}},
		{Provider: "codex", Error: "codex quota (credentials: /x/auth.json): stale — run `codex`"},
	}
	if err := printQuota(&b, rows, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"tomobitの保証ではない",
		"claude-code: five_hour 12%（あと3時間）",
		"seven_day 40%",
		"codex: 不明（codex quota (credentials: /x/auth.json): stale — run `codex`）",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("printQuota output missing %q\n---\n%s", want, out)
		}
	}
	// The 不明 row must carry no invented utilization figure at all.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "codex:") && strings.Contains(line, "%") {
			t.Errorf("不明 row invented a percentage: %q", line)
		}
	}
}

// TestRelativeResetTimeDropsPastAndZero guards the parenthetical: a vendor that
// didn't say (zero) or a reset already crossed both collapse to empty rather
// than "あと0分" or a negative.
func TestRelativeResetTimeDropsPastAndZero(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000).UnixMilli()
	if got := relativeResetTime(now, 0); got != "" {
		t.Errorf("zero ts = %q, want empty", got)
	}
	if got := relativeResetTime(now, now-1000); got != "" {
		t.Errorf("past ts = %q, want empty", got)
	}
	if got := relativeResetTime(now, now+90*60*1000); got != "あと1時間" {
		t.Errorf("90min ahead = %q, want あと1時間", got)
	}
	if got := relativeResetTime(now, now+30*60*1000); got != "あと30分" {
		t.Errorf("30min ahead = %q, want あと30分", got)
	}
}
