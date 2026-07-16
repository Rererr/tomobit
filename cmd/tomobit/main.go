// tomobit — the minimal core loop: record → perceive → connect → rebuild.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Rererr/tomobit/internal/config"
	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/curiosity"
	"github.com/Rererr/tomobit/internal/decide"
	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/executor/claudecode"
	"github.com/Rererr/tomobit/internal/executor/codex"
	"github.com/Rererr/tomobit/internal/face"
	"github.com/Rererr/tomobit/internal/perceive"
	"github.com/Rererr/tomobit/internal/plan"
	"github.com/Rererr/tomobit/internal/reflection"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/voice"
)

const extractorVer = 3 // bump when the extraction prompt/schema changes

// providers holds the registered adapters (SCHEMA.md R3 provider names).
// `--provider <name>` is the human's explicit pick; `--provider auto` hands
// the choice to the Decision Engine (ADR-0012: 悲観分位点ゲート + Thompson
// Sampling), which records its seed to events so the lottery replays.
var providers = map[string]executor.Adapter{
	"claude-code": newClaudeCode(),
	"codex":       codex.New(),
}

// cfg is the machine-local wiring file (~/.tomobit/config.json, ADR-0021),
// written by `tomobit setup`. A load error is stashed, not fatal: every
// command still runs on env/flags, and run() warns once so a typo in the
// file is never silent.
var cfg, cfgErr = config.Load()

// claudeProfileSet reports whether a claude-code profile was chosen at all —
// via env (even empty = "explicitly inherit") or via config. claude-code
// refuses to launch without a choice — see the gate in cmdDo.
var claudeProfileSet bool

var claudeAdapter = claudecode.New()

// newClaudeCode wires the claude-code launch profile. No baked-in default:
// which profile (account) a run uses must be an explicit choice, because
// silently inheriting whatever the shell happens to have is exactly the
// accident this exists to prevent. Resolution is env > config (a flag has
// no seat here — the profile is not a per-run choice):
// TOMOBIT_CLAUDE_CONFIG_DIR / config claude_config_dir select the profile
// (empty value = explicitly inherit the parent env); TOMOBIT_CLAUDE_ARGS /
// config claude_args add flags to every launch (optional).
func newClaudeCode() *claudecode.Adapter {
	wireClaude()
	return claudeAdapter
}

// wireClaude (re)applies the env > config resolution to the shared adapter.
// Called again after `tomobit setup` or the in-run profile question writes
// a fresh config, so the current process picks the choice up immediately.
func wireClaude() {
	if dir, ok := os.LookupEnv("TOMOBIT_CLAUDE_CONFIG_DIR"); ok {
		claudeProfileSet = true
		claudeAdapter.ConfigDir = dir
	} else if cfg.ClaudeConfigDir != nil {
		claudeProfileSet = true
		claudeAdapter.ConfigDir = *cfg.ClaudeConfigDir
	} else {
		claudeProfileSet = false
		claudeAdapter.ConfigDir = ""
	}
	if v, ok := os.LookupEnv("TOMOBIT_CLAUDE_ARGS"); ok {
		claudeAdapter.ExtraArgs = strings.Fields(v)
	} else {
		claudeAdapter.ExtraArgs = cfg.ClaudeArgs
	}
}

func providerNames() []string {
	names := make([]string, 0, len(providers))
	for n := range providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// resolveProvider looks up a registered Adapter by name, or reports the
// available names so an unknown --provider fails with something actionable.
func resolveProvider(name string) (executor.Adapter, error) {
	if a, ok := providers[name]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("do: unknown provider %q (available: %s, human, auto)",
		name, strings.Join(providerNames(), ", "))
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if cfgErr != nil {
		fmt.Fprintln(os.Stderr, "warning: config ignored:", cfgErr, "— `tomobit setup` rewrites it")
	}
	if len(args) == 0 {
		// ADR-0008 Consequences: the first screen is the companion view, not
		// the manual — usage moved to `tomobit help`.
		return cmdStatus(nil)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "do":
		return cmdDo(rest)
	case "record":
		return cmdRecord(rest)
	case "perceive":
		return cmdPerceive(rest)
	case "rebuild":
		return cmdRebuild(rest)
	case "status":
		return cmdStatus(rest)
	case "setup":
		return cmdSetup(rest)
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		if strings.HasPrefix(cmd, "-") {
			// A bare `tomobit --db X` names no subcommand — route the flags to
			// the companion view instead of failing as "unknown command".
			return cmdStatus(args)
		}
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func usage() {
	fmt.Println(`tomobit — a living harness that grows with you

usage:
  tomobit          (no args) companion view — avatar, mood, a line, connections
  tomobit do       [--cap implement] [--timeout 0] [--permission-mode <mode>]
                   [--provider claude-code|codex|human|auto]
                   [--plan auto|full|direct|quick|<steps>] [--size small|medium|large]
                   [--model qwen3:8b] [--url http://localhost:11434] "<prompt>"
                   --provider auto: 決定エンジン(ADR-0012)が能力ゲート+TSで選ぶ
                   （humanも候補 — ADR-0018）。--provider human: 自分でやって
                   同じ台帳に乗せる。--plan auto: 手順も台帳が選ぶ(ADR-0014、
                   例 analyze>implement>test)。--size は判断の温度 n(stakes)
  tomobit record   --session <id> --type <event.type> [--json '{...}']
  tomobit perceive [--model qwen3:8b] [--url http://localhost:11434]
  tomobit rebuild
  tomobit status   same as no args
  tomobit setup    対話式でこの機械の配線を決める(claude profile / ollama)。
                   再実行すれば診断を兼ねる。書き先は ~/.tomobit/config.json

companion markers: "?" = a connection is questioned / "z" = dormant (long quiet)

common flags:
  --db <path>   database file (default ~/.tomobit/tomobit.db, or $TOMOBIT_DB)

config precedence: flag > env > ~/.tomobit/config.json
  env overrides: TOMOBIT_DB, TOMOBIT_CLAUDE_CONFIG_DIR, TOMOBIT_CLAUDE_ARGS`)
}

func dbFlag(fs *flag.FlagSet) *string {
	def := os.Getenv("TOMOBIT_DB")
	if def == "" {
		def = cfg.DB
	}
	if def == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			def = filepath.Join(home, ".tomobit", "tomobit.db")
		} else {
			def = "tomobit.db"
		}
	}
	return fs.String("db", def, "database file")
}

// ollamaModelDefault is the --model default: config, then the ADR-0005
// measured pick.
func ollamaModelDefault() string {
	if cfg.OllamaModel != "" {
		return cfg.OllamaModel
	}
	return "qwen3:8b"
}

func openStore(path string) (*store.Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return store.Open(path)
}

// isTTY reports whether f is a character device (an interactive terminal),
// not a pipe or redirected file. Shared by the Curiosity question (stdin,
// ADR-0007) and the companion-view avatar (stdout, ADR-0008 Decision 4) so
// both honor the same non-interactive detection.
func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func cmdRecord(args []string) error {
	fs := flag.NewFlagSet("record", flag.ExitOnError)
	db := dbFlag(fs)
	session := fs.String("session", "", "session id (required)")
	typ := fs.String("type", "", "event type, e.g. task.started (required)")
	payload := fs.String("json", "{}", "event payload as JSON")
	fs.Parse(args)
	if *session == "" || *typ == "" {
		return fmt.Errorf("record: --session and --type are required")
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(*payload), &p); err != nil {
		return fmt.Errorf("record: --json is not valid JSON: %w", err)
	}
	s, err := openStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.AppendEvent(*session, *typ, time.Now().UnixMilli(), p)
}

func cmdDo(args []string) error {
	fs := flag.NewFlagSet("do", flag.ExitOnError)
	db := dbFlag(fs)
	capability := fs.String("cap", "implement", "capability of the task")
	timeout := fs.Duration("timeout", 0, "max run time, 0 = no limit")
	permMode := fs.String("permission-mode", "", "permission mode passthrough (claude --permission-mode / codex --sandbox)")
	providerName := fs.String("provider", "claude-code", "adapter to run: claude-code|codex|auto")
	planArg := fs.String("plan", "", "plan: auto, a label (full|direct|quick), or steps like analyze>implement>test")
	size := fs.String("size", "", "task size for decision stakes: small|medium|large (--provider auto)")
	model := fs.String("model", ollamaModelDefault(), "ollama model for best-effort perception")
	url := fs.String("url", cfg.OllamaURL, "ollama base url (default http://localhost:11434)")
	fs.Parse(args)

	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		return fmt.Errorf("do: a prompt is required")
	}
	// A named provider fails fast, before any event is recorded. auto is
	// resolved after the session opens — the decision reads the projections.
	// human is a provider with no adapter (ADR-0018 Decision 2): the same
	// ledger, gate, and rehabilitation, executed by the user.
	var adapter executor.Adapter
	var err error
	human := *providerName == "human"
	if *providerName != "auto" && !human {
		if adapter, err = resolveProvider(*providerName); err != nil {
			return err
		}
	}
	// auto can pick claude-code, so both paths need the profile. Checked
	// here, before any event is recorded, so a misconfigured shell never
	// pollutes the ledger with a run that was doomed at launch. On a
	// terminal the missing choice becomes the question itself (ADR-0021);
	// headless (daemon, cron, pipe) it stays a hard error.
	if (*providerName == "claude-code" || *providerName == "auto") && !claudeProfileSet {
		if !isTTY(os.Stdin) || !isTTY(os.Stdout) {
			return fmt.Errorf("do: no claude-code profile chosen — run `tomobit setup`,\n" +
				"  or export TOMOBIT_CLAUDE_CONFIG_DIR=$HOME/.claude-personal\n" +
				"  (set it empty to deliberately inherit the parent environment)")
		}
		fmt.Println("claude-code のプロファイルがまだ決まっていない。")
		if err := askClaudeProfile(bufio.NewReader(os.Stdin), os.Stdout); err != nil {
			return err
		}
	}

	s, err := openStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()

	sid := store.NewID(time.Now().UnixMilli())
	if err := s.AppendEvent(sid, "task.started", time.Now().UnixMilli(),
		map[string]any{"intent": prompt, "source": "production"}); err != nil {
		return err
	}
	if err := s.AppendEvent(sid, "capability.started", time.Now().UnixMilli(),
		map[string]any{"capability": *capability}); err != nil {
		return err
	}

	if adapter == nil && !human {
		dec, err := autoDecide(s, sid, *capability, *size)
		if err != nil {
			return err
		}
		if dec.Provider == "human" {
			human = true
		} else {
			adapter = providers[dec.Provider]
		}
	}

	// Plan selection (ADR-0014). The human path skips it: a human run has no
	// step boundary the harness could drive.
	planName := ""
	if !human {
		if planName, err = resolvePlan(s, sid, *planArg, *capability, *size); err != nil {
			return err
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var result executor.Result
	var runErr error
	if human {
		result, runErr = runHuman(s, sid, os.Stdin)
		if runErr != nil {
			return runErr
		}
	} else {
		sink := func(ev executor.Event, ts int64) error {
			// The user must read the result to judge adoption, so assistant text
			// is echoed to the terminal, not just recorded.
			if ev.Type == executor.EventProviderOutput {
				if text, ok := ev.Payload["text"].(string); ok && text != "" {
					fmt.Println(text)
				}
			}
			return s.AppendEvent(sid, ev.Type, ts, ev.Payload)
		}

		// Warn (malformed stream lines) is always visible; Debug (recognised,
		// intentionally dropped lines) only under TOMOBIT_DEBUG.
		ex := &executor.Executor{Adapter: adapter, Stderr: os.Stderr, Warn: os.Stderr}
		if os.Getenv("TOMOBIT_DEBUG") != "" {
			ex.Debug = os.Stderr
		}

		// One provider run per plan step (ADR-0014); no plan = one plain
		// run. The plan stops at the first failed step — later steps would
		// be working on a broken premise.
		steps := []string{""}
		if planName != "" {
			steps = plan.Steps(planName)
		}
		for i, step := range steps {
			if len(steps) > 1 {
				fmt.Printf("-- plan %d/%d: %s --\n", i+1, len(steps), step)
			}
			result, runErr = ex.Run(ctx, executor.Request{
				Prompt: stepPrompt(prompt, step, i, len(steps)), PermissionMode: *permMode, Timeout: *timeout,
			}, sink)
			if ctx.Err() != nil || runErr != nil || result.ExitCode != 0 {
				break
			}
		}
	}

	// SIGINT: the child was already signalled; record the cancellation and
	// stop, skipping the adoption prompt. The session stays pending, so
	// `tomobit perceive` can still learn from it later.
	if ctx.Err() != nil {
		return s.AppendEvent(sid, "task.cancelled", time.Now().UnixMilli(), nil)
	}

	if payload, need := providerErrorPayload(runErr, result); need {
		if err := s.AppendEvent(sid, "provider.error", time.Now().UnixMilli(), payload); err != nil {
			return err
		}
	}

	// The adoption question runs at the end of every completed run (ADR-0006
	// Decision 4): exit 0 is not adoption, and even a failed run may have
	// produced something the user keeps. A run that never started (e.g. the
	// claude binary is missing) produced nothing to judge, so the question is
	// skipped and the outcome carries no signal.
	payload := map[string]any{}
	if result.Started {
		payload = adoptionPayload(os.Stdin)
	}
	if err := s.AppendEvent(sid, "task.finished", time.Now().UnixMilli(), payload); err != nil {
		return err
	}

	// The reflection snapshot is taken before perception so that whatever
	// the coming Apply batch discovers (Split, 逆転, Questioned, 名誉回復)
	// shows up as a before/after difference (ADR-0015 Decision 2).
	snap, snapErr := reflection.TakeSnapshot(s, time.Now().UnixMilli())
	if snapErr != nil {
		fmt.Fprintln(os.Stderr, "reflection: snapshot failed:", snapErr)
	}

	extras := perceiveBestEffort(s, &perceive.Ollama{URL: *url, Model: *model})

	// ADR-0007 lists the question right after the adoption prompt, but it runs
	// here — after perception — so today's do is already folded into the Gap
	// derivation. The interruption is still once per do at the same boundary,
	// so the position ADR-0007 protects is unchanged.
	askBestEffort(s, sid)

	if snapErr == nil {
		reflectBestEffort(s, snap, extras, sid)
	}
	return nil
}

// reflectBestEffort runs the mirror at the do boundary (ADR-0015):
// detection over the before/after snapshots, one telling a day at most,
// reaction recorded as a Learning Reality. Best-effort — a failure prints
// to stderr but never fails the do.
func reflectBestEffort(s *store.Store, snap *reflection.Snapshot, extras []reflection.Candidate, doSession string) {
	reflectWithIO(s, snap, extras, doSession, os.Stdin, os.Stdout, isTTY(os.Stdin), time.Now().UnixMilli())
}

// reflectWithIO is reflectBestEffort's testable core (the askWithIO split).
// extras are candidates the snapshot comparison cannot see (re-perception,
// ADR-0019 Decision 4). The seed doubles as nowMs: millisecond resolution is
// plenty for a 1/day lottery, and RecordAndApply persists it either way.
func reflectWithIO(s *store.Store, snap *reflection.Snapshot, extras []reflection.Candidate, doSession string, in io.Reader, out io.Writer, interactive bool, now int64) {
	// A pipe has no reader to mirror to; the budget stays unspent.
	if !interactive {
		return
	}
	ok, err := reflection.HasBudget(s, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reflection: budget check failed:", err)
		return
	}
	if !ok {
		return
	}
	cands, err := reflection.Detect(snap, s, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reflection: detection failed:", err)
		return
	}
	cands = append(cands, extras...)
	if len(cands) == 0 {
		return
	}
	exps, err := s.CurrentExperiences()
	if err != nil {
		fmt.Fprintln(os.Stderr, "reflection: ledger read failed:", err)
		return
	}
	cand := reflection.Pick(cands, exps, now, now)
	reaction := reflection.Ask(in, out, cand.Text())
	en := &core.Engine{Repo: s}
	if err := reflection.RecordAndApply(s, en, cand, now, reaction, doSession, extractorVer, now); err != nil {
		fmt.Fprintln(os.Stderr, "reflection: recording failed:", err)
	}
}

// runHuman is the Human Executor path (ADR-0018 Decision 2): the ledger —
// or the user with --provider human — routed this task to the human. Tomo
// records the routing like any provider.selected, waits, and the ordinary
// adoption/perception tail turns the work into a human experience on the
// same ledger, same decay, same gate.
func runHuman(s *store.Store, sid string, in io.Reader) (executor.Result, error) {
	if err := s.AppendEvent(sid, "provider.selected", time.Now().UnixMilli(),
		map[string]any{"provider": "human"}); err != nil {
		return executor.Result{}, err
	}
	fmt.Printf("\n「%s」\n", voice.RouteHuman())
	// EOF (piped stdin) falls straight through: the adoption prompt then
	// also sees EOF and records no signal, which is correct for a headless
	// run that no human actually performed.
	bufio.NewReader(in).ReadString('\n')
	return executor.Result{Started: true}, nil
}

// resolvePlan turns the --plan flag into a canonical plan name and records
// the choice (ADR-0014). "" keeps the plain single-run behavior and records
// nothing; "auto" derives the live menu, lets Curiosity propose into free
// space, and runs the same decision rule over the plan bets; anything else
// is a label or explicit steps.
func resolvePlan(s *store.Store, sid, arg, capability, size string) (string, error) {
	switch arg {
	case "":
		return "", nil
	case "auto":
		now := time.Now()
		menu, err := plan.Live(s, capability, now.UnixMilli())
		if err != nil {
			return "", err
		}
		if len(menu) == 0 {
			return "", nil
		}
		// Curiosity proposal (ADR-0014 Decision 3/5): a newborn enters the
		// menu as a blank slate — TS gives it its first light task.
		if proposed, err := plan.Propose(s, capability, menu, now.UnixMilli()); err != nil {
			return "", err
		} else if proposed != "" {
			menu = append(menu, proposed)
			fmt.Printf("proposed plan %s\n", proposed)
		}
		conns, err := s.AllConnections()
		if err != nil {
			return "", err
		}
		tokens := []string{core.CanonToken("cap", capability)}
		dec := decide.ChoosePlan(conns, menu, tokens, size, now.UnixNano(), now.UnixMilli())
		if err := s.AppendEvent(sid, "plan.selected", now.UnixMilli(), map[string]any{
			"plan": dec.Provider, "seed": strconv.FormatInt(dec.Seed, 10),
			"n": dec.N, "q": dec.Q, "fallback": dec.Fallback,
			"cap": capability, "size": size,
		}); err != nil {
			return "", err
		}
		fmt.Printf("plan: %s (n=%d)\n", dec.Provider, dec.N)
		return dec.Provider, nil
	default:
		name, ok := plan.Resolve(capability, arg)
		if !ok {
			return "", fmt.Errorf("do: unknown --plan %q (auto, full|direct|quick, or steps like analyze>implement>test)", arg)
		}
		if err := s.AppendEvent(sid, "plan.selected", time.Now().UnixMilli(), map[string]any{
			"plan": name, "cap": capability, "manual": true,
		}); err != nil {
			return "", err
		}
		return name, nil
	}
}

// stepInstruction frames each capability step deterministically — harness
// text, not model judgment (ADR-0014 Decision 2: LLMはPlanを生成しない).
var stepInstruction = map[string]string{
	"analyze":   "変更は行わず、対象を分析して要点を報告する",
	"implement": "実装する",
	"review":    "ここまでの変更内容をレビューし、問題があれば修正する",
	"refactor":  "挙動を変えずにコードを整理する",
	"summarize": "ここまでの結果を要約する",
	"test":      "テストを実行し、失敗があれば修正する",
	"benchmark": "ベンチマークを実行して結果を報告する",
	"commit":    "変更をコミットする",
	"deploy":    "デプロイする",
	"notify":    "結果を通知する",
}

// stepPrompt wraps the user's prompt with the current step's framing. A
// plain run (no plan) passes the prompt through untouched.
func stepPrompt(prompt, step string, i, total int) string {
	if step == "" || total <= 1 && stepInstruction[step] == "" {
		return prompt
	}
	inst := stepInstruction[step]
	if inst == "" {
		inst = step
	}
	return fmt.Sprintf("%s\n\n[plan %d/%d: %s] このステップでは%s。", prompt, i+1, total, step, inst)
}

// autoDecide runs the Decision Engine (ADR-0012) over the current
// projections and records the full audit — seed included — as a
// tomo.decided event, so the same ledger + the same seed replays the same
// choice. The seed is stored as a string: a UnixNano exceeds JSON's exact
// float64 integer range and would silently lose the bits that make the
// lottery replayable.
func autoDecide(s *store.Store, sid, capability, size string) (decide.Decision, error) {
	conns, err := s.AllConnections()
	if err != nil {
		return decide.Decision{}, err
	}
	now := time.Now()
	tokens := []string{core.CanonToken("cap", capability)}
	// human competes on the same ledger with the same gate (ADR-0018
	// Decision 2) — the engine may honestly route the task to the user.
	candidates := append(providerNames(), "human")
	dec := decide.Choose(conns, candidates, tokens, size, now.UnixNano(), now.UnixMilli())

	cands := make([]map[string]any, len(dec.Candidates))
	for i, c := range dec.Candidates {
		cands[i] = map[string]any{
			"provider": c.Provider, "quantile": c.Quantile,
			"passed": c.Passed, "scope": c.ScopeKey,
		}
	}
	payload := map[string]any{
		"provider": dec.Provider, "seed": strconv.FormatInt(dec.Seed, 10),
		"n": dec.N, "q": dec.Q, "fallback": dec.Fallback,
		"cap": capability, "size": size, "candidates": cands,
	}
	if err := s.AppendEvent(sid, "tomo.decided", now.UnixMilli(), payload); err != nil {
		return decide.Decision{}, err
	}
	// Operational log line (ADR-0009: machine channel, not Tomo's voice).
	if dec.Fallback {
		fmt.Printf("decided %s (every provider below the gate — least pessimistic chosen)\n", dec.Provider)
	} else {
		fmt.Printf("decided %s (n=%d)\n", dec.Provider, dec.N)
	}
	// The calibrated voice (ADR-0019 Decision 1): confidence follows the
	// judgment's wobble, measured with the same sampler the decision used.
	// human speaks its own routing line in runHuman instead.
	if dec.Provider != "human" {
		w := decide.Wobble(conns, candidates, tokens, size, 64, 1, now.UnixMilli())
		fmt.Printf("\n「%s」\n\n", voice.Decided(dec.Provider, w))
	}
	return dec, nil
}

// askBestEffort runs the Curiosity question at the do boundary (ADR-0007).
// Best-effort: any failure prints to stderr but never fails the do.
func askBestEffort(s *store.Store, doSession string) {
	askWithIO(s, doSession, os.Stdin, os.Stdout, isTTY(os.Stdin), time.Now().UnixMilli())
}

// askWithIO is askBestEffort's testable core (same split as
// adoptionPayload): stdin/stdout/interactivity/clock are injected so the
// budget check and the recording side effect can be exercised without a
// real terminal.
func askWithIO(s *store.Store, doSession string, in io.Reader, out io.Writer, interactive bool, now int64) {
	// Non-interactive stdin has no human to interrupt, so we neither ask nor
	// record: the budget guards interruption frequency, and recording a
	// tomo.asked here would silently burn 24h of budget on a headless run that
	// interrupted no one.
	if !interactive {
		return
	}
	ok, err := curiosity.HasBudget(s, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "curiosity: budget check failed:", err)
		return
	}
	if !ok {
		return
	}
	gaps, err := curiosity.Gaps(s, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "curiosity: gap derivation failed:", err)
		return
	}
	if len(gaps) == 0 {
		return
	}
	gap := gaps[0]
	fmt.Fprintln(out) // speaker separation: a blank line before Tomo's question
	preferred, over, answered := curiosity.Ask(in, out, gap)
	en := &core.Engine{Repo: s}
	if err := curiosity.RecordAndPerceive(s, en, gap, preferred, over, answered, doSession, extractorVer, now); err != nil {
		fmt.Fprintln(os.Stderr, "curiosity: recording failed:", err)
	}
}

// providerErrorPayload decides whether the caller still needs to record its
// own provider.error. It skips that when the adapter's stream already
// reported one (Result.ErrorReported) — otherwise the same failure would be
// recorded twice — but still records executor-level failures (start error,
// timeout, a crash with no matching stream error) that the adapter never saw.
func providerErrorPayload(runErr error, result executor.Result) (payload map[string]any, need bool) {
	if runErr == nil && result.ExitCode == 0 {
		return nil, false
	}
	if result.ErrorReported {
		return nil, false
	}
	msg := fmt.Sprintf("provider exited with code %d", result.ExitCode)
	if runErr != nil {
		msg = runErr.Error()
	}
	return map[string]any{"message": msg}, true
}

// adoptionPayload asks the one closing question and maps the answer to a
// task.finished payload (ADR-0006 Decision 4). "s" and unreadable input
// (including EOF on non-interactive stdin) carry no learning signal, so the
// payload is empty — EOF must never be read as "Enter" (both trim to ""),
// or a headless run with no terminal would be silently recorded as adopted.
func adoptionPayload(in io.Reader) map[string]any {
	fmt.Print("採用? [Enter=そのまま / e=手直しあり / r=破棄 / s=わからない] ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil {
		return map[string]any{}
	}
	switch strings.TrimSpace(strings.ToLower(line)) {
	case "":
		return map[string]any{"adopted": "as-is", "reverted": false}
	case "e":
		return map[string]any{"adopted": "with-edits", "reverted": false}
	case "r":
		return map[string]any{"adopted": "", "reverted": true}
	default:
		return map[string]any{}
	}
}

// perceiveBestEffort mirrors cmdPerceive but never fails the run: if Ollama is
// down the session stays pending (Deferred Perception, ADR-0006 Decision 5).
// It returns any re-perception candidates (ADR-0019 Decision 4) for the
// reflection boundary that follows.
//
// Unlike cmdPerceive, the do user gets no per-experience machine log line —
// the one spoken line (ADR-0009) replaces it, since a do session's user
// wants to hear what Tomo learned, not read an extraction trace.
func perceiveBestEffort(s *store.Store, extractor perceive.Extractor) []reflection.Candidate {
	p := &perceive.Perceiver{
		Store:     s,
		Extractor: extractor,
		Ver:       extractorVer,
	}
	beforeCurrent, err := s.CurrentExperiences()
	if err != nil {
		fmt.Println("perception pending — run `tomobit perceive` later:", err)
		return nil
	}
	exps, err := p.Run()
	if err != nil {
		fmt.Println("perception pending — run `tomobit perceive` later:", err)
		return nil
	}
	if len(exps) == 0 {
		return nil
	}
	// Apply in the (ts, id) order the store replays on rebuild, so the live
	// projection matches the canonical rebuilt one.
	sort.Slice(exps, func(i, j int) bool {
		if exps[i].TS != exps[j].TS {
			return exps[i].TS < exps[j].TS
		}
		return exps[i].ID < exps[j].ID
	})

	now := time.Now().UnixMilli()
	before, err := s.AllConnections()
	if err != nil {
		fmt.Printf("perceived but projection is stale — run `tomobit rebuild`: %v\n", err)
		return nil
	}
	stageBefore, err := face.StageFrom(s, now)
	if err != nil {
		fmt.Printf("perceived but projection is stale — run `tomobit rebuild`: %v\n", err)
		return nil
	}

	en := &core.Engine{Repo: s}
	expIDs := make([]string, 0, len(exps))
	for _, e := range exps {
		if err := en.Apply(e); err != nil {
			fmt.Printf("perceived but projection is stale — run `tomobit rebuild`: %v\n", err)
			return nil
		}
		expIDs = append(expIDs, e.ID)
	}

	after, err := s.AllConnections()
	if err != nil {
		fmt.Printf("perceived but projection is stale — run `tomobit rebuild`: %v\n", err)
		return nil
	}
	stageAfter, err := face.StageFrom(s, now)
	if err != nil {
		fmt.Printf("perceived but projection is stale — run `tomobit rebuild`: %v\n", err)
		return nil
	}
	maxExcess, err := s.MaxExcess(expIDs)
	if err != nil {
		maxExcess = 0
	}
	if text, ok := voice.Perceive(stageBefore, stageAfter, voice.NewSplits(before, after), exps, maxExcess); ok {
		fmt.Printf("\n「%s」\n", text) // speaker separation: a blank line before Tomo's line
	}

	afterCurrent, err := s.CurrentExperiences()
	if err != nil {
		return nil
	}
	return reflection.ReperceptionCandidates(beforeCurrent, afterCurrent)
}

func cmdPerceive(args []string) error {
	fs := flag.NewFlagSet("perceive", flag.ExitOnError)
	db := dbFlag(fs)
	model := fs.String("model", ollamaModelDefault(), "ollama model for context extraction")
	url := fs.String("url", cfg.OllamaURL, "ollama base url (default http://localhost:11434)")
	fs.Parse(args)

	s, err := openStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()

	p := &perceive.Perceiver{
		Store:     s,
		Extractor: &perceive.Ollama{URL: *url, Model: *model},
		Ver:       extractorVer,
	}
	snap, snapErr := reflection.TakeSnapshot(s, time.Now().UnixMilli())
	beforeCurrent, curErr := s.CurrentExperiences()
	exps, err := p.Run()
	var extras []reflection.Candidate
	if len(exps) > 0 {
		// Apply in the same (ts, id) order the store replays on rebuild, so
		// the live projection matches the canonical rebuilt one.
		sort.Slice(exps, func(i, j int) bool {
			if exps[i].TS != exps[j].TS {
				return exps[i].TS < exps[j].TS
			}
			return exps[i].ID < exps[j].ID
		})

		now := time.Now().UnixMilli()
		before, snapErr := s.AllConnections()
		if snapErr != nil {
			return snapErr
		}
		stageBefore, snapErr := face.StageFrom(s, now)
		if snapErr != nil {
			return snapErr
		}

		en := &core.Engine{Repo: s}
		expIDs := make([]string, 0, len(exps))
		for _, e := range exps {
			if applyErr := en.Apply(e); applyErr != nil {
				return fmt.Errorf("apply %s: %w (experiences are saved; the projection is stale — run `tomobit rebuild` to repair)", e.ID, applyErr)
			}
			fmt.Printf("perceived %s: %s %s → %s\n",
				e.SessionID, e.Kind, core.NewScope(e.Tokens()...).Key(), e.Target())
			expIDs = append(expIDs, e.ID)
		}

		after, snapErr := s.AllConnections()
		if snapErr != nil {
			return snapErr
		}
		stageAfter, snapErr := face.StageFrom(s, now)
		if snapErr != nil {
			return snapErr
		}
		maxExcess, exErr := s.MaxExcess(expIDs)
		if exErr != nil {
			maxExcess = 0
		}
		// The machine log lines above stay (this is the operational command,
		// ADR-0009): the spoken line is an addition, not a replacement.
		if text, ok := voice.Perceive(stageBefore, stageAfter, voice.NewSplits(before, after), exps, maxExcess); ok {
			fmt.Printf("「%s」\n", text)
		}
		if curErr == nil {
			if afterCurrent, err := s.CurrentExperiences(); err == nil {
				extras = reflection.ReperceptionCandidates(beforeCurrent, afterCurrent)
			}
		}
	}
	if err != nil {
		return err
	}
	if len(exps) == 0 {
		fmt.Println("nothing pending")
	}
	// Re-perception is this command's specialty (a bumped extractor_ver
	// re-reads history here), so the mirror gets its boundary too.
	if snapErr == nil {
		reflectBestEffort(s, snap, extras, "")
	}
	return nil
}

func cmdRebuild(args []string) error {
	fs := flag.NewFlagSet("rebuild", flag.ExitOnError)
	db := dbFlag(fs)
	fs.Parse(args)
	s, err := openStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()
	en := &core.Engine{Repo: s}
	if err := en.Rebuild(); err != nil {
		return err
	}
	conns, err := s.AllConnections()
	if err != nil {
		return err
	}
	fmt.Printf("rebuilt: %d connections\n", len(conns))
	return nil
}

// cmdStatus is the companion view (ADR-0008 Consequences): avatar, mood, one
// spoken line, then the existing connections table. It backs both
// `tomobit status` and bare `tomobit`.
func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	db := dbFlag(fs)
	fs.Parse(args)
	s, err := openStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()

	conns, err := s.AllConnections()
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	stage, err := face.StageFrom(s, now)
	if err != nil {
		return err
	}

	// A pipe or redirect wants the machine-readable table only — no avatar,
	// no speech (ADR-0008 Decision 4: "the avatar only draws on a TTY").
	tty := isTTY(os.Stdout)
	if tty {
		printAvatar(os.Stdout, stage)
		fmt.Println() // speaker separation: a blank line before Tomo's own line
		greetIfReturned(os.Stdout, s, conns, now)
	}
	if len(conns) == 0 {
		if tty {
			fmt.Printf("Tomo · %s\n\n", face.StageName(stage))
			fmt.Printf("%s\n\n", voice.FirstMeeting())
		}
		fmt.Println("no connections yet — record a session and run `tomobit perceive`")
		return nil
	}

	en := &core.Engine{Repo: s}
	cands := make([]voice.Candidate, len(conns))
	for i, c := range conns {
		sum, err := en.LedgerSum(c, now)
		if err != nil {
			return err
		}
		cands[i] = voice.Candidate{Conn: c, State: c.State(now, sum), LedgerSum: sum}
	}

	if tty {
		states := make([]string, len(cands))
		for i, c := range cands {
			states[i] = c.State
		}
		_, marker := face.Mood(states)
		fmt.Printf("Tomo · %s %s\n\n", face.StageName(stage), marker)
		if text, ok := voice.Suggest(cands, now); ok {
			fmt.Printf("「%s」\n\n", text)
		}
	}
	return printConnections(os.Stdout, cands, now, tty)
}

// Absence knobs (ADR-0019 Decision 2: 不在と判定する空白の長さ).
const (
	absenceMs     = 72 * 3600 * 1000 // three quiet days make a return
	okaeriMinFade = 0.02             // smallest confidence drop worth naming
)

// greetIfReturned speaks the return greeting (ADR-0019 Decision 2): absence
// is the gap since the newest event, and the greeting's content is the
// lazy-decay diff across it — 忘却という正直な機構が、再会の挨拶になる.
// A tomo.greeted event closes the gap so the same return greets once.
func greetIfReturned(w io.Writer, s *store.Store, conns []*core.Connection, now int64) {
	last, err := s.LatestEventTS()
	if err != nil || last == 0 || now-last < absenceMs {
		return
	}
	var faded core.Scope
	best := okaeriMinFade
	for _, c := range conns {
		if drop := c.Confidence(last) - c.Confidence(now); drop > best {
			best = drop
			faded = c.Scope()
		}
	}
	fmt.Fprintf(w, "「%s」\n\n", voice.Okaeri(faded))
	if err := s.AppendEvent(store.NewID(now), "tomo.greeted", now,
		map[string]any{"absent_ms": now - last}); err != nil {
		fmt.Fprintln(os.Stderr, "okaeri: recording failed:", err)
	}
}

// printAvatar draws the sprite (ADR-0008 Decision 4): a short animation in
// color, or a single static frame under NO_COLOR — never cursor-control
// bytes on a terminal that was told not to expect color.
func printAvatar(w io.Writer, stage int) {
	// NO_COLOR is "present", not "non-empty" (https://no-color.org/): an
	// empty NO_COLOR= must still disable color, so this checks presence via
	// LookupEnv rather than Getenv's != "".
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		// 0 is face's first sprite frame (face.frameA is unexported).
		io.WriteString(w, face.Render(stage, 0, face.Mono))
		return
	}
	face.Animate(w, stage, face.Color256)
}

// scopeColumnWidth and targetColumnWidth cap the TTY table's SCOPE/TARGET
// columns so a token-heavy scope key (many Split children accumulate
// tokens) doesn't push STRENGTH/CONF/EVIDENCE/STATE off the visible line.
const (
	scopeColumnWidth  = 40
	targetColumnWidth = 24
)

// printConnections renders the KIND/SCOPE/.../STATE table. On a TTY, SCOPE
// and TARGET are truncated to a fixed display width; piped output (tty=false)
// stays exactly as before, since a machine reader needs the untruncated key.
func printConnections(w io.Writer, cands []voice.Candidate, now int64, tty bool) error {
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tSCOPE\tTARGET\tSTRENGTH\tCONF\tEVIDENCE\tSTATE")
	for _, x := range cands {
		c := x.Conn
		scope, target := c.ScopeKey, c.Target
		if tty {
			scope = truncate(scope, scopeColumnWidth)
			target = truncate(target, targetColumnWidth)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%.2f\t%.2f\t%.1f\t%s\n",
			c.Kind, scope, target, c.Mean(now), c.Confidence(now), c.Evidence(now), x.State)
	}
	return tw.Flush()
}

// truncate shortens s to at most max runes, replacing the cut tail with a
// single ellipsis rune. Rune-based (not byte-based) so a multi-byte
// character straddling the cut point is never split into invalid UTF-8.
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
