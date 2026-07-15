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
	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/executor/claudecode"
	"github.com/Rererr/tomobit/internal/perceive"
	"github.com/Rererr/tomobit/internal/store"
)

const extractorVer = 3 // bump when the extraction prompt/schema changes

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
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
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func usage() {
	fmt.Println(`tomobit — a living harness that grows with you

usage:
  tomobit do       [--cap implement] [--timeout 0] [--permission-mode <mode>]
                   [--model qwen3:8b] [--url http://localhost:11434] "<prompt>"
  tomobit record   --session <id> --type <event.type> [--json '{...}']
  tomobit perceive [--model qwen3:8b] [--url http://localhost:11434]
  tomobit rebuild
  tomobit status

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
	permMode := fs.String("permission-mode", "", "claude --permission-mode passthrough")
	model := fs.String("model", "qwen3:8b", "ollama model for best-effort perception")
	url := fs.String("url", "", "ollama base url (default http://localhost:11434)")
	fs.Parse(args)

	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		return fmt.Errorf("do: a prompt is required")
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
	ex := &executor.Executor{Adapter: claudecode.New(), Stderr: os.Stderr, Warn: os.Stderr}
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
	return nil
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
	en := &core.Engine{Repo: s}
	for _, e := range exps {
		if err := en.Apply(e); err != nil {
			fmt.Printf("perceived but projection is stale — run `tomobit rebuild`: %v\n", err)
			return
		}
		fmt.Printf("perceived %s: %s %s → %s\n",
			e.SessionID, e.Kind, core.NewScope(e.Tokens()...).Key(), e.Target())
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
		en := &core.Engine{Repo: s}
		for _, e := range exps {
			if applyErr := en.Apply(e); applyErr != nil {
				return fmt.Errorf("apply %s: %w (experiences are saved; the projection is stale — run `tomobit rebuild` to repair)", e.ID, applyErr)
			}
			fmt.Printf("perceived %s: %s %s → %s\n",
				e.SessionID, e.Kind, core.NewScope(e.Tokens()...).Key(), e.Target())
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
	if len(conns) == 0 {
		fmt.Println("no connections yet — record a session and run `tomobit perceive`")
		return nil
	}
	en := &core.Engine{Repo: s}
	now := time.Now().UnixMilli()
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "KIND\tSCOPE\tTARGET\tSTRENGTH\tCONF\tEVIDENCE\tSTATE")
	for _, c := range conns {
		sum, err := en.LedgerSum(c, now)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%.2f\t%.2f\t%.1f\t%s\n",
			c.Kind, c.ScopeKey, c.Target,
			c.Mean(now), c.Confidence(now), c.Evidence(now),
			c.State(now, sum))
	}
	return w.Flush()
}
