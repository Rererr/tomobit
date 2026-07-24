// quota.go wires ADR-0044's route-A observers (internal/quota) into the two
// status views. `tomobit status` reads each Provider's own usage endpoint with
// the user's own OAuth token and shows the vendor-reported utilization — an
// observation, never tomobit's own estimate (Decision 1). A fetch that cannot
// observe becomes 不明（理由）, not a 0% or a stale guess (Decision 5). The
// credentials are read at fetch time and discarded; the token never reaches a
// log, the ledger, or an error — an error carries only which credential origin
// was read (Keychain service or file path), the one fact a 429-vs-wrong-profile
// misdiagnosis needs (Decision 5 追補).
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Rererr/tomobit/internal/quota"
)

// quotaFetchTimeout bounds the whole status-view quota read. Shorter than the
// per-fetcher client timeout (internal/quota.fetchTimeout, 10s): a status
// glance must not hang on an unofficial endpoint that stopped answering — it
// degrades to 不明 instead of blocking the most common command.
const quotaFetchTimeout = 6 * time.Second

// quotaHTTPTimeout caps the claude fetcher's own client (codex's default
// constructor sets its own). Kept just under quotaFetchTimeout so the client,
// not the outer context, is normally the one to name a timeout.
const quotaHTTPTimeout = 5 * time.Second

// quotaStatus is one Provider's vendor-reported utilization for the status
// views. Exactly one of Windows / Error is populated: a Provider that could
// not be observed still appears, as 不明（Error）, so a reader can tell
// 「観測して余裕あり」from「観測できなかった」— never a silent omission that
// would read as "no limits" (ADR-0044 Decision 5).
type quotaStatus struct {
	Provider   string        `json:"provider"`
	Windows    []quotaWindow `json:"windows,omitempty"`
	Error      string        `json:"error,omitempty"`       // 不明 reason when Windows is empty
	ObservedAt int64         `json:"observed_at,omitempty"` // unix ms; 0 when Error
}

// quotaWindow is one rate-limit window, both vendors' clocks already
// normalized to unix ms by internal/quota (claude's RFC3339 and codex's epoch
// seconds are indistinguishable here).
type quotaWindow struct {
	Label       string  `json:"label"`
	UsedPercent float64 `json:"used_percent"`
	ResetsAt    int64   `json:"resets_at,omitempty"` // unix ms; 0 = vendor didn't say
}

// quotaCollector is the injection seam that keeps every `go test` offline:
// production builds read real endpoints, tests replace it (see TestMain) so no
// test touches a real credential or the network — the same discipline
// internal/quota holds for its own fetchers.
type quotaCollector func(ctx context.Context) []quotaStatus

// collectQuotaFn is the collector cmdStatus uses. A package var so TestMain can
// swap the real reader out; chat's inline /status passes nil instead, staying
// offline between turns (a per-turn network wait is not what a chat is for).
var collectQuotaFn quotaCollector = func(ctx context.Context) []quotaStatus {
	return collectQuota(ctx, defaultQuotaFetchers())
}

// collectQuota fetches every Provider concurrently and returns one row each,
// sorted by Provider so the view is deterministic. A fetch error becomes the
// row's Error (不明) rather than dropping the Provider — the fetcher already
// stripped the token from that message and left only the credential origin.
func collectQuota(ctx context.Context, fetchers []quota.Fetcher) []quotaStatus {
	out := make([]quotaStatus, len(fetchers))
	var wg sync.WaitGroup
	for i, f := range fetchers {
		wg.Add(1)
		go func(i int, f quota.Fetcher) {
			defer wg.Done()
			snap, err := f.Fetch(ctx)
			if err != nil {
				out[i] = quotaStatus{Provider: f.Provider(), Error: err.Error()}
				return
			}
			out[i] = snapshotToStatus(snap)
		}(i, f)
	}
	wg.Wait()
	sort.Slice(out, func(a, b int) bool { return out[a].Provider < out[b].Provider })
	return out
}

func snapshotToStatus(s quota.Snapshot) quotaStatus {
	windows := make([]quotaWindow, len(s.Windows))
	for i, w := range s.Windows {
		var resetsAt int64
		if !w.ResetsAt.IsZero() {
			resetsAt = w.ResetsAt.UnixMilli()
		}
		windows[i] = quotaWindow{Label: w.Label, UsedPercent: w.UsedPercent, ResetsAt: resetsAt}
	}
	var observed int64
	if !s.ObservedAt.IsZero() {
		observed = s.ObservedAt.UnixMilli()
	}
	return quotaStatus{Provider: s.Provider, Windows: windows, ObservedAt: observed}
}

// errorFetcher surfaces a wiring-time failure (no credentials, home unresolved)
// as 不明（理由）in the view rather than dropping the Provider silently —
// silence would read as "no limits", the very thing Decision 5 forbids.
type errorFetcher struct {
	provider string
	err      error
}

func (f errorFetcher) Provider() string { return f.provider }
func (f errorFetcher) Fetch(context.Context) (quota.Snapshot, error) {
	return quota.Snapshot{}, f.err
}

// defaultQuotaFetchers assembles the route-A observers for this machine: the
// codex CLI's own auth file, and — for claude — the credential origin that
// actually exists, targeting the SAME profile the claude-code adapter launches
// with (env > config claude_config_dir). Reading the default Keychain item for
// a non-default profile surfaces as a 429 that lies about being rate-limited
// (ADR-0044 実測3), so the service name is derived from the resolved config
// dir, never assumed.
func defaultQuotaFetchers() []quota.Fetcher {
	return []quota.Fetcher{buildClaudeFetcher(), buildCodexFetcher()}
}

func buildClaudeFetcher() quota.Fetcher {
	src, origin, err := claudeTokenSource(resolvedClaudeConfigDir())
	if err != nil {
		// The provider string mirrors internal/quota.claudeProvider; it is
		// unexported there and this is the one place outside that package that
		// must name it, so a literal (not an import) is the smaller coupling.
		return errorFetcher{provider: "claude-code", err: err}
	}
	return &quota.ClaudeFetcher{
		Tokens:           src,
		HTTP:             &http.Client{Timeout: quotaHTTPTimeout},
		CredentialOrigin: origin,
	}
}

func buildCodexFetcher() quota.Fetcher {
	f, err := quota.NewCodexFetcher()
	if err != nil {
		return errorFetcher{provider: "codex", err: err}
	}
	return f
}

// resolvedClaudeConfigDir mirrors wireClaude's env > config precedence so the
// quota reader targets the exact profile the claude-code adapter runs. An empty
// string ("explicitly inherit the parent env") maps to the default Keychain
// service — what claude-code itself would use.
func resolvedClaudeConfigDir() string {
	if dir, ok := os.LookupEnv("TOMOBIT_CLAUDE_CONFIG_DIR"); ok {
		return dir
	}
	if cfg.ClaudeConfigDir != nil {
		return *cfg.ClaudeConfigDir
	}
	return ""
}

// claudeTokenSource picks the credential origin that actually exists on this
// machine: the on-disk credentials file if present (file-type installs), else
// the macOS Keychain item for the resolved profile. The origin string it
// returns rides every fetch error (Decision 5 追補) so a 不明 names which
// credentials were read — never the token itself.
func claudeTokenSource(configDir string) (quota.TokenSource, string, error) {
	if path, err := quota.ClaudeCredentialsPath(); err == nil {
		if _, statErr := os.Stat(path); statErr == nil {
			return quota.ClaudeFileToken{Path: path}, path, nil
		}
	}
	if runtime.GOOS == "darwin" {
		service := quota.ClaudeKeychainServiceName(configDir)
		return quota.ClaudeKeychainToken{Service: service, Read: quota.ReadClaudeKeychain},
			"keychain " + strconv.Quote(service), nil
	}
	return nil, "", fmt.Errorf("no claude credentials: no file at ~/.claude/.credentials.json and Keychain is macOS-only")
}

// printQuota renders the vendor-reported utilization block showStatus prints
// after the provider-usage table. The header names the honesty boundary
// (ADR-0044 Consequences item 2): this is the Provider's own declared figure,
// not a number tomobit stands behind. A Provider that could not be observed
// prints 不明（理由）— never a 0% or a stale value (Decision 5).
func printQuota(w io.Writer, rows []quotaStatus, now int64) error {
	if _, err := fmt.Fprintln(w, "残量（各Providerの申告値・tomobitの保証ではない）"); err != nil {
		return err
	}
	for _, r := range rows {
		if len(r.Windows) == 0 {
			if _, err := fmt.Fprintf(w, "  %s: 不明（%s）\n", r.Provider, quotaUnknownReason(r.Error)); err != nil {
				return err
			}
			continue
		}
		parts := make([]string, len(r.Windows))
		for i, wnd := range r.Windows {
			if reset := relativeResetTime(now, wnd.ResetsAt); reset != "" {
				parts[i] = fmt.Sprintf("%s %.0f%%（%s）", wnd.Label, wnd.UsedPercent, reset)
			} else {
				parts[i] = fmt.Sprintf("%s %.0f%%", wnd.Label, wnd.UsedPercent)
			}
		}
		if _, err := fmt.Fprintf(w, "  %s: %s\n", r.Provider, strings.Join(parts, "  ")); err != nil {
			return err
		}
	}
	return nil
}

// quotaUnknownReason keeps the fetcher's diagnostic verbatim (it already omits
// the token, carrying only the credential origin) and only invents text when
// there is none — a blank 不明 would strip the very reason Decision 5 requires.
func quotaUnknownReason(err string) string {
	if strings.TrimSpace(err) == "" {
		return "理由不明"
	}
	return err
}

// relativeResetTime names how far ahead a window resets, for the human 残量
// block. Empty when the vendor didn't say (zero ts) or the reset is already
// past (a snapshot read across the boundary) — the caller drops the
// parenthetical rather than inventing "あと0分" or a negative.
func relativeResetTime(now, ts int64) string {
	if ts <= 0 || ts <= now {
		return ""
	}
	d := time.Duration(ts-now) * time.Millisecond
	switch {
	case d < time.Hour:
		return fmt.Sprintf("あと%d分", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("あと%d時間", int(d/time.Hour))
	default:
		return fmt.Sprintf("あと%d日", int(d/(24*time.Hour)))
	}
}
