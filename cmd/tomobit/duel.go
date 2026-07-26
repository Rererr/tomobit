package main

// A/B duel: an experiment, not orchestration (ADR-0026). When an open
// Preference Gap covers the task's capability, Tomo offers to run the gap's two
// providers on the same prompt and let the user judge — turning "which do you
// prefer?" (a hypothetical) into "let me try both and find out". The parallel
// run exists only to ground a preference in real head-to-head work (VISION:
// "It compares. It experiments."), never to go faster.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/curiosity"
	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/perceive"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/voice"
	"github.com/Rererr/tomobit/internal/workspace"
)

// duelEligible reports whether a `do` may offer an A/B experiment (ADR-0026
// Decision 2). The offer belongs to the unpinned/auto path: an explicit
// --provider (or human) means the user took the wheel, so Tomo has no room to
// propose a pair. The default provider counts as unpinned — a bare
// `tomobit do "…"` is the common path the offer is meant to reach. The split
// protocol no longer gates this (ADR-0028 made it always-on and so unknowable
// before the run): a declined duel just falls through to a normal run that
// carries the protocol.
func duelEligible(providerExplicit bool, providerName string) bool {
	if providerName == "human" {
		return false
	}
	return !providerExplicit || providerName == "auto"
}

// pickDuelGap returns the highest-VoI open gap whose scope is knowable before
// the task runs (a subset of the pre-run tokens — realistically the capability
// alone, since lang/framework are only perceived after the work) and whose
// pair is two launchable providers — registry ∩ PATH, the same ADR-0043
// Decision 2 predicate auto's candidate list uses, because a duel is a real
// run: a side whose binary is missing can only "lose" by never starting, and
// a forfeit must never become a preference. The registry-only check would
// re-admit that on any machine whose ledger knows a provider it no longer
// has installed (the registry stays static by design). A gap may also pair a
// provider with "human" (ADR-0018 — human runs on the same ledger), but a
// human has no stream to run in a goroutine; human is never registered, so
// the same predicate excludes it. Gaps arrive VoI-sorted (curiosity.Gaps),
// so the first match is the one most worth settling by experiment.
func pickDuelGap(gaps []curiosity.Gap, tokens []string) (curiosity.Gap, bool) {
	for _, g := range gaps {
		if !g.Scope.SubsetOf(tokens) {
			continue
		}
		if launchableProvider(g.A, executableOnPath) && launchableProvider(g.B, executableOnPath) {
			return g, true
		}
	}
	return curiosity.Gap{}, false
}

// duelBudgetOK gates the offer on the curiosity budget (ADR-0026 Decision 2:
// the same window as the question). An offer is expensive — two provider runs —
// so it may not interrupt more often than an ordinary tomo.asked, and each
// event type in the trailing window blocks the next: a question just asked, or
// a duel just offered, both mean "don't propose again yet".
func duelBudgetOK(s *store.Store, now int64) (bool, error) {
	for _, typ := range []string{"tomo.asked", "tomo.duel_offered"} {
		ts, found, err := s.LastEventTS(typ)
		if err != nil {
			return false, err
		}
		if found && now-ts < curiosity.BudgetWindowMs {
			return false, nil
		}
	}
	return true, nil
}

// duelOffer decides whether to run an A/B experiment for this task (ADR-0026
// Decision 2). It fires only interactively, within budget, when a runnable
// pre-run gap exists. Whatever the answer, the offer is recorded (a declined
// offer still spends the budget — ADR-0007 Decision 3): the record both rate-
// limits future offers and lets the face know Tomo asked.
//
// tp is the task's Task Perception holder (ADR-0036 Decision 2b) — duelOffer
// runs before task.started is even recorded, so it is often the first of the
// three decision paths to ask, which is exactly why the holder exists rather
// than being owned by autoDecide alone.
func duelOffer(s *store.Store, capability, size string, in *bufio.Reader, out io.Writer, interactive bool, now int64, tp *taskPerception) (curiosity.Gap, bool) {
	if !interactive {
		return curiosity.Gap{}, false
	}
	ok, err := duelBudgetOK(s, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "duel: budget check failed:", err)
		return curiosity.Gap{}, false
	}
	if !ok {
		return curiosity.Gap{}, false
	}
	gaps, err := curiosity.Gaps(s, now)
	if err != nil {
		fmt.Fprintln(os.Stderr, "duel: gap derivation failed:", err)
		return curiosity.Gap{}, false
	}
	var semantic []string
	if tp != nil {
		semantic = tp.semanticTokens(out)
	}
	gap, found := pickDuelGap(gaps, perceptionTokens(capability, size, semantic))
	if !found {
		return curiosity.Gap{}, false
	}

	fmt.Fprintln(out) // speaker separation, like the curiosity question
	accepted := askDuelYN(in, out, gap)
	if err := recordDuelOffer(s, gap, accepted, now); err != nil {
		fmt.Fprintln(os.Stderr, "duel: recording offer failed:", err)
	}
	return gap, accepted
}

// askDuelYN prints the offer and reads a Y/n answer. The default is no
// (Enter/EOF): an experiment that doubles the cost must be opted into, never
// fallen into. Only an explicit y/yes runs the duel.
func askDuelYN(in *bufio.Reader, out io.Writer, gap curiosity.Gap) bool {
	fmt.Fprintf(out, "「%s」（2本分のコストがかかる）[y/N] ", voice.DuelOffer(gap.Scope, gap.A, gap.B))
	line, _ := in.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// recordDuelOffer writes the offer to its own session (like curiosity's ask
// session): it holds no task.finished, so deferred perception skips it. The
// event spends the duel budget whether accepted or not.
func recordDuelOffer(s *store.Store, gap curiosity.Gap, accepted bool, now int64) error {
	offerSID := store.NewID(now)
	return s.AppendEvent(offerSID, "tomo.duel_offered", now, map[string]any{
		"scope":    []string(gap.Scope),
		"pair":     []string{gap.A, gap.B},
		"accepted": accepted,
	})
}

// runDuel runs the gap's two providers on the same prompt concurrently and
// records both as sibling task sessions under one duel parent (ADR-0026
// Decision 4). Unlike a split (ADR-0023, sequential, fail-stops-the-rest), the
// siblings run at once and both run to completion even if one fails: the point
// is to compare two outcomes, so neither is abandoned for the other. The store
// serializes the two goroutines' writes on its single connection (seq is
// per-session), so only the shared terminal needs a mutex.
//
// Phase 2 (ADR-0026 Decision 3) will present both results here and record the
// user's preference; for now each sibling becomes an ordinary execution
// experience and the parent closes quietly.
func runDuel(ctx context.Context, s *store.Store, gap curiosity.Gap, prompt, capability, size string, permMode executor.Permission, timeout time.Duration, in *bufio.Reader, out io.Writer, extractor perceive.Extractor) error {
	now := time.Now().UnixMilli()
	parentSID := store.NewID(now)
	if err := s.AppendEvent(parentSID, "task.started", now,
		map[string]any{"intent": prompt, "source": "production"}); err != nil {
		return err
	}
	if err := s.AppendEvent(parentSID, "capability.started", now,
		map[string]any{"capability": capability}); err != nil {
		return err
	}
	if err := s.AppendEvent(parentSID, "task.duel", now,
		map[string]any{"pair": []string{gap.A, gap.B}, "scope": []string(gap.Scope)}); err != nil {
		return err
	}
	fmt.Fprintf(out, "duel: %s と %s を並走\n", gap.A, gap.B)

	pair := [2]string{gap.A, gap.B}
	childSID := [2]string{}
	adapter := [2]executor.Adapter{}
	for i, p := range pair {
		// tp: nil — a duel's providerName is always one concrete side of the
		// gap (never "auto"), so openSubtask's autoDecide branch is
		// structurally unreachable here; there is nothing to hand it.
		sid, a, _, err := openSubtask(s, out, p, capability, size, prompt, parentSID, nil)
		if err != nil {
			return err
		}
		childSID[i], adapter[i] = sid, a
	}

	// Each child gets its own workspace (ADR-0050 Decision 3). A duel is the
	// one place where a shared tree is most corrosive: the same prompt runs
	// twice at once, so each side can see — and overwrite — the other's work,
	// and an experiment whose two arms edit one checkout is not an experiment.
	// Splitting them costs nothing here because the two arms never depend on
	// each other, which is exactly why split subtasks are NOT given separate
	// workspaces.
	//
	// The prompts stay isomorphic in the sense ADR-0026 cares about: the task
	// text is identical, and only the harness's own wiring (a path per child)
	// differs.
	isolate := isolateProtocolEnabled()
	childPrompt := [2]string{prompt, prompt}
	isoParent := [2]string{}
	if isolate {
		for i := range pair {
			p, child, err := isolationDir(childSID[i])
			if err != nil {
				fmt.Fprintln(os.Stderr, "workspace: 隔離先を用意できないので隔離せずに走る:", err)
				isolate = false
				break
			}
			isoParent[i] = p
			childPrompt[i] = workspace.Instruction(prompt, child)
		}
	}

	var mu sync.Mutex // guards the shared terminal, not the store
	var wg sync.WaitGroup
	result := [2]executor.Result{}
	runErr := [2]error{}
	texts := [2][]string{}
	for i := range pair {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ex := &executor.Executor{Adapter: adapter[i], Stderr: os.Stderr, Warn: os.Stderr}
			if os.Getenv("TOMOBIT_DEBUG") != "" {
				ex.Debug = os.Stderr
			}
			req := executor.Request{
				Prompt: childPrompt[i], PermissionMode: permMode, Timeout: timeout,
			}
			var collect *[]string
			if isolate {
				req.AddDirs = append(req.AddDirs, isoParent[i])
				collect = &texts[i]
			}
			result[i], runErr[i] = ex.Run(ctx, req,
				duelSink(s, childSID[i], out, &mu, pair[i], collect))
		}(i)
	}
	wg.Wait()

	if isolate {
		for i := range pair {
			if result[i].Started {
				recordWorkspace(s, childSID[i], texts[i])
			}
		}
	}

	// SIGINT hit both children; record the cancellation on each started child
	// and the parent, and skip the (future) judgment — there is nothing to
	// compare. A side that never launched gets no task.cancelled either: a
	// cancelled boundary still enters PendingSessions and would be perceived
	// into an experience, while a launch that never happened has nothing for
	// perception to read (ADR-0043 Decision 3).
	if ctx.Err() != nil {
		for i, sid := range childSID {
			if !result[i].Started {
				continue
			}
			if err := s.AppendEvent(sid, "task.cancelled", time.Now().UnixMilli(), nil); err != nil {
				return err
			}
		}
		return s.AppendEvent(parentSID, "task.cancelled", time.Now().UnixMilli(), nil)
	}

	bothStarted, bothProduced := true, true
	for i, sid := range childSID {
		// A side that never launched (missing binary, spawn failure) leaves no
		// provider.error and no task boundary — the same non-evidence shape
		// cmdDo gives a never-started run (ADR-0043 Decision 3). pickDuelGap
		// should have kept such a side out; this catches the launch that fails
		// anyway (PATH raced, spawn error), so the guarantee below — never by
		// forfeit — holds even then. The failure is printed, not swallowed.
		if !result[i].Started {
			bothStarted, bothProduced = false, false
			fmt.Fprintf(out, "duel: %s は起動できなかった（この側は経験に残さない）: %v\n", pair[i], runErr[i])
			continue
		}
		if payload, need := providerErrorPayload(runErr[i], result[i]); need {
			bothProduced = false
			if err := s.AppendEvent(sid, "provider.error", time.Now().UnixMilli(), payload); err != nil {
				return err
			}
		}
		// No per-child adoption question: the pairwise judgment below is the
		// duel's one adoption. An empty task.finished still makes the session
		// perceivable (store.PendingSessions).
		if err := s.AppendEvent(sid, "task.finished", time.Now().UnixMilli(), map[string]any{}); err != nil {
			return err
		}
	}

	// Judgment (ADR-0026 Decision 3): the user compares the two real outputs and
	// their verdict becomes a preference experience at the gap scope — the duel
	// pays off by grounding a preference in work, not a hypothetical. It runs
	// only when both sides produced something to compare: a failed side is
	// judged by its own execution experience (its capability drops), never by
	// forfeit. A draw (Enter) records nothing.
	if !bothStarted {
		fmt.Fprintln(out, "duel: 片方が起動できなかったので好みは記録しない（不戦勝は好みにならない）")
	} else if !bothProduced {
		fmt.Fprintln(out, "duel: 片方が完走しなかったので好みは記録しない（両者は経験に残る）")
	} else if in != nil {
		if preferred, over, judged := duelVerdict(in, out, gap); judged {
			en := &core.Engine{Repo: s}
			if err := recordDuelVerdict(s, en, gap, preferred, over, time.Now().UnixMilli()); err != nil {
				return err
			}
		}
	}

	if err := s.AppendEvent(parentSID, "task.finished", time.Now().UnixMilli(), map[string]any{}); err != nil {
		return err
	}
	perceiveBestEffort(s, os.Stdout, extractor)
	return nil
}

// duelVerdict asks which of the two real outputs the user preferred (ADR-0026
// Decision 3). 1/2 name the winner; Enter (or anything else) is a draw that
// records nothing — the same skip-is-cheap shape as the curiosity question.
func duelVerdict(in *bufio.Reader, out io.Writer, gap curiosity.Gap) (preferred, over string, judged bool) {
	fmt.Fprintf(out, "\nどっちが good だった? [1=%s / 2=%s / Enter=引き分け] ", gap.A, gap.B)
	line, _ := in.ReadString('\n')
	switch strings.TrimSpace(line) {
	case "1":
		return gap.A, gap.B, true
	case "2":
		return gap.B, gap.A, true
	default:
		return "", "", false
	}
}

// recordDuelVerdict turns the verdict into a preference experience the same way
// the curiosity question does (ADR-0007 Decision 4), but grounded in real work
// rather than a hypothetical: its own session (no task.finished, so deferred
// perception skips it), context fixed to the gap scope, applied straight into
// the preference ledger the next decide.Choose will read.
func recordDuelVerdict(s *store.Store, en *core.Engine, gap curiosity.Gap, preferred, over string, now int64) error {
	verdictSID := store.NewID(now)
	if err := s.AppendEvent(verdictSID, "user.preference", now, map[string]any{
		"preferred": preferred, "over": over, "source": "duel",
	}); err != nil {
		return fmt.Errorf("record duel preference: %w", err)
	}
	exp := &core.Experience{
		ID: store.NewID(now), SessionID: verdictSID, TS: now,
		Kind: core.KindPreference, ExtractorVer: extractorVer,
		ExtractorModel: "deterministic",
		Context:        scopeToContext(gap.Scope),
		Outcome:        core.Outcome{Preferred: preferred, Over: over},
		Source:         "learning",
	}
	if err := s.InsertExperiences([]*core.Experience{exp}); err != nil {
		return fmt.Errorf("insert duel preference: %w", err)
	}
	if err := en.Apply(exp); err != nil {
		return fmt.Errorf("apply duel preference: %w (experience saved; `tomobit rebuild` repairs the projection)", err)
	}
	// This experience is its own batch (ADR-0037 Decision 2): reconcile at
	// its boundary so merge judgment reaches children this Apply didn't
	// touch, the same reach Rebuild's closing sweep already has.
	if err := en.ReconcileMerges(now); err != nil {
		return fmt.Errorf("reconcile merges: %w (experience saved; `tomobit rebuild` repairs the projection)", err)
	}
	return nil
}

// scopeToContext parses a gap scope's "k=v" tokens back into a context map, so
// the preference experience inherits exactly the gap's scope (mirrors
// curiosity's own scopeContext).
func scopeToContext(scope core.Scope) map[string]string {
	ctx := map[string]string{}
	for _, tok := range scope {
		if k, v, ok := strings.Cut(tok, "="); ok && k != "" && v != "" {
			ctx[k] = v
		}
	}
	return ctx
}

// duelSink records one child's stream to its own session and echoes its text to
// the shared terminal, prefixed with the provider name so two interleaved
// streams stay readable (ADR-0026 Decision 4). The store side is unsynchronized
// on purpose — its single connection already serializes — so the mutex guards
// only the io.Writer.
// collect, when non-nil, gathers this child's assistant text for the workspace
// declaration (ADR-0050). It is owned by the one goroutine driving this child,
// so the shared mutex above it guards only the terminal — appending here needs
// no lock of its own.
func duelSink(s *store.Store, sid string, out io.Writer, mu *sync.Mutex, label string, collect *[]string) executor.Sink {
	return func(ev executor.Event, ts int64) error {
		if ev.Type == executor.EventProviderOutput {
			if text, ok := ev.Payload["text"].(string); ok && text != "" {
				mu.Lock()
				fmt.Fprintf(out, "[%s] %s\n", label, text)
				mu.Unlock()
				if collect != nil {
					*collect = append(*collect, text)
				}
			}
		}
		return recordEvent(s, sid, ev, ts)
	}
}
