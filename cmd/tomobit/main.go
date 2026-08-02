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
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/Rererr/tomobit/internal/config"
	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/curiosity"
	"github.com/Rererr/tomobit/internal/decide"
	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/executor/claudecode"
	"github.com/Rererr/tomobit/internal/executor/codex"
	"github.com/Rererr/tomobit/internal/face"
	"github.com/Rererr/tomobit/internal/facelock"
	"github.com/Rererr/tomobit/internal/observe"
	"github.com/Rererr/tomobit/internal/perceive"
	"github.com/Rererr/tomobit/internal/plan"
	"github.com/Rererr/tomobit/internal/reflection"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/subtask"
	"github.com/Rererr/tomobit/internal/voice"
	"github.com/Rererr/tomobit/internal/workspace"
	"golang.org/x/term"
)

const extractorVer = 4 // bump when the extraction prompt/schema changes

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

	// Copied, not aliased: cfg.ClaudeArgs must survive whatever the APPEND
	// step below does to this slice (setup can re-save cfg from the same
	// backing array a stale append would have clobbered).
	var resolved []string
	if v, ok := os.LookupEnv("TOMOBIT_CLAUDE_ARGS"); ok {
		resolved = splitArgs(v)
	} else {
		resolved = append([]string(nil), cfg.ClaudeArgs...)
	}
	if v := os.Getenv("TOMOBIT_CLAUDE_ARGS_APPEND"); v != "" {
		resolved = append(resolved, splitArgs(v)...)
	}
	claudeAdapter.ExtraArgs = resolved
}

// splitArgs divides s into argv the way TOMOBIT_CLAUDE_ARGS(_APPEND) needs:
// whitespace-separated tokens, with "..." and '...' preserving embedded
// whitespace — even mid-token, e.g. ab"c d"e -> "abc de" — and a backslash
// outside single quotes escaping the following rune. Backslashes inside
// single quotes are literal. Input with no quotes or backslashes splits
// exactly like strings.Fields, so a plain TOMOBIT_CLAUDE_ARGS keeps working
// unchanged. An unterminated quote is not treated as an error — GUI-supplied
// strings must never abort a launch — it just pulls the rest of s into the
// final token and logs one warning so the truncation isn't silent.
func splitArgs(s string) []string {
	var args []string
	var buf strings.Builder
	inToken := false
	var quote rune // 0, '\'', or '"'

	flush := func() {
		if inToken {
			args = append(args, buf.String())
			buf.Reset()
			inToken = false
		}
	}

	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				buf.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && i+1 < len(runes):
				i++
				buf.WriteRune(runes[i])
			default:
				buf.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inToken = true
		case r == '\\' && i+1 < len(runes):
			i++
			buf.WriteRune(runes[i])
			inToken = true
		case unicode.IsSpace(r):
			flush()
		default:
			buf.WriteRune(r)
			inToken = true
		}
	}
	if quote != 0 {
		fmt.Fprintln(os.Stderr, "warning: unterminated quote in argument string; treating the remainder as one argument")
	}
	flush()
	return args
}

func providerNames() []string {
	names := make([]string, 0, len(providers))
	for n := range providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// providerFlag registers --provider with the one default do and chat share
// (ADR-0043 Decision 1): unset means auto — the Decision Engine, not a fixed
// name, chooses. One definition, not two string literals, so the commands can
// never drift on who decides when the user says nothing.
func providerFlag(fs *flag.FlagSet) *string {
	return fs.String("provider", "auto",
		"adapter to run: claude-code|codex|human|auto (auto: the ledger decides — ADR-0043)")
}

// onPathCache memoizes executableOnPath's verdicts for the process lifetime:
// PATH is inherited once at startup and never changes mid-run, so the first
// LookPath answers for every later decision (a long chat decides once per
// task). The memo is per executable, deliberately not a snapshot of the
// candidate list — the providers map is mutable (tests register fakes), and a
// frozen list would go stale under it.
var onPathCache sync.Map // executable name → bool

func executableOnPath(name string) bool {
	if v, ok := onPathCache.Load(name); ok {
		return v.(bool)
	}
	_, err := exec.LookPath(name)
	found := err == nil
	onPathCache.Store(name, found)
	return found
}

// availableProviderNames narrows providerNames() to the adapters whose CLI can
// actually launch (ADR-0043 Decision 2): auto must never draw a lot on a
// binary that is not there — that failure says nothing about the provider,
// only about this machine, and recording it would sink a capability the
// provider never got to show. The registry itself stays static:
// resolveProvider keeps answering `--provider codex` with the honest "codex
// won't start", never "no such provider".
func availableProviderNames() []string {
	return runnableNames(providerNames(), executableOnPath)
}

// runnableNames is availableProviderNames' testable core — onPath is injected
// so the filter pins against a fake PATH instead of whatever this machine has
// installed.
func runnableNames(names []string, onPath func(string) bool) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if launchableProvider(n, onPath) {
			out = append(out, n)
		}
	}
	return out
}

// launchableProvider is the one "this provider can actually run" predicate
// (ADR-0043 Decision 2): registered adapter AND its executable on PATH. Every
// path that will really launch a provider — auto's candidate list above, the
// duel's pair (pickDuelGap) — must share it, or a machine without the binary
// gets offered a run that can only fail. Registration alone answers a
// different question: resolveProvider keeps telling an explicit
// `--provider codex` the honest "codex won't start", never "no such provider".
func launchableProvider(name string, onPath func(string) bool) bool {
	a, ok := providers[name]
	if !ok {
		return false
	}
	exe, _, _ := a.Command(executor.Request{})
	return onPath(exe)
}

// resolveProvider looks up a registered Adapter by name, or reports the
// available names so an unknown --provider fails with something actionable.
func resolveProvider(name string) (executor.Adapter, error) {
	if a, ok := providers[name]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("unknown provider %q (available: %s, human, auto)",
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
		return cmdHome(nil)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "do":
		return cmdDo(rest)
	case "chat":
		return cmdChat(rest)
	case "record":
		return cmdRecord(rest)
	case "perceive":
		return cmdPerceive(rest)
	case "rebuild":
		return cmdRebuild(rest)
	case "forget":
		return cmdForget(rest)
	case "amend":
		return cmdAmend(rest)
	case "verdict":
		return cmdVerdict(rest)
	case "status":
		return cmdStatus(rest)
	case "setup":
		return cmdSetup(rest)
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		if strings.HasPrefix(cmd, "-") {
			// A bare `tomobit --db X` names no subcommand — route the flags
			// home instead of failing as "unknown command".
			return cmdHome(args)
		}
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// cmdHome is what bare `tomobit` does. ADR-0008 made the first screen the
// companion view rather than the manual; ADR-0022 Decision 4 makes its next
// line the prompt — you meet Tomo and you can just talk. Piped or redirected
// there is nobody to talk to, so it stays the view it has always been.
func cmdHome(args []string) error {
	if isTTY(os.Stdin) && isTTY(os.Stdout) {
		return cmdChat(args)
	}
	return cmdStatus(args)
}

func usage() {
	fmt.Println(`tomobit — a living harness that grows with you

usage:
  tomobit          (no args) 相棒ビュー(ステージ・気分・一言・Connection)を出して
                   そのまま対話に入る。対話起動時は顔窓(tomobit-face)も開く
                   (config face_auto_launch / env TOMOBIT_FACE=0 で止める)。
                   姿は窓、声とテキストは端末。パイプ・リダイレクトなら見せて終わる
  tomobit chat     [--cap implement] [--provider claude-code|codex|human|auto]
                   [--permission-mode <mode>] [--timeout 0] [--size ...]
                   [--backend ollama|mlx-lm] [--model <name>] [--url <addr>]
                   [--view ndjson] ["<prompt>"]
                   対話セッション(ADR-0022)。1つの会話 = 1つのタスク = 1つの経験。
                   ターンは同じスレッドを継ぐ。/new か /exit で区切ると
                   Feedback → 知覚 → Tomoの質問 が走る。/help でコマンド一覧
                   --backend/--model/--url は知覚(best-effort)の配線 — do と同じ解決順
                   --view ndjson は stdout 全体を機械可読な view ストリームにする
                   (ADR-0032、GUI向けオプトイン。TTYには出せない)
  tomobit do       [--cap implement] [--timeout 0] [--permission-mode <mode>]
                   [--provider claude-code|codex|human|auto]
                   [--plan auto|full|direct|quick|<steps>] [--size small|medium|large]
                   [--backend ollama|mlx-lm] [--model <name>] [--url <addr>] "<prompt>"
                   --provider の既定は auto: 決定エンジン(ADR-0012)が能力ゲート+TSで
                   選ぶ。候補は起動できるProviderのみ・humanは文脈を知っている時
                   だけ候補 (ADR-0043)。--provider human: 自分でやって
                   同じ台帳に乗せる。--plan auto: 手順も台帳が選ぶ(ADR-0014、
                   例 analyze>implement>test)。--size は判断の温度 n(stakes)。
                   --backend/--model/--url は知覚(best-effort)の配線 — 既定は
                   config、更にその既定はconfig未配線ならOSごとに解決(ADR-0029)。
                   providerが「大きすぎる/独立に分けられる」と判断すれば分割提案を
                   受けてサブタスクを実行(ADR-0023/0028、常時ON。--plan・humanでは
                   付けない。config split_protocol=false で止める)。独立群を宣言されたら
                   実行直前に y/N で並走可否を聞く(既定N=全逐次。概算コストを実測
                   中央値から提示。並走ストリームは[n:provider]表示。非TTYは常に逐次)
  tomobit record   --session <id> --type <event.type> [--json '{...}']
  tomobit perceive [--backend ollama|mlx-lm] [--model <name>] [--url <addr>]
  tomobit rebuild
  tomobit forget   --id <exp-id> [--id ...] | --session <session-id> [--yes]
                   忘却の器官(ADR-0033)。経験を物理削除(--id)、または生ログごと
                   セッションを完全削除(--session)。削除後は同一コマンド内で
                   自動rebuild + vacuum。不可逆なのでTTYはy/N、非対話は--yes必須。
                   --session は子セッションを列挙するが消すのは指名分のみ
  tomobit amend    --id <exp-id> [--context '<json>'] [--outcome '<json>'] [--provider <name>]
                   経験の訂正(ADR-0033)。削除ではなく人間による再知覚として追記
                   (現行世代のみ・過去世代は不可)。context/outcome/providerを
                   全置換。key閉集合・provider登録名+humanに限定。自動rebuild
  tomobit verdict  <session-id> up|down|clear
                   第2層の判定(ADR-0003/0055)。閉じたタスクへの👍/👎で、
                   赤テストを含む他の全層を上書きする。clearで取り消せる。
                   user.verdictを追記し、知覚済みなら経験を1行繰り上げて自動rebuild。
                   中断・未終了・分割の子・amend済みのセッションは断る
  tomobit status   [--view human|json] same as no args
                   --view json は顔窓を起動せず、TTY装飾も挨拶記帳もせず
                   {type,exists,stage,stage_name,mood,speak,providers} を1行書いて終わる
                   (ADR-0039、GUIヘッダ向けオプトイン。providersはProvider別の利用実績)
  tomobit setup    対話式でこの機械の配線を決める(claude profile / 知覚バックエンド / 顔窓)。
                   再実行すれば診断を兼ねる。書き先は ~/.tomobit/config.json

companion markers: "?" = a connection is questioned / "z" = dormant (long quiet)

common flags:
  --db <path>   database file (default ~/.tomobit/tomobit.db, or $TOMOBIT_DB)

config precedence: flag > env > ~/.tomobit/config.json
  env overrides: TOMOBIT_DB, TOMOBIT_CLAUDE_CONFIG_DIR, TOMOBIT_CLAUDE_ARGS
                 TOMOBIT_CLAUDE_ARGS_APPEND (解決済み引数への追記・引用符可)
                 TOMOBIT_FACE=0|1 (顔窓の自動起動を止める / 強制する)`)
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

// Hardcoded perception defaults (ADR-0029 Decision 3): the floor under
// config when a backend's url/model was never wired at all.
const (
	defaultOllamaURL   = "http://localhost:11434"
	defaultOllamaModel = "qwen3:8b" // ADR-0005 measured pick
	defaultMLXURL      = "http://localhost:8080"
	defaultMLXModel    = "mlx-community/Qwen3-8B-4bit" // ADR-0029 Decision 3
)

// newExtractor is the one place do/chat/perceive/duel build a perception
// Extractor (ADR-0029 Decision 1/4), so the backend switch lives once instead
// of repeating &perceive.Ollama{...} at every call site. backend/url/model are
// the raw --backend/--url/--model flag values (possibly "", since a flag's
// default cannot depend on a backend not yet known at flag-definition time);
// each empty field falls through url/model > config's key for the resolved
// backend > the hardcoded default above.
func newExtractor(backend, url, model string) (perceive.Extractor, error) {
	b := backend
	if b == "" {
		var err error
		if b, err = cfg.ResolveBackend(runtime.GOOS); err != nil {
			return nil, err
		}
	}
	switch b {
	case "ollama":
		if url == "" {
			url = firstNonEmpty(cfg.OllamaURL, defaultOllamaURL)
		}
		if model == "" {
			model = firstNonEmpty(cfg.OllamaModel, defaultOllamaModel)
		}
		return &perceive.Ollama{URL: url, Model: model}, nil
	case "mlx-lm":
		if url == "" {
			url = firstNonEmpty(cfg.MLXURL, defaultMLXURL)
		}
		if model == "" {
			model = firstNonEmpty(cfg.MLXModel, defaultMLXModel)
		}
		return &perceive.MLXLM{URL: url, Model: model}, nil
	default:
		return nil, fmt.Errorf("unknown --backend %q (ollama, mlx-lm)", b)
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func openStore(path string) (*store.Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return store.Open(path)
}

// isTTY reports whether f is an interactive terminal. Shared by the Curiosity
// question (stdin, ADR-0007), the companion-view avatar (stdout, ADR-0008
// Decision 4) and the chat (ADR-0022), so every organ honors the same
// detection — the same one the line editor uses to choose raw mode.
//
// It asks the terminal driver (a termios ioctl) rather than trusting the file
// mode: /dev/null is a character device too, so `tomobit do ... < /dev/null`
// would otherwise look like a human sitting at a keyboard, and Tomo would
// spend its one question a day on nobody.
func isTTY(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

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
	permMode := fs.String("permission-mode", "", "how much a Provider may do without asking: auto|strict|open (ADR-0053)")
	providerName := providerFlag(fs)
	planArg := fs.String("plan", "", "plan: auto, a label (full|direct|quick), or steps like analyze>implement>test")
	size := fs.String("size", "", "task size for decision stakes: small|medium|large (--provider auto)")
	backend := fs.String("backend", "", "perception backend for best-effort perception: ollama|mlx-lm (default: resolved from config)")
	model := fs.String("model", "", "perception model for best-effort perception (default depends on --backend)")
	url := fs.String("url", "", "perception backend url for best-effort perception (default depends on --backend)")
	fs.Parse(args)

	// Whether --provider was set explicitly, so the duel offer can tell an
	// intentional pin from the default (ADR-0026 Decision 2).
	providerExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "provider" {
			providerExplicit = true
		}
	})

	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		return fmt.Errorf("do: a prompt is required")
	}
	// A named provider fails fast, before the store is even opened.
	if *providerName != "auto" && *providerName != "human" {
		if _, err := resolveProvider(*providerName); err != nil {
			return err
		}
	}
	// After validation, like chat: a `do` that fails its args (a bad provider)
	// must not leave a detached window behind.
	// Take presence before spawning the face (ADR-0027 Decision 2/3): the run is
	// live from the moment the window opens until it finishes. split/duel run as
	// children of this `do`, so the parent's single presence covers them all.
	releasePresence := registerPresence(os.Stderr)
	defer releasePresence()
	maybeLaunchFace(*db)
	stdin := bufio.NewReader(os.Stdin)
	if err := ensureClaudeProfile(stdin, *providerName); err != nil {
		return err
	}

	s, err := openStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()

	// Built once and reused for the rest of the run — the pre-run Task
	// Perception holder below and the post-run perceiveBestEffort/runSplit
	// both need an Extractor, and there is no reason for the backend/model/
	// url resolution to happen twice in one invocation.
	extractor, err := newExtractor(*backend, *url, *model)
	if err != nil {
		return err
	}
	// One lazy Task Perception holder for the whole task (ADR-0036 Decision
	// 2b): whichever of duelOffer / autoDecide / ChoosePlan asks first
	// perceives; every split child below is handed this same holder rather
	// than building its own. A run nobody asks (--provider claude-code
	// --plan direct, duel unfired) never perceives at all.
	tp := newTaskPerception(prompt, taskExtractFuncFor(s, extractor))

	// 権限の語は中立3語だけ (ADR-0053 Decision 1): 生のCLI語を通すと、tomobit が
	// 片方のCLIの語彙を持つことになる。duel も split も同じ値を受け取る。
	perm, err := executor.ParsePermission(*permMode)
	if err != nil {
		return err
	}

	// Before committing to one provider, Tomo may offer an A/B experiment
	// (ADR-0026): if an open Preference Gap covers this capability, it asks to
	// run both providers and settle the preference by real work. Y takes the
	// duel path and returns; anything else falls through to the normal run.
	if duelEligible(providerExplicit, *providerName) {
		now := time.Now().UnixMilli()
		if gap, accepted := duelOffer(s, *capability, *size, stdin, os.Stdout,
			isTTY(os.Stdin) && isTTY(os.Stdout), now, tp); accepted {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			return runDuel(ctx, s, gap, prompt, *capability, *size, perm, *timeout,
				stdin, os.Stdout, extractor)
		}
	}

	sid, adapter, human, err := openTask(s, os.Stdout, *providerName, *capability, *size, prompt, tp)
	if err != nil {
		return err
	}

	// Plan selection (ADR-0014). The human path skips it: a human run has no
	// step boundary the harness could drive.
	planName := ""
	if !human {
		if planName, err = resolvePlan(s, sid, *planArg, *capability, *size, tp); err != nil {
			return err
		}
	}

	// Whether the split protocol rides this do target run (ADR-0028 Decision
	// 1). Always-on now — whether to split is the Provider's call on every
	// task, not a flag the user must remember. A subtask's or duel's own run
	// never gets it either, but those frame their prompts elsewhere (runSplit /
	// runDuel), so depth stays 1 (ADR-0023 Decision 4).
	splitProtocol := splitProtocolEligible(splitProtocolEnabled(), human, planName)
	// The isolation protocol has one exclusion the split protocol also has —
	// human, which returns no stream to declare in — but rides plan runs too:
	// a plan's steps all work in the same tree, so isolating it once at the
	// start is exactly as meaningful as isolating a plain run (ADR-0050
	// Decision 5 excludes only --provider human).
	isolateProtocol := isolateProtocolEnabled() && !human

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var result executor.Result
	var runErr error
	var texts []string
	if human {
		result, runErr = runHuman(s, os.Stdout, sid, stdin)
		if runErr != nil {
			return runErr
		}
	} else {
		runPrompt := prompt
		var collect *[]string
		if splitProtocol {
			runPrompt = subtask.Instruction(runPrompt)
			collect = &texts
		}
		// The isolation protocol rides the same run (ADR-0050 Decision 5). It
		// is appended after the split protocol so that a Provider deciding to
		// split — which means doing no work at all (ADR-0023 Decision 1) — has
		// already read that instruction; there is no workspace to isolate for a
		// run that only proposes, and Parse simply finds no declaration.
		var isoParent, isoChild string
		if isolateProtocol {
			p, c, err := isolationDir(sid)
			if err != nil {
				// A place that cannot be prepared is not a reason to refuse the
				// work: the run goes ahead unisolated, loudly.
				fmt.Fprintln(os.Stderr, "workspace: 隔離先を用意できないので隔離せずに走る:", err)
			} else {
				isoParent, isoChild = p, c
				runPrompt = workspace.Instruction(runPrompt, isoChild)
				collect = &texts
			}
		}
		sink := providerSink(s, sid, os.Stdout, collect)

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
			req := executor.Request{
				Prompt: stepPrompt(runPrompt, step, i, len(steps)), PermissionMode: perm, Timeout: *timeout,
			}
			if isoParent != "" {
				// The scope has to name a directory that exists, so AddDirs
				// gets the parent while the prompt names the leaf the Provider
				// will create (ADR-0047 wiring + ADR-0050 Decision 4).
				req.AddDirs = append(req.AddDirs, isoParent)
			}
			result, runErr = ex.Run(ctx, req, sink)
			if ctx.Err() != nil || runErr != nil || result.ExitCode != 0 {
				break
			}
		}
	}

	// SIGINT: the child was already signalled; record the cancellation and
	// stop, skipping the Feedback question. The session stays pending, so
	// `tomobit perceive` can still learn from it later.
	if ctx.Err() != nil {
		return s.AppendEvent(sid, "task.cancelled", time.Now().UnixMilli(), nil)
	}

	// The provider never started (missing binary, spawn failure): fail the
	// command itself instead of closing the task as if work had happened
	// (ADR-0043 Decision 3 — the old path recorded provider.error and returned
	// nil, so the user saw a clean exit and the ledger saw a provider failure
	// that was really this machine's). No task.finished either — and not
	// task.cancelled: a cancelled boundary still enters PendingSessions and is
	// perceived into an experience row, while a launch that never happened has
	// nothing for perception to read. The unclosed session is the honest shape.
	if runErr != nil && !result.Started {
		return fmt.Errorf("do: provider never started: %w", runErr)
	}

	if payload, need := providerErrorPayload(runErr, result); need {
		if err := s.AppendEvent(sid, "provider.error", time.Now().UnixMilli(), payload); err != nil {
			return err
		}
	}

	// Where the work went is recorded before anything downstream reads it: the
	// first-layer observation (ADR-0052) follows this path when it is present,
	// so the ledger must know it by the time finishTask runs.
	var decl *workspace.Declaration
	if result.Started {
		decl = recordWorkspace(s, sid, texts)
	}

	// A clean run's output is read for a split proposal (ADR-0023 Decision
	// 1): a broken run (runErr, non-zero exit, already excluded by ctx.Err()
	// above) never reaches here, so its output is never trusted as one. The
	// "\n" join assumes adapters emit message-level text (both current ones
	// do): a token-delta adapter could split the marker key across events,
	// and the joined text would no longer contain it.
	if splitProtocol && runErr == nil && result.ExitCode == 0 {
		if groups := readSplitProposal(texts); groups != nil {
			// The children inherit this run's decision and its place, both
			// already settled above (ADR-0054 Decisions 1 and 3). do has no /cd
			// of its own, so the parent's place is this process's cwd unless the
			// isolation declaration moved it. The isolation *parent* directory
			// is deliberately not passed on: it was scope for creating the leaf,
			// and a child that already works inside the leaf needs nothing
			// outside it (ADR-0050 Decision 4).
			w := subtaskWiring{
				adapter: adapter, human: human, capability: *capability,
				permMode: perm, timeout: *timeout,
				workDir: subtaskWorkDir(decl, ""),
			}
			return runSplit(ctx, s, sid, groups, prompt, w,
				stdin, os.Stdout, isTTY(os.Stdin) && isTTY(os.Stdout), extractor)
		}
	}

	// do has no declaration mouth for humanPresent (ADR-0035 Decision 2 — that
	// argument was rejected as YAGNI): it hands finishTask the same
	// isTTY(os.Stdin) the function used to read for itself, so behavior here is
	// unchanged.
	return finishTask(s, sid, stdin, os.Stdout, result.Started, isTTY(os.Stdin), extractor, "")
}

// providerSink builds the Executor Sink shared by a plain `do` run and each
// ADR-0023 subtask run: assistant text is echoed to out so the user (or
// subtask.Parse, ADR-0028) can read it, and every event lands in sid in
// stream order. collect, when non-nil, also gathers the echoed text — the
// concatenation a split-protocol run hands to subtask.Parse.
func providerSink(s *store.Store, sid string, out io.Writer, collect *[]string) executor.Sink {
	return func(ev executor.Event, ts int64) error {
		if ev.Type == executor.EventProviderOutput {
			if text, ok := ev.Payload["text"].(string); ok && text != "" {
				fmt.Fprintln(out, text)
				if collect != nil {
					*collect = append(*collect, text)
				}
			}
		}
		// View-only keys (tool detail, tool output) stay out of the ledger
		// (ADR-0024 Decision 6, ADR-0030) — recordEvent strips them, and every
		// sink routes through it so `do` and chat record the same shape.
		return recordEvent(s, sid, ev, ts)
	}
}

// recordEvent strips an event's view-only keys and appends it to the ledger,
// but skips an event that carries nothing but display (ADR-0030): a tool_result
// is view-only, so after the strip its payload is empty, and recording that
// empty provider.output would spend the perception digest budget on a
// zero-information row — the very cost Decision 1 refused. A tool_use survives
// the strip with its {"tool": name}, so only the tool_result-only events fall
// out here. Shared by every sink (do, chat, split, duel) so the skip rule
// cannot drift between them.
func recordEvent(s *store.Store, sid string, ev executor.Event, ts int64) error {
	// 許可は配線であって経験ではない (ADR-0053 Decision 4)。「何を許したか」は
	// どう走らせるかの話で、何が起きたかではない — ADR-0047 が働く場所について
	// 引いた線がそのまま当たる。人に見せるだけで、台帳には残さない。
	if ev.Type == executor.EventPermissionRequired {
		return nil
	}
	payload := executor.StripViewOnly(ev.Payload)
	if len(payload) == 0 && len(ev.Payload) > 0 {
		return nil // purely view-only: shown to the human, not recorded
	}
	return s.AppendEvent(sid, ev.Type, ts, payload)
}

// openSubtask opens one split subtask's own task session (ADR-0023 Decision
// 2): the same task.started/capability.started shape openTask writes for a
// top-level do, plus the parent link. A small helper of its own rather than
// a reuse of openTask — that one resolves --provider human by itself and
// returns nothing to link a parent, both wrong for a subtask.
//
// **It resolves no Provider.** Whoever opens a child already knows which
// Provider it runs on: a split's children all run on the one the parent's task
// boundary chose (ADR-0054 Decision 1 — one task, one decision, so one
// tomo.decided), and a duel names both sides itself (ADR-0026). Deciding again
// here used to call autoDecide per child on the *parent's* perception tokens,
// which drew the same lottery K times and then filed each result under the
// child's own re-perceived scope — the arm pulled and the arm updated were
// different ones.
func openSubtask(s *store.Store, capability, sub, parentSID string) (subSID string, err error) {
	now := time.Now().UnixMilli()
	subSID = store.NewID(now)
	if err = s.AppendEvent(subSID, "task.started", now,
		map[string]any{"intent": sub, "source": "production", "parent": parentSID}); err != nil {
		return "", err
	}
	if err = s.AppendEvent(subSID, "capability.started", now,
		map[string]any{"capability": capability}); err != nil {
		return "", err
	}
	return subSID, nil
}

// runSplit executes an accepted split proposal (ADR-0023, groups and
// parallelism since ADR-0028). The proposal arrives as groups a Provider
// declared independent; a wide group (Decision 2) triggers the one permission
// gate (Decision 3): a yes runs each wide group's members in parallel (groups
// still sequential between themselves, fail-stopping the next group), a no — or
// a non-interactive run — flattens the whole thing back to the ADR-0023
// sequential order. Either way each subtask becomes its own task session — same
// ledger, same rehabilitation as any other task.
//
// Subtasks carry no Feedback and, since ADR-0054 Decision 2, no experience at
// all: a split is one task's breakdown, so the task's one experience is the
// parent's. Their events stay in the ledger; only the projection skips them.
// judged=false on the closing finishTask: the parent's own artifact was the
// split proposal itself, not something to grade.
func runSplit(ctx context.Context, s *store.Store, parentSID string, groups [][]string,
	parentIntent string, w subtaskWiring,
	in *bufio.Reader, out io.Writer, interactive bool, extractor perceive.Extractor) error {
	_, cancelled, err := executeSplit(ctx, s, parentSID, groups, parentIntent, w, in, out, interactive, nil)
	if err != nil {
		return err
	}
	if cancelled {
		return nil // children and parent already hold task.cancelled; skip finishTask
	}
	// runSplit is do-only (chat's own split path, splitAndFold, folds back into
	// the conversation instead of calling finishTask) — the same isTTY(os.Stdin)
	// cmdDo's own call passes (ADR-0035 Decision 2).
	return finishTask(s, parentSID, in, out, false, isTTY(os.Stdin), extractor, "")
}

// flattenGroups turns the Provider's declared group structure into the flat
// proposal-order execution list Phase 1 runs, plus the index groups recorded
// in task.split's payload (groups [["a"],["b","c"]] → subs [a,b,c], idxGroups
// [[0],[1,2]] — SCHEMA.md R4). The index form keeps the flat execution order
// and the independence declaration both auditable from one event.
func flattenGroups(groups [][]string) (subs []string, idxGroups [][]int) {
	idxGroups = make([][]int, 0, len(groups))
	for _, g := range groups {
		idx := make([]int, 0, len(g))
		for _, sub := range g {
			idx = append(idx, len(subs))
			subs = append(subs, sub)
		}
		idxGroups = append(idxGroups, idx)
	}
	return subs, idxGroups
}

// splitProtocolEligible reports whether the split protocol rides a do target
// run (ADR-0028 Decision 1): the kill switch must be on, and the run must be
// neither a plan step's output (not unambiguously "the proposal") nor a human
// run (no provider stream to read one from). Pure — enabled is injected — so the
// cases pin without a real run or a config file, symmetric with duelEligible.
func splitProtocolEligible(enabled, human bool, planName string) bool {
	return enabled && !human && planName == ""
}

// splitProtocolEnabled resolves the ADR-0028 kill switch (config split_protocol,
// default true). The pointer distinguishes an absent key (nil = default on, so a
// config predating the key is never silently downgraded) from an explicit false
// (the opt-out that stops the always-on protocol — Decision 1).
func splitProtocolEnabled() bool {
	if cfg.SplitProtocol == nil {
		return true
	}
	return *cfg.SplitProtocol
}

// isolateProtocolEnabled resolves the ADR-0050 kill switch (config
// isolate_protocol, default true), shaped exactly like splitProtocolEnabled:
// an absent key must never read as "disabled" and silently downgrade a machine
// that predates it.
func isolateProtocolEnabled() bool {
	if cfg.IsolateProtocol == nil {
		return true
	}
	return *cfg.IsolateProtocol
}

// parallelSubtasksEnabled resolves the ADR-0056 kill switch (config
// parallel_subtasks, default true), shaped like the two above. An explicit
// false is the 「信じない」 position — every split runs sequentially — and
// needs no other change: the declaration is still recorded in task.split's
// groups, so the evidence for reopening the decision keeps accumulating.
func parallelSubtasksEnabled() bool {
	if cfg.ParallelSubtasks == nil {
		return true
	}
	return *cfg.ParallelSubtasks
}

// isolationDir prepares the place this session's Provider is told to build its
// workspace in, returning the parent to hand to AddDirs and the child path to
// name in the instruction (ADR-0050 Decision 4).
//
// Only the parent is created. `git worktree add` (and its equivalents) want to
// make the leaf themselves — pre-creating it would hand every Provider a
// "directory already exists" to work around on the very first step. The parent
// must exist because that is what goes into AddDirs, and a permission scope
// naming a directory that isn't there is not a scope.
//
// Measured 2026-07-26: this exact split — AddDirs on the parent, the child
// named in the prompt — is what claude followed on the first try.
func isolationDir(sid string) (parent, child string, err error) {
	child, err = workspace.Dir(sid)
	if err != nil {
		return "", "", err
	}
	parent = filepath.Dir(child)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", "", err
	}
	return parent, child, nil
}

// recordWorkspace reads the isolation declaration out of a run's output and
// files it (ADR-0050 Decision 2). Absent is the ordinary case and says nothing;
// malformed warns and continues, exactly as a broken split proposal does.
//
// Unlike a split proposal, this is read even when the run failed. A proposal is
// an instruction to act on, so ADR-0023 refuses to trust a broken run's version
// of one; a declaration is a report of *where the work went*, and a run that
// failed halfway still moved its work somewhere. Telling the human their
// results are missing would be worse than telling them where to look.
// It returns what was declared (nil when nothing was, or when it was broken)
// so the caller can hand a split's children the same place to work in
// (ADR-0054 Decision 3) without re-reading the ledger.
func recordWorkspace(s *store.Store, sid string, texts []string) *workspace.Declaration {
	decl, err := workspace.Parse(strings.Join(texts, "\n"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "workspace:", err, "— 宣言は記帳しない")
		return nil
	}
	if decl == nil {
		return nil
	}
	if err := s.AppendEvent(sid, "task.workspace", time.Now().UnixMilli(), decl.Payload()); err != nil {
		fmt.Fprintln(os.Stderr, "workspace: 記帳に失敗:", err)
	}
	return decl
}

// subtaskWorkDir is where a split's children work (ADR-0054 Decision 3): the
// isolated workspace the parent declared, or — when there is none — the parent's
// own working place, unchanged (ADR-0047). A split is one task's breakdown, so
// its children never work somewhere the parent is not.
//
// The declared path is checked for being a directory, and only that. Whether it
// really is a worktree stays unverified and unverifiable (ADR-0050 Decision 2);
// this is the same check /cd already runs before accepting a place, and it is
// here because a broken declaration should not turn every child into a chdir
// failure. Falling back is loud — silently working in the original repository is
// exactly the bug this Decision closes, so it must never happen quietly.
func subtaskWorkDir(decl *workspace.Declaration, parentDir string) string {
	if decl == nil || !decl.Isolated || decl.Path == "" {
		return parentDir
	}
	if fi, err := os.Stat(decl.Path); err != nil || !fi.IsDir() {
		fmt.Fprintf(os.Stderr,
			"workspace: 宣言された隔離先 %q が見つからないので、サブタスクは親の作業場所で走る\n", decl.Path)
		return parentDir
	}
	return decl.Path
}

// declaresGroups reports whether the proposal declared any independent group —
// a group wider than one subtask (ADR-0028 Decision 2). A flat proposal (every
// element a lone subtask) declares none, and its task.split omits groups.
func declaresGroups(groups [][]string) bool {
	for _, g := range groups {
		if len(g) > 1 {
			return true
		}
	}
	return false
}

// finishTask is the tail every task boundary shares (ADR-0022 Decision 1):
// Feedback → best-effort知覚 → Tomoの質問 → 鏡. `do` reaches it when its one
// run ends; a chat session reaches it at /new, /exit or Ctrl-D. The boundary
// moved, the organs did not.
//
// judged says the session produced something a human can judge — a run that
// never started (e.g. the claude binary is missing) produced nothing, so the
// Feedback question is skipped and the outcome carries no signal.
//
// humanPresent gates the boundary organs proper — Tomo's question and the
// mirror (ADR-0035 Decision 2) — and arrives as an argument rather than a
// direct isTTY(os.Stdin) read here: a chat holds context (its own --view
// ndjson) that this function cannot see, so the caller resolves the
// predicate and hands it in.
// workDir is where this task worked (a chat's /cd, ADR-0047; "" for do, which
// means the process's own cwd). It reaches here only to find the first-layer
// command wired for that place (ADR-0052) — the boundary organs themselves are
// place-blind.
func finishTask(s *store.Store, sid string, in *bufio.Reader, out io.Writer, judged, humanPresent bool, extractor perceive.Extractor, workDir string) error {
	// The first layer of Outcome, observed before the human is asked (ADR-0052
	// Decision 5): a red suite is a fact the person grading deserves to have,
	// and ADR-0026 Decision 3 already allowed the signal as 補助表示 so long as
	// the decision stays theirs.
	observeFirstLayer(s, sid, out, workDir)

	// The Feedback question runs at the end of every completed task (ADR-0006
	// Decision 4): exit 0 is not a verdict, and even a failed run may have
	// produced something the user keeps.
	payload := map[string]any{}
	if judged {
		payload = feedbackPayload(s, sid, in, out)
	}
	if err := s.AppendEvent(sid, "task.finished", time.Now().UnixMilli(), payload); err != nil {
		return err
	}
	// 分け方の評価 (ADR-0051). Runs right after Feedback because it reads that
	// answer: a session graded 文句なし needs no second question, so the extra
	// friction lands only on the days it earns.
	recordSplitVerdict(s, sid, in, out, judged, payload)

	// 第2層 (ADR-0055 Decision 1). Same shape and the same reason as the line
	// above — it reads the Feedback answer and only fires when it contradicts
	// the observation, so the question lands on the one day it means something.
	// It runs before perception so the verdict is read on the first pass.
	if judged {
		askVerdictOnContradiction(s, sid, in, out, humanPresent, payload)
	}

	// The reflection snapshot is taken before perception so that whatever
	// the coming Apply batch discovers (Split, 逆転, Questioned, 名誉回復)
	// shows up as a before/after difference (ADR-0015 Decision 2).
	snap, snapErr := reflection.TakeSnapshot(s, time.Now().UnixMilli())
	if snapErr != nil {
		fmt.Fprintln(os.Stderr, "reflection: snapshot failed:", snapErr)
	}

	extras := perceiveBestEffort(s, out, extractor)

	// ADR-0007 lists the question right after the adoption prompt, but it runs
	// here — after perception — so today's work is already folded into the Gap
	// derivation. The interruption is still once per boundary, so the position
	// ADR-0007 protects is unchanged.
	askWithIO(s, sid, in, out, humanPresent, time.Now().UnixMilli())

	if snapErr == nil {
		reflectWithIO(s, snap, extras, sid, in, out, humanPresent, time.Now().UnixMilli())
	}
	return nil
}

// splitOf reports whether this session proposed a split, and which Provider
// did the proposing — the one whose 分け方 is about to be judged.
func splitOf(s *store.Store, sid string) (provider string, split bool) {
	events, err := s.EventsBySession(sid)
	if err != nil {
		return "", false
	}
	for _, e := range events {
		switch e.Type {
		case "task.split":
			split = true
		case "provider.selected":
			if p, ok := e.Payload["provider"].(string); ok {
				provider = p
			}
		}
	}
	return provider, split
}

// recordSplitVerdict files how the 分け方 was judged (ADR-0051).
//
// Splitting is the one thing a Provider does that no layer of the ledger has
// been able to see: the children's outcomes belong to the children, and the
// parent's own Feedback — where it has one — mixes 采配, 統合 and the whole
// conversation into a single grade. This separates the one strand out.
//
// The verdict comes from one of two places, never both:
//
//	相乗り    a session graded 文句なし says the split was fine too. That is not
//	          a guess: the human saw the whole session, including how it was cut
//	          up, and called it good. ADR-0028 Decision 5 already treats this
//	          grade as covering 「分割という采配 + 統合 + 会話全体」; recording it
//	          separately only splits out a strand that was always in there.
//	追加の問い a session that went less than well gets one more question, because
//	          that is the case where "was it the split's fault?" has an answer
//	          worth having. `do` has no Feedback to ride on (ADR-0023 Decision 5),
//	          so it asks its own.
//
// A session that split is always recorded, even with no signal: without a row,
// 「聞かなかった」 and 「答えなかった」 would look identical later.
func recordSplitVerdict(s *store.Store, sid string, in *bufio.Reader, out io.Writer, judged bool, feedback map[string]any) {
	provider, split := splitOf(s, sid)
	if !split {
		return
	}
	verdict, source := "", "feedback"
	switch {
	case judged && feedback["adopted"] == "as-is":
		verdict = "good"
	case judged && (feedback["adopted"] == "with-edits" || feedback["reverted"] == true):
		verdict, source = askSplitBlame(in, out), "question"
	case judged:
		// Enter/EOF — the human declined to grade the session at all. Pressing
		// them for a second opinion on the split would be asking twice for
		// something they just said they could not say.
	default:
		// `do`'s split parent: no Feedback exists to ride on, so the only
		// question asked here is about the 分け方 itself.
		verdict, source = askSplitQuality(in, out, provider), "question"
	}

	payload := map[string]any{"sid": sid, "provider": provider, "verdict": verdict, "source": source}
	if err := s.AppendEvent(sid, "user.split_verdict", time.Now().UnixMilli(), payload); err != nil {
		fmt.Fprintln(os.Stderr, "split verdict: 記帳に失敗:", err)
	}
}

// askSplitBlame is the follow-up on a session that did not go well. It asks
// whether the split was the cause — not whether it was good — because that is
// the question a disappointing session can actually answer. 「関係なかった」 is a
// real positive signal about the 分け方, not an absence of one.
func askSplitBlame(in *bufio.Reader, out io.Writer) string {
	fmt.Fprint(out, "分け方は関係あった? [1=分け方が悪かった / 2=分け方のせいではない / Enter=わからない] ")
	line, err := in.ReadString('\n')
	if err != nil {
		return ""
	}
	switch strings.TrimSpace(line) {
	case "1":
		return "bad"
	case "2":
		return "good"
	default:
		return ""
	}
}

// askSplitQuality is `do`'s own question. ADR-0023 Decision 5 gave the split
// parent no Feedback because 「親の実行の成果物は分割提案であり、判定対象の
// 作業物がない」— the first half stays true, and the second is what ADR-0051
// revisits: the proposal itself is the artifact being judged here.
func askSplitQuality(in *bufio.Reader, out io.Writer, provider string) string {
	who := provider
	if who == "" {
		who = "Provider"
	}
	fmt.Fprintf(out, "分けたのは %s。この分け方、よかった? [1=よかった / 2=微妙だった / Enter=まだ言えない] ", who)
	line, err := in.ReadString('\n')
	if err != nil {
		return ""
	}
	switch strings.TrimSpace(line) {
	case "1":
		return "good"
	case "2":
		return "bad"
	default:
		return ""
	}
}

// isolatedPath returns the workspace this session declared, or "" when there
// is none to follow.
//
// The existence check here is NOT the verification ADR-0050 Decision 2 refuses.
// The ledger keeps saying exactly what the Provider declared, unchanged and
// unchecked; this is the observer picking a directory it can actually run a
// command in. A declared path that isn't there would make the runner fail to
// start, which records nothing at all (ADR-0052 Decision 4) — so falling back
// to the wired place, loudly, keeps a first-layer signal instead of losing one
// to a declaration nobody could follow.
func isolatedPath(s *store.Store, sid string) string {
	events, err := s.EventsBySession(sid)
	if err != nil {
		return ""
	}
	for _, e := range events {
		if e.Type != "task.workspace" {
			continue
		}
		if isolated, _ := e.Payload["isolated"].(bool); !isolated {
			return ""
		}
		path, _ := e.Payload["path"].(string)
		if path == "" {
			return ""
		}
		if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
			fmt.Fprintf(os.Stderr, "test: 宣言された作業場が見つからないので元の場所で観測する (%s)\n", path)
			return ""
		}
		return path
	}
	return ""
}

// observeFirstLayer runs the command the user wired for this place and records
// what its exit code says (ADR-0052). It is the only writer of test.result —
// ADR-0006 Decision 3 left the type without one, because a test running inside
// someone else's process cannot be identified from outside. Running the command
// ourselves removes the need to identify anything.
//
// Everything here is best-effort and silent by default: a place with no entry
// runs nothing, and a runner that will not start or will not finish records
// nothing (ADR-0052 Decision 4) — neither says a thing about the deliverable,
// and a broken test setup must never be filed as the Provider's failure.
// Diagnostics go to the human-facing stderr面, never to the ledger.
func observeFirstLayer(s *store.Store, sid string, out io.Writer, workDir string) {
	command, dir, ok := observe.Lookup(cfg.TestCommands, workDir)
	if !ok {
		return
	}
	// Follow the work if it moved (ADR-0050 + ADR-0052 Consequences). When the
	// Provider isolated its workspace, the results are in the worktree and the
	// original checkout is untouched — testing the latter would report a
	// serene green about work nobody did there. Which command to run is still
	// a property of the place the user wired; only where it runs moves.
	if isolated := isolatedPath(s, sid); isolated != "" {
		dir = isolated
	}
	res, ran, err := observe.Run(context.Background(), command, dir,
		time.Duration(cfg.TestTimeoutSec)*time.Second)
	if !ran {
		fmt.Fprintf(os.Stderr, "test: 観測できなかったので記帳しない (%s): %v\n", command, err)
		return
	}
	if err := s.AppendEvent(sid, "test.result", time.Now().UnixMilli(), res.Payload()); err != nil {
		fmt.Fprintln(os.Stderr, "test: 記帳に失敗:", err)
		return
	}
	// One line of fact, no recommendation: the grade stays the human's
	// (ADR-0018 経験主権). "だめだったのでは?" would be the harness voting.
	verdict := "通った"
	if !res.Passed {
		verdict = "落ちた"
	}
	fmt.Fprintf(out, "テスト: %s (%s / %.1fs)\n", verdict, command, float64(res.DurationMs)/1000)
}

// perceiveSlotGrace bounds how long a boundary waits for the machine's one
// perception slot. Long enough to outlast an ordinary local model run, short
// enough that a person at the boundary is not left staring.
const perceiveSlotGrace = 90 * time.Second

// acquirePerceiveSlot takes the machine-wide perception lock. A home directory
// that cannot be resolved is not a reason to refuse to learn: it returns
// (nil, nil) and perception runs unserialised, which is exactly today's
// behaviour.
func acquirePerceiveSlot() (*facelock.Lock, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}
	return facelock.AcquireWithin(filepath.Join(home, ".tomobit", "perceive.lock"), perceiveSlotGrace)
}

// reflectBestEffort runs the mirror at the do boundary (ADR-0015):
// detection over the before/after snapshots, one telling a day at most,
// reaction recorded as a Learning Reality. Best-effort — a failure prints
// to stderr but never fails the do.
func reflectBestEffort(s *store.Store, snap *reflection.Snapshot, extras []reflection.Candidate, doSession string) {
	reflectWithIO(s, snap, extras, doSession, bufio.NewReader(os.Stdin), os.Stdout, isTTY(os.Stdin), time.Now().UnixMilli())
}

// reflectWithIO is reflectBestEffort's testable core (the askWithIO split).
// extras are candidates the snapshot comparison cannot see (re-perception,
// ADR-0019 Decision 4). The seed doubles as nowMs: millisecond resolution is
// plenty for a 1/day lottery, and RecordAndApply persists it either way.
// humanPresent is the same "is anyone there to read this" predicate askWithIO
// takes (ADR-0035 Decision 2) — a terminal or a declared view stream, not
// whether that reader has a screen to draw into.
func reflectWithIO(s *store.Store, snap *reflection.Snapshot, extras []reflection.Candidate, doSession string, in *bufio.Reader, out io.Writer, humanPresent bool, now int64) {
	// Nobody present has no reader to mirror to; the budget stays unspent.
	if !humanPresent {
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
func runHuman(s *store.Store, out io.Writer, sid string, in *bufio.Reader) (executor.Result, error) {
	if err := s.AppendEvent(sid, "provider.selected", time.Now().UnixMilli(),
		map[string]any{"provider": "human"}); err != nil {
		return executor.Result{}, err
	}
	// out, not os.Stdout: a chat's routing line must ride c.out so the NDJSON
	// view stream stays intact (ADR-0032 Decision 1); do passes os.Stdout, so its
	// output is byte-for-byte what it always was.
	fmt.Fprintf(out, "\n「%s」\n", voice.RouteHuman())
	// EOF (piped stdin) falls straight through: the adoption prompt then
	// also sees EOF and records no signal, which is correct for a headless
	// run that no human actually performed.
	in.ReadString('\n')
	return executor.Result{Started: true}, nil
}

// openTask opens the ledger session for one task and settles who will run it.
// `do` and a chat's first turn open the same thing (ADR-0022 Decision 1), so
// they open it the same way.
//
// A named provider is resolved before anything is recorded: a session whose
// provider never existed would sit in the ledger as a task that never
// happened. auto is resolved after, by nature — its decision reads the
// projections and is recorded into the very session it is deciding for.
// human is a provider with no adapter (ADR-0018 Decision 2): the same ledger,
// gate, and rehabilitation, executed by the user.
//
// tp is the task's Task Perception holder (ADR-0036 Decision 2b); it may be
// nil, in which case autoDecide falls back to Decision 1's deterministic
// tokens alone.
func openTask(s *store.Store, out io.Writer, providerName, capability, size, intent string, tp *taskPerception) (sid string, adapter executor.Adapter, human bool, err error) {
	human = providerName == "human"
	if !human && providerName != "auto" {
		if adapter, err = resolveProvider(providerName); err != nil {
			return "", nil, false, err
		}
	}

	now := time.Now().UnixMilli()
	sid = store.NewID(now)
	if err = s.AppendEvent(sid, "task.started", now,
		map[string]any{"intent": intent, "source": "production"}); err != nil {
		return "", nil, false, err
	}
	if err = s.AppendEvent(sid, "capability.started", now,
		map[string]any{"capability": capability}); err != nil {
		return "", nil, false, err
	}

	if providerName == "auto" {
		dec, err := autoDecide(s, out, sid, capability, size, tp)
		if err != nil {
			return "", nil, false, err
		}
		if dec.Provider == "human" {
			human = true
		} else {
			adapter = providers[dec.Provider]
		}
	}
	return sid, adapter, human, nil
}

// ensureClaudeProfile gates every path that can launch claude-code — auto can
// pick it too, and a chat can switch to it mid-conversation. Called before any
// event is recorded, so a misconfigured shell never pollutes the ledger with a
// run that was doomed at launch.
func ensureClaudeProfile(in *bufio.Reader, providerName string) error {
	return ensureClaudeProfileIO(in, os.Stdout, providerName, isTTY(os.Stdin) && isTTY(os.Stdout))
}

// ensureClaudeProfileIO is ensureClaudeProfile's testable core (the askWithIO
// split): interactivity is injected, so the question — the branch that costs
// the user a prompt if it reads from the wrong place — can be exercised
// without a terminal.
//
// On a terminal the missing choice becomes the question itself (ADR-0021
// Decision 4); headless (daemon, cron, pipe) it stays a hard error.
func ensureClaudeProfileIO(in *bufio.Reader, out io.Writer, providerName string, interactive bool) error {
	if providerName != "claude-code" && providerName != "auto" || claudeProfileSet {
		return nil
	}
	if !interactive {
		return fmt.Errorf("no claude-code profile chosen — run `tomobit setup`,\n" +
			"  or export TOMOBIT_CLAUDE_CONFIG_DIR=$HOME/.claude-personal\n" +
			"  (set it empty to deliberately inherit the parent environment)")
	}
	fmt.Fprintln(out, "claude-code のプロファイルがまだ決まっていない。")
	return askClaudeProfile(in, out)
}

// resolvePlan turns the --plan flag into a canonical plan name and records
// the choice (ADR-0014). "" keeps the plain single-run behavior and records
// nothing; "auto" derives the live menu, lets Curiosity propose into free
// space, and runs the same decision rule over the plan bets; anything else
// is a label or explicit steps.
func resolvePlan(s *store.Store, sid, arg, capability, size string, tp *taskPerception) (string, error) {
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
		var semantic []string
		if tp != nil {
			// os.Stdout, not an out param: resolvePlan already prints its own
			// "proposed plan"/"plan:" lines straight to os.Stdout above and
			// below (cmdDo's only caller), so the degrade log follows the
			// same convention rather than inventing an io.Writer parameter
			// this function has never taken.
			semantic = tp.semanticTokens(os.Stdout)
		}
		tokens := perceptionTokens(capability, size, semantic)
		dec := decide.ChoosePlan(conns, menu, tokens, size, now.UnixNano(), now.UnixMilli())
		payload := map[string]any{
			"plan": dec.Provider, "seed": strconv.FormatInt(dec.Seed, 10),
			"n": dec.N, "q": dec.Q, "fallback": dec.Fallback,
			"cap": capability, "size": size,
			"tokens": tokens, // ADR-0036 Decision 2d/2e: the actual scope-match input, for replay
		}
		if tp != nil {
			if reason := tp.degradedReason(); reason != "" {
				payload["perception_degraded"] = reason
			}
		}
		if err := s.AppendEvent(sid, "plan.selected", now.UnixMilli(), payload); err != nil {
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

// decisionTokens is the context the Decision Engine reads a Connection
// against (ADR-0013 Decision 2: 判断は最細一致のみを読む). v1 carries only what
// the harness knows for certain before anything runs — the capability asked
// for and the stakes declared — because a token that is a guess would pick a
// finer Connection on a guess (ADR-0036 Decision 1).
//
// size rides here as well as into Draws(): the same attribute cannot be the
// stakes of the lottery and invisible to the scope match. Its absence was the
// gap, not a decision.
//
// An empty value is not a token. Experience.Tokens() skips empty values when
// it feeds engine.applyTo, so emitting "size=" here would ask the ledger for a
// scope no experience can ever have written.
//
// The semantic attributes (lang / framework / topic) need Task Perception —
// a guess, never certain before the run — so they do not belong in this
// deterministic-only set. perceptionTokens below is the caller-facing token
// list: decisionTokens' output plus whatever Task Perception adds, with this
// function's tokens always taking precedence (ADR-0036 Decision 2b/2c).
func decisionTokens(capability, size string) []string {
	tokens := []string{core.CanonToken("cap", capability)}
	if size != "" {
		tokens = append(tokens, core.CanonToken("size", size))
	}
	return tokens
}

// perceptionTokens folds Task Perception's semantic tokens (ADR-0036 Decision
// 2b/2c) into decisionTokens' deterministic ones. cap and size — Decision 1's
// tokens — always win: a token the extractor guessed at either key is
// dropped, so the harness's own certain knowledge is never overwritten by a
// model's guess at the same key (perceive.PerceiveTaskContext's own doc
// leaves this precedence to the caller — this is that caller).
//
// size is dropped even when --size was left unset. The lottery's n(stakes)
// reads the flag, not the token (decide.Draws), so a guessed size token would
// name a scope the same decision never widened its draws for — the audit would
// read size=large beside n=1. An attribute that only half the decision can see
// is worse than an absent one.
//
// The consequence is that a run without --size never reaches a size-scoped
// Connection, even though session perception writes size into the Context that
// grows one. That asymmetry is the honest one: size declares the stakes being
// taken, and on such a run nobody declared any.
func perceptionTokens(capability, size string, semantic []string) []string {
	tokens := decisionTokens(capability, size)
	for _, tok := range semantic {
		k, _, ok := strings.Cut(tok, "=")
		if !ok || k == "cap" || k == "size" {
			continue
		}
		tokens = append(tokens, tok)
	}
	return tokens
}

// contextTokens turns a Task Perception extraction map (already trimmed and
// lowercased by perceive.PerceiveTaskContext) into canonical "k=v" scope
// tokens — the caller's own CanonToken call PerceiveTaskContext's doc defers
// to. Keys are sorted first so the result is deterministic (map iteration
// order is not — ADR-0011: 判断は数学), which matters here because this slice
// is what the decision audit records.
func contextTokens(ctx map[string]string) []string {
	keys := make([]string, 0, len(ctx))
	for k := range ctx {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	tokens := make([]string, 0, len(keys))
	for _, k := range keys {
		if ctx[k] == "" {
			continue
		}
		tokens = append(tokens, core.CanonToken(k, ctx[k]))
	}
	return tokens
}

// taskExtractFunc extracts Task Perception's semantic tokens (ADR-0036
// Decision 2) from a task's intent alone, before anything has run. A
// taskPerception holder takes this as a function value, not a direct
// store+extractor pair, so its own "extract once, lazily" logic (below) can
// be pinned in tests against a plain counting fake instead of a real store
// and a real LLM backend.
type taskExtractFunc func(intent string) (map[string]string, error)

// taskExtractFuncFor is the production taskExtractFunc: it gathers the known-
// vocabulary map (taskVocab) and calls the wired extractor's own Task
// Perception entry point (perceive.PerceiveTaskContext).
func taskExtractFuncFor(s *store.Store, extractor perceive.Extractor) taskExtractFunc {
	return func(intent string) (map[string]string, error) {
		vocab, err := perceive.Vocab(s)
		if err != nil {
			return nil, err
		}
		return perceive.PerceiveTaskContext(extractor, intent, vocab)
	}
}

// taskPerception is one task's lazy Task Perception holder (ADR-0036 Decision
// 2b): whichever of autoDecide / ChoosePlan (resolvePlan) / pickDuelGap
// (duelOffer) asks first runs extract exactly once; every later asker —
// including a split child, which is handed the same holder rather than
// building its own (ADR-0036 Decision 2b: 子ごとに再知覚しない) — reads the
// cached result. A task nobody asks (--provider claude-code --plan direct,
// duel unfired) never runs extract at all — the cost ADR-0036 measured is
// spent only when it buys something. A nil *taskPerception is a valid,
// silent "not participating" state (e.g. a duel child, whose provider is
// already fixed to one side of the gap and never reaches autoDecide).
type taskPerception struct {
	intent  string
	extract taskExtractFunc

	once     sync.Once
	tokens   []string // canonical semantic tokens; nil if never asked or degraded
	degraded string   // why extraction produced no tokens; "" = never asked, or it worked
}

func newTaskPerception(intent string, extract taskExtractFunc) *taskPerception {
	return &taskPerception{intent: intent, extract: extract}
}

// semanticTokens runs extraction on the first call and returns the cached
// tokens on every later one (ADR-0036 Decision 2b: 遅延して1回). A missing
// extractor or a failed extraction is not silence: one operational log line
// (ADR-0009 — the machine's channel, not Tomo's voice) names the reason, and
// the caller proceeds on Decision 1's deterministic tokens alone
// (perceptionTokens above).
func (tp *taskPerception) semanticTokens(out io.Writer) []string {
	tp.once.Do(func() {
		switch {
		case tp.extract == nil:
			tp.degraded = "no task perception extractor wired"
		default:
			ctx, err := tp.extract(tp.intent)
			if err != nil {
				tp.degraded = err.Error()
			} else {
				tp.tokens = contextTokens(ctx)
			}
		}
		if tp.degraded != "" {
			fmt.Fprintln(out, "perceive: task perception degraded —", tp.degraded, "— deciding on deterministic tokens only")
		}
	})
	return tp.tokens
}

// degradedReason reports semanticTokens' failure reason, for the decision
// audit (ADR-0036 Decision 2d) — "" both when extraction was never asked for
// and when it succeeded, since neither has anything to confess.
func (tp *taskPerception) degradedReason() string { return tp.degraded }

// decidedViewer lets autoDecide's out writer double as the sink for the
// ADR-0040 decided view event, without giving autoDecide (or the whole
// openTask/openSubtask/duelOffer/runSplit call chain above it, shared by both
// `do` and chat) a second parameter just to carry an optional stream. chat's
// noteWriter is the only implementer. sid rides along because a split's
// subtasks each call autoDecide for their own session — the GUI's only way
// to tell whose decision this is.
type decidedViewer interface {
	viewDecided(sid string, dec decide.Decision)
}

// autoDecide runs the Decision Engine (ADR-0012) over the current
// projections and records the full audit — seed included — as a
// tomo.decided event, so the same ledger + the same seed replays the same
// choice. The seed is stored as a string: a UnixNano exceeds JSON's exact
// float64 integer range and would silently lose the bits that make the
// lottery replayable.
//
// tp is the task's Task Perception holder (ADR-0036 Decision 2b) — nil skips
// semantic extraction entirely (decisionTokens' cap/size alone), which is
// exactly right for a caller that structurally never wants it (a duel child,
// whose provider is already the gap's own pick).
func autoDecide(s *store.Store, out io.Writer, sid, capability, size string, tp *taskPerception) (decide.Decision, error) {
	conns, err := s.AllConnections()
	if err != nil {
		return decide.Decision{}, err
	}
	now := time.Now()
	var semantic []string
	if tp != nil {
		semantic = tp.semanticTokens(out)
	}
	tokens := perceptionTokens(capability, size, semantic)
	// Candidates are the adapters that can actually launch (ADR-0043 Decision
	// 2). human still competes on the same ledger with the same gate
	// (ADR-0018 Decision 2) — but only when this context already knows human
	// (ADR-0043 Decision 4): an ignorant lottery would hand the task back to
	// the user as "exploration" whose cost is the user working, and human
	// needs no lottery to bootstrap — `--provider human` writes its evidence
	// directly.
	candidates := availableProviderNames()
	if decide.KnowsCapability(conns, "human", tokens) {
		candidates = append(candidates, "human")
	}
	if len(candidates) == 0 {
		return decide.Decision{}, fmt.Errorf(
			"auto: no runnable provider on this machine (registered: %s) — install one, or pin --provider human",
			strings.Join(providerNames(), ", "))
	}
	dec := decide.Choose(conns, candidates, tokens, size, now.UnixNano(), now.UnixMilli())

	cands := make([]map[string]any, len(dec.Candidates))
	for i, c := range dec.Candidates {
		cands[i] = map[string]any{
			"provider": c.Provider, "quantile": c.Quantile,
			"passed": c.Passed, "scope": c.ScopeKey,
			// wins is -1 for a gated-out candidate (never entered the pairwise
			// tournament) and the win tally otherwise (ADR-0012) — recorded
			// here so the ledger, not just the view, holds the full audit
			// ADR-0040 says autoDecide already has (Decision 1: view mirrors
			// the ledger, it does not add to it).
			"wins": c.Wins,
		}
	}
	payload := map[string]any{
		"provider": dec.Provider, "seed": strconv.FormatInt(dec.Seed, 10),
		"n": dec.N, "q": dec.Q, "fallback": dec.Fallback,
		"cap": capability, "size": size, "candidates": cands,
		// tokens is the actual scope-match input this decision read (ADR-0036
		// Decision 2d/2e): with a semantic extractor in the loop, re-running
		// extraction later is not guaranteed to reproduce it, so replay
		// (ADR-0012 Decision 5) needs it recorded verbatim, not reconstructed
		// from cap/size alone.
		"tokens": tokens,
	}
	if tp != nil {
		if reason := tp.degradedReason(); reason != "" {
			payload["perception_degraded"] = reason
		}
	}
	if err := s.AppendEvent(sid, "tomo.decided", now.UnixMilli(), payload); err != nil {
		return decide.Decision{}, err
	}
	// The same audit rides the NDJSON view stream (ADR-0040 Decision 1) — dec
	// itself, not a recomputation, so a GUI can show "why this provider"
	// without ever reading the ledger. out is the only signal available here
	// (autoDecide has no chat/stream argument of its own): a chat's noteWriter
	// implements decidedViewer under --view ndjson, do's os.Stdout and a plain
	// chat's out do not, so this is silently a no-op everywhere else.
	if dv, ok := out.(decidedViewer); ok {
		dv.viewDecided(sid, dec)
	}
	// Operational log line (ADR-0009: machine channel, not Tomo's voice). It
	// rides out, not os.Stdout, so a chat's auto decision (openTask / a split's
	// openSubtask) frames through c.out and never leaks a bare line into the
	// NDJSON view stream (ADR-0032 Decision 1); do passes os.Stdout, unchanged.
	if dec.Fallback {
		fmt.Fprintf(out, "decided %s (every provider below the gate — least pessimistic chosen)\n", dec.Provider)
	} else {
		fmt.Fprintf(out, "decided %s (n=%d)\n", dec.Provider, dec.N)
	}
	// The calibrated voice (ADR-0019 Decision 1): confidence follows the
	// judgment's wobble, measured with the same sampler the decision used.
	// human speaks its own routing line in runHuman instead.
	if dec.Provider != "human" {
		w := decide.Wobble(conns, candidates, tokens, size, 64, 1, now.UnixMilli())
		fmt.Fprintf(out, "\n「%s」\n\n", voice.Decided(dec.Provider, w))
	}
	return dec, nil
}

// askWithIO is the Curiosity question at a task boundary (ADR-0007), and
// finishTask's only way in. stdin/stdout/clock are injected so the budget
// check and the recording side effect can be exercised without a real
// terminal; humanPresent is finishTask's own argument — a terminal or a
// declared view stream, ADR-0035 Decision 1/2 — not a live isTTY() read.
func askWithIO(s *store.Store, doSession string, in *bufio.Reader, out io.Writer, humanPresent bool, now int64) {
	// Nobody present has no one to interrupt, so we neither ask nor record: the
	// budget guards interruption frequency, and recording a tomo.asked here
	// would silently burn 24h of budget on a headless run that interrupted no
	// one.
	if !humanPresent {
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
	// A run that never launched is not evidence about the provider (ADR-0043
	// Decision 3): "couldn't be started" and "ran and failed" are different
	// facts, and recording the first as provider.error would let a missing
	// binary sink a capability (perceive maps provider.error to
	// outcome.Failed). Every caller still surfaces the error itself — do exits
	// non-zero, chat prints it, split fail-stops, duel prints the side's launch
	// failure and skips the verdict — so nothing is swallowed, just never
	// ledgered as the provider's failure.
	if !result.Started && runErr != nil {
		return nil, false
	}
	msg := fmt.Sprintf("provider exited with code %d", result.ExitCode)
	if runErr != nil {
		msg = runErr.Error()
	}
	return map[string]any{"message": msg}, true
}

// feedbackPayload produces the closing task.finished payload (ADR-0006
// Decision 4; 呼称の統一 ADR-0028) — from the reaction the human already placed
// during the conversation, or, failing that, from the one closing question.
// The question is a verdict on the session's quality, not a retention action:
// by the time a do finishes the user has already iterated in-dialogue until
// satisfied, so "keep it?" is moot — what the ledger still wants is how good
// the result was. 1/2/3 grade it; Enter and any other input (including EOF on
// non-interactive stdin) carry no signal, so the payload is empty. The
// no-signal default is deliberate — a mindless Enter or a headless run must
// never inflate the ledger with praise. The payload keys stay adopted/reverted
// (SCHEMA + rebuild unchanged — ADR-0028): only the vocabulary moved.
//
// 反応が置かれていれば訊かない (ADR-0057 Decision 2)。人が答える口が2つに
// なっただけで、台帳に落ちる形は1つも増えていない — 訳の対応は closingGrades
// という1枚の表から両方が引く。`do` の締めはこの経路を通らない: 会話が無い
// のだから反応も1件も無く、従来どおり問いへ落ちる。
//
// Why not 反応があっても確認だけ出すか: 押させる儀式が1つ減るだけで、待たせて
// いる事実は変わらない。反応は clear で取り消せるし、締めた後も第2層
// (tomobit verdict) が残っている — 可逆な操作に確認を積まない。
func feedbackPayload(s *store.Store, sid string, in *bufio.Reader, out io.Writer) map[string]any {
	grade, placed, err := lastReaction(s, sid)
	if err != nil {
		// 読めなかったなら訊く側へ落ちる: 反応を読み落として黙って無信号にするより、
		// 人へもう一度訊く方が安い。失敗自体は隠さない。
		fmt.Fprintln(os.Stderr, err)
	}
	if placed {
		// 黙って記録しない (ADR-0057 Decision 2): 沈黙で済ませると、人は自分が
		// いつ採点したかを思い出せない。
		fmt.Fprintln(out, dim(fmt.Sprintf("今回は %s として記録した（会話中に置いた反応）", grade.label)))
		return grade.payload()
	}
	fmt.Fprintf(out, "今回、どうだった? [%s / Enter=まだ言えない] ", feedbackChoices())
	line, err := in.ReadString('\n')
	if err != nil {
		return map[string]any{}
	}
	if g, ok := gradeByChoice(strings.TrimSpace(line)); ok {
		return g.payload()
	}
	return map[string]any{}
}

// perceiveLive sorts a perceive run's batch into the (ts, id) order
// Engine.PerceiveBatch's contract requires, then folds it into the live
// projection through that one call (ADR-0041): live Apply+ReconcileMerges,
// or a Rebuild — logged to stderr immediately before Rebuild starts, since
// Rebuild is not one transaction and a mid-rebuild crash must not go silent
// (ADR-0041 前提と残す露出) — when the batch is out of order or re-perceives
// an already-known session. cmdPerceive and perceiveBestEffort both route
// through this one function so the guard reaches do/chat's tail perception
// exactly as it reaches the operational command, not just one of them.
//
// curErr is the error from the CurrentExperiences() call that produced
// beforeCurrent, checked here rather than trusted to the caller: a failed
// fetch silently handing PerceiveBatch a nil/incomplete `known` would defeat
// both of its guards without anyone noticing (cmdPerceive used to do
// exactly that, ignoring the error until a later, unrelated read).
func perceiveLive(en *core.Engine, batch, beforeCurrent []*core.Experience, curErr error) error {
	if curErr != nil {
		return fmt.Errorf("reading experiences_current: %w", curErr)
	}
	sort.Slice(batch, func(i, j int) bool {
		if batch[i].TS != batch[j].TS {
			return batch[i].TS < batch[j].TS
		}
		return batch[i].ID < batch[j].ID
	})
	onRebuild := func() {
		fmt.Fprintln(os.Stderr, "out-of-order batch — rebuilding projections")
	}
	_, err := en.PerceiveBatch(batch, beforeCurrent, onRebuild)
	return err
}

// perceiveBestEffort mirrors cmdPerceive but never fails the run: if Ollama is
// down the session stays pending (Deferred Perception, ADR-0006 Decision 5).
// It returns any re-perception candidates (ADR-0019 Decision 4) for the
// reflection boundary that follows.
//
// Unlike cmdPerceive, the do user gets no per-experience machine log line —
// the one spoken line (ADR-0009) replaces it, since a do session's user
// wants to hear what Tomo learned, not read an extraction trace.
func perceiveBestEffort(s *store.Store, out io.Writer, extractor perceive.Extractor) []reflection.Candidate {
	// 知覚は機械に1本ずつ (GUI ADR-0009 Decision 5)。窓が4つあれば境界も4つ
	// 同時に来うるが、ローカルのモデルは1つしかない — 同時に4本走らせても
	// 速くならず、機械によっては全部が遅くなる。順序を落としても台帳は嘘を
	// つかない（ADR-0041 が out-of-order 知覚を扱っている）ので、待つ。
	//
	// 待ちきれなければ知覚を諦める: セッションは pending のまま残り、
	// `tomobit perceive` が後で消化する。境界を人質に取らないための降り方で、
	// モデルが落ちている時の既存の劣化と同じ結末になる。
	if lock, err := acquirePerceiveSlot(); err != nil {
		fmt.Fprintln(out, "perception pending — 他の会話が知覚中: run `tomobit perceive` later")
		return nil
	} else if lock != nil {
		defer lock.Release()
	}

	p := &perceive.Perceiver{
		Store:     s,
		Extractor: extractor,
		Ver:       extractorVer,
	}
	beforeCurrent, err := s.CurrentExperiences()
	if err != nil {
		fmt.Fprintln(out, "perception pending — run `tomobit perceive` later:", err)
		return nil
	}
	exps, err := p.Run()
	if err != nil {
		fmt.Fprintln(out, "perception pending — run `tomobit perceive` later:", err)
		return nil
	}
	if len(exps) == 0 {
		return nil
	}

	now := time.Now().UnixMilli()
	before, err := s.AllConnections()
	if err != nil {
		fmt.Fprintf(out, "perceived but projection is stale — run `tomobit rebuild`: %v\n", err)
		return nil
	}
	stageBefore, err := face.StageFrom(s, now)
	if err != nil {
		fmt.Fprintf(out, "perceived but projection is stale — run `tomobit rebuild`: %v\n", err)
		return nil
	}

	en := &core.Engine{Repo: s}
	// curErr is nil here — already checked and returned on above.
	if err := perceiveLive(en, exps, beforeCurrent, nil); err != nil {
		fmt.Fprintf(out, "perceived but projection is stale — run `tomobit rebuild`: %v\n", err)
		return nil
	}
	expIDs := make([]string, 0, len(exps))
	for _, e := range exps {
		expIDs = append(expIDs, e.ID)
	}

	after, err := s.AllConnections()
	if err != nil {
		fmt.Fprintf(out, "perceived but projection is stale — run `tomobit rebuild`: %v\n", err)
		return nil
	}
	stageAfter, err := face.StageFrom(s, now)
	if err != nil {
		fmt.Fprintf(out, "perceived but projection is stale — run `tomobit rebuild`: %v\n", err)
		return nil
	}
	maxExcess, err := s.MaxExcess(expIDs)
	if err != nil {
		maxExcess = 0
	}
	if text, ok := voice.Perceive(stageBefore, stageAfter, voice.NewSplits(before, after), exps, maxExcess); ok {
		fmt.Fprintf(out, "\n「%s」\n", text) // speaker separation: a blank line before Tomo's line
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
	backend := fs.String("backend", "", "perception backend: ollama|mlx-lm (default: resolved from config)")
	model := fs.String("model", "", "perception model for context extraction (default depends on --backend)")
	url := fs.String("url", "", "perception backend url (default depends on --backend)")
	fs.Parse(args)

	s, err := openStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()

	extractor, err := newExtractor(*backend, *url, *model)
	if err != nil {
		return err
	}
	p := &perceive.Perceiver{
		Store:     s,
		Extractor: extractor,
		Ver:       extractorVer,
	}
	snap, snapErr := reflection.TakeSnapshot(s, time.Now().UnixMilli())
	beforeCurrent, curErr := s.CurrentExperiences()
	exps, err := p.Run()
	var extras []reflection.Candidate
	if len(exps) > 0 {
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
		if liveErr := perceiveLive(en, exps, beforeCurrent, curErr); liveErr != nil {
			return fmt.Errorf("%w (experiences are saved; the projection is stale — run `tomobit rebuild` to repair)", liveErr)
		}
		expIDs := make([]string, 0, len(exps))
		for _, e := range exps {
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

// cmdStatus is the companion view (ADR-0008 Consequences): one `Tomo 「…」`
// spoken line, then the connections table. Growth and mood now read off the
// desktop sprite alone (ADR-0025) — its shape is the stage, its face the mood —
// so the terminal no longer spells either in text. It backs both `tomobit
// status` and bare `tomobit`, and spawns the face window on a TTY.
func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	db := dbFlag(fs)
	view := fs.String("view", "human", "output view: human (default) or json")
	fs.Parse(args)
	switch *view {
	case "human":
		maybeLaunchFace(*db)
		s, err := openStore(*db)
		if err != nil {
			return err
		}
		defer s.Close()
		return showStatus(os.Stdout, s, collectQuotaFn)
	case "json":
		return statusJSON(os.Stdout, *db, collectQuotaFn)
	default:
		return fmt.Errorf("unknown --view %q (human, json)", *view)
	}
}

// statusPayload is the machine view `tomobit status --view json` writes
// (ADR-0039 Decision 1): the one derivation GUI reads instead of hand-porting
// stage.go. Stage is a pointer so a real S0 (毛玉, the zero stage constant)
// still serializes as "stage":0 rather than vanishing under omitempty —
// only the absent-ledger case leaves it nil.
type statusPayload struct {
	Type      string          `json:"type"`
	Exists    bool            `json:"exists"`
	Stage     *int            `json:"stage,omitempty"`
	StageName string          `json:"stage_name,omitempty"`
	Mood      *statusMood     `json:"mood,omitempty"`
	Speak     string          `json:"speak,omitempty"`
	Providers []providerUsage `json:"providers,omitempty"`
	Growth    *statusGrowth   `json:"growth,omitempty"`
	// Quota is each Provider's vendor-reported utilization (ADR-0044). Attached
	// only when the ledger exists, like Providers/Growth: exists:false carries
	// nothing else. Each row is either observed windows or a 不明 Error, so a
	// GUI reader can render the honesty boundary rather than a silent gap.
	Quota []quotaStatus `json:"quota,omitempty"`
}

type statusMood struct {
	Name   string `json:"name"`
	Marker string `json:"marker"`
}

// statusGrowth is the growth disclosure (ADR-0046 Decision 1): the gates the
// next stage requires, straight from face.GrowthFrom — the same evaluation
// that produced the stage field, not a second derivation. Absent entirely at
// あいぼう (no next, no fake 100%) and on old readers' side ignored under the
// ADR-0032 forward-compat contract.
type statusGrowth struct {
	Next     int          `json:"next"`
	NextName string       `json:"next_name"`
	Gates    []statusGate `json:"gates"`
}

// statusGate mirrors face.Gate. Value carries no omitempty: 測定不能 must
// serialize as an explicit null, not vanish — a reader has to be able to
// tell 「測れない」 from 「届いていない」 (ADR-0046 Decision 1). Hint is the
// one move that raises the value, only on unmet gates (Decision 3).
type statusGate struct {
	Name      string   `json:"name"`
	Value     *float64 `json:"value"`
	Threshold float64  `json:"threshold"`
	Met       bool     `json:"met"`
	Hint      string   `json:"hint,omitempty"`
}

// statusCandidates reduces connections to the voice.Candidate rows and state
// names both status views read. Shared between showStatus and statusJSON so
// the reduction can't drift between the human and machine view — the same
// divergence ADR-0039 exists to kill between binaries.
func statusCandidates(s *store.Store, conns []*core.Connection, now int64) ([]voice.Candidate, []string, error) {
	en := &core.Engine{Repo: s}
	cands := make([]voice.Candidate, len(conns))
	states := make([]string, len(conns))
	for i, c := range conns {
		sum, err := en.LedgerSum(c, now)
		if err != nil {
			return nil, nil, err
		}
		state := c.State(now, sum)
		cands[i] = voice.Candidate{Conn: c, State: state, LedgerSum: sum}
		states[i] = state
	}
	return cands, states, nil
}

// statusJSON writes the machine view. It stats the DB path before opening it
// (ADR-0039 Decision 1): openStore's MkdirAll+store.Open would otherwise
// create a ledger just because a machine reader asked to look — a mutation
// the human `status` never had reason to guard against, since it always drew
// the companion view anyway. No face window, no greeting: a machine reader is
// not the person the return greeting or the desktop sprite are for.
//
// Unlike chat's --view ndjson, a TTY is not rejected: that gate protects a
// stream whose framing assumes every terminal organ stayed closed, while this
// is a one-shot object with those organs unconditionally off — a human piping
// it through jq on a terminal breaks nothing.
func statusJSON(w io.Writer, dbPath string, quotaFn quotaCollector) error {
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return json.NewEncoder(w).Encode(statusPayload{Type: "status", Exists: false})
		}
		return err
	}

	s, err := openStore(dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	now := time.Now().UnixMilli()
	growth, err := face.GrowthFrom(s, now)
	if err != nil {
		return err
	}
	stage := growth.Stage
	conns, err := s.AllConnections()
	if err != nil {
		return err
	}
	cands, states, err := statusCandidates(s, conns, now)
	if err != nil {
		return err
	}
	moodName, moodMarker := face.Mood(states)

	payload := statusPayload{
		Type:      "status",
		Exists:    true,
		Stage:     &stage,
		StageName: face.StageName(stage),
		Mood:      &statusMood{Name: moodName, Marker: moodMarker},
	}
	if next, ok := growth.Next(); ok {
		gates := make([]statusGate, len(growth.Gates))
		for i, g := range growth.Gates {
			gates[i] = statusGate{Name: g.Name, Value: g.Value, Threshold: g.Threshold, Met: g.Met, Hint: g.Hint()}
		}
		payload.Growth = &statusGrowth{Next: next, NextName: face.StageName(next), Gates: gates}
	}
	if text, ok := voice.Suggest(cands, now); ok {
		payload.Speak = text
	}
	exps, err := s.CurrentExperiences()
	if err != nil {
		return err
	}
	if usage := providerUsageSummary(exps); len(usage) > 0 {
		payload.Providers = usage
	}
	if quotaFn != nil {
		qctx, cancel := context.WithTimeout(context.Background(), quotaFetchTimeout)
		q := quotaFn(qctx)
		cancel()
		if len(q) > 0 {
			payload.Quota = q
		}
	}
	return json.NewEncoder(w).Encode(payload)
}

// showStatus draws the companion view on an already-open store, so a chat can
// slip it between turns (/status) without opening the DB a second time. It
// writes to w rather than os.Stdout directly (ADR-0032 Decision 1) so a chat's
// /status can route through the framing writer that keeps stdout NDJSON; the
// TTY gate still reads the real terminal, since w may be a wrapper around it.
func showStatus(w io.Writer, s *store.Store, quotaFn quotaCollector) error {
	conns, err := s.AllConnections()
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()

	// Quota is fetched once here (a network read of each Provider's usage
	// endpoint, ADR-0044) and rendered below whichever branch runs. quotaFn is
	// nil on chat's inline /status, keeping the between-turn view offline; only
	// the standalone `tomobit status` pays the network wait.
	var quotaRows []quotaStatus
	if quotaFn != nil {
		qctx, cancel := context.WithTimeout(context.Background(), quotaFetchTimeout)
		quotaRows = quotaFn(qctx)
		cancel()
	}

	// A pipe or redirect wants the machine-readable table only — no speech
	// (ADR-0008 Decision 4). The avatar is gone (ADR-0025): the desktop window
	// draws the face now, and it alone carries growth and mood — the terminal
	// keeps only Tomo's one spoken line.
	tty := isTTY(os.Stdout)
	// On a TTY the companion view sits at the chat's gutter, table included; a
	// pipe gets the untouched machine-readable table flush left.
	out := w
	if tty {
		out = newIndentWriter(w, gutter)
	}
	if tty {
		greetIfReturned(out, s, conns, now)
	}

	// Providers is independent of whether any capability Connection has been
	// born yet (a Provider's usage and the projection it feeds decay at
	// different rates), so it is read once here and rendered in both branches
	// below rather than only the len(conns) > 0 path.
	exps, err := s.CurrentExperiences()
	if err != nil {
		return err
	}
	usage := providerUsageSummary(exps)

	if len(conns) == 0 {
		if tty {
			fmt.Fprintf(out, "Tomo %s\n\n", voice.FirstMeeting())
		}
		fmt.Fprintln(out, "no connections yet — record a session and run `tomobit perceive`")
		if len(usage) > 0 {
			fmt.Fprintln(out)
			if err := printProviderUsage(out, usage, now); err != nil {
				return err
			}
		}
		if len(quotaRows) > 0 {
			fmt.Fprintln(out)
			return printQuota(out, quotaRows, now)
		}
		return nil
	}

	cands, _, err := statusCandidates(s, conns, now)
	if err != nil {
		return err
	}

	if tty {
		// Stage and mood used to print here; the desktop sprite now carries both
		// (ADR-0025), so the terminal keeps only the one spoken line. With no
		// remark to make (a preference-only network), Tomo still names itself —
		// the companion's presence is the view, even in silence (ADR-0008).
		if text, ok := voice.Suggest(cands, now); ok {
			fmt.Fprintf(out, "Tomo 「%s」\n\n", text)
		} else {
			fmt.Fprintf(out, "Tomo\n\n")
		}
	}

	if len(usage) > 0 {
		if err := printProviderUsage(out, usage, now); err != nil {
			return err
		}
		fmt.Fprintln(out)
	}

	if err := printConnections(out, cands, now, tty); err != nil {
		return err
	}
	if len(quotaRows) > 0 {
		fmt.Fprintln(out)
		return printQuota(out, quotaRows, now)
	}
	return nil
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
