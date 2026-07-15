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
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/curiosity"
	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/executor/claudecode"
	"github.com/Rererr/tomobit/internal/executor/codex"
	"github.com/Rererr/tomobit/internal/face"
	"github.com/Rererr/tomobit/internal/perceive"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/voice"
)

const extractorVer = 3 // bump when the extraction prompt/schema changes

// providers is the Decision Engine's Phase 1 stand-in (ADR-0010 Decision 1):
// a human picks via --provider until Connections grow enough to justify
// automatic selection. Registered names are SCHEMA.md R3 provider names.
var providers = map[string]executor.Adapter{
	"claude-code": claudecode.New(),
	"codex":       codex.New(),
}

// resolveProvider looks up a registered Adapter by name, or reports the
// available names so an unknown --provider fails with something actionable.
func resolveProvider(name string) (executor.Adapter, error) {
	if a, ok := providers[name]; ok {
		return a, nil
	}
	names := make([]string, 0, len(providers))
	for n := range providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return nil, fmt.Errorf("do: unknown provider %q (available: %s)", name, strings.Join(names, ", "))
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
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
                   [--provider claude-code|codex]
                   [--model qwen3:8b] [--url http://localhost:11434] "<prompt>"
  tomobit record   --session <id> --type <event.type> [--json '{...}']
  tomobit perceive [--model qwen3:8b] [--url http://localhost:11434]
  tomobit rebuild
  tomobit status   same as no args

companion markers: "?" = a connection is questioned / "z" = dormant (long quiet)

common flags:
  --db <path>   database file (default ~/.tomobit/tomobit.db, or $TOMOBIT_DB)`)
}

func dbFlag(fs *flag.FlagSet) *string {
	def := os.Getenv("TOMOBIT_DB")
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
	providerName := fs.String("provider", "claude-code", "adapter to run: claude-code|codex")
	model := fs.String("model", "qwen3:8b", "ollama model for best-effort perception")
	url := fs.String("url", "", "ollama base url (default http://localhost:11434)")
	fs.Parse(args)

	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		return fmt.Errorf("do: a prompt is required")
	}
	adapter, err := resolveProvider(*providerName)
	if err != nil {
		return err
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

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
	result, runErr := ex.Run(ctx, executor.Request{
		Prompt: prompt, PermissionMode: *permMode, Timeout: *timeout,
	}, sink)

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

	perceiveBestEffort(s, &perceive.Ollama{URL: *url, Model: *model})

	// ADR-0007 lists the question right after the adoption prompt, but it runs
	// here — after perception — so today's do is already folded into the Gap
	// derivation. The interruption is still once per do at the same boundary,
	// so the position ADR-0007 protects is unchanged.
	askBestEffort(s, sid)
	return nil
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
//
// Unlike cmdPerceive, the do user gets no per-experience machine log line —
// the one spoken line (ADR-0009) replaces it, since a do session's user
// wants to hear what Tomo learned, not read an extraction trace.
func perceiveBestEffort(s *store.Store, extractor perceive.Extractor) {
	p := &perceive.Perceiver{
		Store:     s,
		Extractor: extractor,
		Ver:       extractorVer,
	}
	exps, err := p.Run()
	if err != nil {
		fmt.Println("perception pending — run `tomobit perceive` later:", err)
		return
	}
	if len(exps) == 0 {
		return
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
		return
	}
	stageBefore := face.Stage(before, now)

	en := &core.Engine{Repo: s}
	for _, e := range exps {
		if err := en.Apply(e); err != nil {
			fmt.Printf("perceived but projection is stale — run `tomobit rebuild`: %v\n", err)
			return
		}
	}

	after, err := s.AllConnections()
	if err != nil {
		fmt.Printf("perceived but projection is stale — run `tomobit rebuild`: %v\n", err)
		return
	}
	if text, ok := voice.Perceive(stageBefore, face.Stage(after, now), voice.NewSplits(before, after), exps); ok {
		fmt.Printf("\n「%s」\n", text) // speaker separation: a blank line before Tomo's line
	}
}

func cmdPerceive(args []string) error {
	fs := flag.NewFlagSet("perceive", flag.ExitOnError)
	db := dbFlag(fs)
	model := fs.String("model", "qwen3:8b", "ollama model for context extraction")
	url := fs.String("url", "", "ollama base url (default http://localhost:11434)")
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
	exps, err := p.Run()
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
		stageBefore := face.Stage(before, now)

		en := &core.Engine{Repo: s}
		for _, e := range exps {
			if applyErr := en.Apply(e); applyErr != nil {
				return fmt.Errorf("apply %s: %w (experiences are saved; the projection is stale — run `tomobit rebuild` to repair)", e.ID, applyErr)
			}
			fmt.Printf("perceived %s: %s %s → %s\n",
				e.SessionID, e.Kind, core.NewScope(e.Tokens()...).Key(), e.Target())
		}

		after, snapErr := s.AllConnections()
		if snapErr != nil {
			return snapErr
		}
		// The machine log lines above stay (this is the operational command,
		// ADR-0009): the spoken line is an addition, not a replacement.
		if text, ok := voice.Perceive(stageBefore, face.Stage(after, now), voice.NewSplits(before, after), exps); ok {
			fmt.Printf("「%s」\n", text)
		}
	}
	if err != nil {
		return err
	}
	if len(exps) == 0 {
		fmt.Println("nothing pending")
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
	stage := face.Stage(conns, now)

	// A pipe or redirect wants the machine-readable table only — no avatar,
	// no speech (ADR-0008 Decision 4: "the avatar only draws on a TTY").
	tty := isTTY(os.Stdout)
	if tty {
		printAvatar(os.Stdout, stage)
		fmt.Println() // speaker separation: a blank line before Tomo's own line
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
