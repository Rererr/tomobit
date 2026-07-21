package main

// Split parallelism (ADR-0028 Phase 2): a Provider may declare that some
// subtasks are independent (a group wider than one — ADR-0028 Decision 2), and
// when it does, tomobit offers to run that group's members at once. The offer
// is the one place a human is asked (Decision 3): parallelism front-loads K
// provider runs and loses the sequential split's fail-stop wallet safety valve
// (ADR-0023 Decision 4), so it is opted into, never fallen into. A no keeps the
// whole sequence sequential — the work was asked for; only the parallelism
// needs a yes. The run mechanism is the duel's (Decision 4): goroutine +
// WaitGroup, the store's single connection serializes the writes, so only the
// shared terminal needs a mutex.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/subtask"
)

// parallelWidthCap bounds how many of a group's provider runs launch at once
// (ADR-0028 実装時ノブ, initial 3): subtask.Max=5 means a group could declare
// five independent members, and five real provider CLIs at once is not a load
// to reach for before it is measured. The group still runs to completion — the
// cap throttles concurrency, not membership.
const parallelWidthCap = 3

// costSampleLimit bounds how many recent cost_usd-bearing provider.finished
// events feed the gate's median (ADR-0028 実装時ノブ, initial 20).
const costSampleLimit = 20

// parallelWidth is the number of subtasks that will actually run in parallel:
// the sum of the wide groups' sizes. A lone group runs sequentially and is not
// part of the parallel commitment, so it does not count. This is the multiplier
// the cost estimate and the gate line both use.
func parallelWidth(groups [][]string) int {
	w := 0
	for _, g := range groups {
		if len(g) > 1 {
			w += len(g)
		}
	}
	return w
}

// parallelCostEstimate multiplies the median of the most recent real provider
// costs by the parallel width (ADR-0028 Decision 3). cost_usd rides only on
// claude-code's provider.finished (codex does not report it), so an empty
// sample means no honest number exists: it returns ok=false rather than
// fabricate one — the gate then says "概算なし" instead of a made-up dollar
// figure. Best-effort: a store read failure is logged and treated as no sample,
// never fatal to the run.
func parallelCostEstimate(s *store.Store, groups [][]string) (usd float64, ok bool) {
	costs, err := s.RecentProviderCosts(costSampleLimit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "split: cost estimate skipped:", err)
		return 0, false
	}
	if len(costs) == 0 {
		return 0, false
	}
	return median(costs) * float64(parallelWidth(groups)), true
}

// median returns the middle value of xs (the mean of the two middle values for
// an even count). It copies before sorting so the caller's slice order — the
// newest-first cost sample — is left intact.
func median(xs []float64) float64 {
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// parallelGate asks, once, whether to run the declared wide groups in parallel
// (ADR-0028 Decision 3). It fires only when the Provider actually declared a
// wide group and a human is watching: a pipe or CI (non-interactive) never sees
// it, so those stay sequential and deterministic. The default is N (Enter/EOF);
// only an explicit y/yes accepts. This is a machine line (ADR-0009), not Tomo's
// voice — a work confirmation, not a question — so it records nothing and spends
// no curiosity budget. subs (the flat order) and est/haveEst are the ones the
// caller already computed, so the gate's presentation and the task.split record
// cannot drift apart.
func parallelGate(groups [][]string, subs []string, in *bufio.Reader, out io.Writer, interactive bool, est float64, haveEst bool) (offered, accepted bool) {
	if !interactive || !declaresGroups(groups) {
		return false, false
	}
	width := parallelWidth(groups)
	cost := "（概算なし — 費用の実測がまだない）"
	if haveEst {
		cost = fmt.Sprintf("（概算 $%.2f = 直近実測の中央値 × %d本）", est, width)
	}
	fmt.Fprintf(out, "split: %d個を%d群で実行。独立宣言された%d本を並走できる — 並走する?\n%s [y/N] ",
		len(subs), len(groups), width, cost)
	line, _ := in.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, true
	default:
		return true, false
	}
}

// recordSubtaskOutcome writes a finished subtask's objective signals (ADR-0028
// Decision 5): a provider.error when it failed, then an empty task.finished so
// the session is perceivable (store.PendingSessions) without a subjective
// Feedback question. It reports whether the subtask failed, so the caller can
// enforce the fail-stop between groups. Shared by the sequential and parallel
// paths, so a child is recorded the same way regardless of how it was scheduled.
func recordSubtaskOutcome(s *store.Store, sid string, runErr error, result executor.Result, ts int64) (failed bool, err error) {
	if payload, need := providerErrorPayload(runErr, result); need {
		if err := s.AppendEvent(sid, "provider.error", ts, payload); err != nil {
			return false, err
		}
	}
	if err := s.AppendEvent(sid, "task.finished", ts, map[string]any{}); err != nil {
		return false, err
	}
	return runErr != nil || result.ExitCode != 0, nil
}

// cancelChildrenAndParent records task.cancelled on every opened child and the
// parent (ADR-0028 Decision 4 / duel's shape): SIGINT already reached the
// children, so the ledger keeps their cancellation and skips the closing
// Feedback — the sessions stay pending for a later `tomobit perceive`.
func cancelChildrenAndParent(s *store.Store, parentSID string, childSIDs []string) error {
	for _, sid := range childSIDs {
		if err := s.AppendEvent(sid, "task.cancelled", time.Now().UnixMilli(), nil); err != nil {
			return err
		}
	}
	return s.AppendEvent(parentSID, "task.cancelled", time.Now().UnixMilli(), nil)
}

// readSplitProposal is the one place `do` (cmdDo) and chat (chat.run) turn a
// clean opening run's collected provider text into subtask groups — the
// single responsibility of "how tomobit reads a Provider's split proposal"
// (ADR-0023 Decision 1: a malformed marker warns and falls through, it never
// silently drops or silently rescues). The two callers keep their own gating
// (splitProtocolEligible / splitProtocolEnabled, runErr/ExitCode) and their
// own divergent reaction to an accepted proposal — do finishes the task
// differently from chat's fold-back (ADR-0028 Decision 5) — because reading
// the proposal and acting on it are different responsibilities; only the
// reading was duplicated.
func readSplitProposal(texts []string) [][]string {
	groups, parseErr := subtask.Parse(strings.Join(texts, "\n"))
	if parseErr != nil {
		fmt.Fprintln(os.Stderr, "split: proposal ignored —", parseErr)
		return nil
	}
	return groups
}

// executeSplit records the accepted proposal and runs its subtasks — the shared
// core of the do path (runSplit) and the chat path (chat.splitAndFold). It runs the
// gate once (only a wide group fires it — Decision 3), records task.split with
// the SCHEMA.md R4 payload, and executes the subtasks (accepted → group-by-group
// parallel, declined or non-interactive → the flat sequential fail-stop). It
// returns the flat subtask list and whether a SIGINT cancelled the run (children
// and parent already hold task.cancelled — the caller then skips its own boundary
// work). What happens after is the caller's: do finishes the parent task, chat
// folds the subtask results back into the thread (Decision 5). Splitting the tail
// out of runSplit is what keeps the do path's behavior byte-for-byte while chat
// reuses the same execution — no do-only regression.
//
// newView, when non-nil, is a chat's NDJSON view stream reaching down into the
// sequential path only (ADR-0032 Decision 1): a subtask's Provider output must
// land in the same typed text/tool/tool_result vocabulary the parent turn uses,
// not providerSink's raw echo. do and a plain/TTY chat pass nil and see the
// unchanged echo. It never reaches runGroups/runGroupParallel — the gate that
// leads there requires interactive, which a view session structurally never is
// (validateViewFlag forces stdout non-TTY), so there is nothing to wire there.
//
// tp is the parent task's Task Perception holder, handed straight through to
// every subtask opened below (ADR-0036 Decision 2b: a split child reads the
// parent's tokens, never re-perceives).
func executeSplit(ctx context.Context, s *store.Store, parentSID string, groups [][]string,
	parentIntent, providerName, capability, size, permMode string, timeout time.Duration,
	in *bufio.Reader, out io.Writer, interactive bool, newView func(string) view, tp *taskPerception) (subs []string, cancelled bool, err error) {
	subs, idxGroups := flattenGroups(groups)
	now := time.Now().UnixMilli()

	// The cost estimate is computed once, so the number shown at the gate is the
	// same one recorded on task.split (Decision 1's metric reads it). It is
	// gated on interactive too: the gate only ever fires when a human is
	// watching, so a pipe/CI run skips the RecentProviderCosts read it could
	// never use — keeping the deterministic non-TTY path free of a wasted query.
	wide := declaresGroups(groups)
	est, haveEst := 0.0, false
	if wide && interactive {
		est, haveEst = parallelCostEstimate(s, groups)
	}
	offered, accepted := parallelGate(groups, subs, in, out, interactive, est, haveEst)

	payload := map[string]any{"subtasks": subs}
	// A flat proposal records subtasks alone — SCHEMA.md R4 omits "groups 以降"
	// (groups and the gate fields) when no independence was declared, since a
	// flat proposal's index groups would be [[0],[1],…] and its gate never fires.
	// A wide proposal records the index groups (the audit signal that
	// parallelism was declared — Decision 1's metric reads it), the gate's
	// offer/answer (whether it was even offered, so non-TTY reads as offered=
	// false), and est_cost_usd when a real cost was measured (omitted otherwise —
	// Decision 3 refuses to fabricate one).
	if wide {
		payload["groups"] = idxGroups
		payload["parallel_offered"] = offered
		payload["parallel_accepted"] = accepted
		if haveEst {
			payload["est_cost_usd"] = est
		}
	}
	if err := s.AppendEvent(parentSID, "task.split", now, payload); err != nil {
		return nil, false, err
	}
	fmt.Fprintf(out, "split: %d個のサブタスクとして実行\n", len(subs))

	if accepted {
		cancelled, err = runGroups(ctx, s, parentSID, groups, subs, parentIntent,
			providerName, capability, size, permMode, timeout, in, out, tp)
	} else {
		cancelled, err = runSubtasksSequential(ctx, s, parentSID, subs, parentIntent,
			providerName, capability, size, permMode, timeout, in, out, newView, tp)
	}
	return subs, cancelled, err
}

// runGroups runs an accepted proposal group by group (ADR-0028 Decision 4):
// groups run in proposal order, a wide group's members run in parallel, and a
// failure anywhere in a group stops the next group from starting — a later
// group may depend on this one's result (ADR-0023 Decision 4 preserved between
// groups). A lone group is a single sequential run. cancelled reports a SIGINT,
// after which the children and parent already hold task.cancelled and the
// caller skips finishTask.
func runGroups(ctx context.Context, s *store.Store, parentSID string, groups [][]string, subs []string,
	parentIntent, providerName, capability, size, permMode string, timeout time.Duration,
	in *bufio.Reader, out io.Writer, tp *taskPerception) (cancelled bool, err error) {
	var mu sync.Mutex // guards the shared terminal, not the store (its single connection serializes writes)
	base := 0
	for gi, g := range groups {
		if len(g) > 1 {
			fmt.Fprintf(out, "-- group %d/%d: %d本を並走 --\n", gi+1, len(groups), len(g))
			failed, canc, err := runGroupParallel(ctx, s, parentSID, g, base, len(subs),
				providerName, capability, size, parentIntent, permMode, timeout, in, out, &mu, tp)
			base += len(g)
			if err != nil {
				return false, err
			}
			if canc {
				return true, nil
			}
			if failed {
				fmt.Fprintf(out, "split: 群%d内に失敗 — 残りの群は開始しない\n", gi+1)
				return false, nil
			}
		} else {
			fmt.Fprintf(out, "-- subtask %d/%d: %s --\n", base+1, len(subs), truncate(g[0], 60))
			// runGroups only runs once the parallel gate accepted (Decision 3),
			// which requires interactive — and a chat's NDJSON view stream forces
			// non-interactive (ADR-0032 Decision 1: view mode assumes every TTY
			// gate shut). A view is therefore never available on this path; nil
			// is that structural fact, not a placeholder for future wiring.
			failed, canc, err := runSubtaskSequential(ctx, s, parentSID, g[0], base, len(subs),
				providerName, capability, size, parentIntent, permMode, timeout, in, out, nil, tp)
			base++
			if err != nil {
				return false, err
			}
			if canc {
				return true, nil
			}
			if failed {
				fmt.Fprintf(out, "split: subtask %d/%d failed — remaining subtasks not started\n", base, len(subs))
				return false, nil
			}
		}
	}
	return false, nil
}

// runSubtasksSequential runs the flat proposal order one subtask at a time — the
// path a declined gate (or a non-interactive run) takes (ADR-0028 Decision 3:
// "n でも作業は失われない"). It is exactly the ADR-0023 fail-stop: a failed
// subtask stops the loop before the next one opens, so a task that never started
// leaves no half-run in the ledger.
func runSubtasksSequential(ctx context.Context, s *store.Store, parentSID string, subs []string,
	parentIntent, providerName, capability, size, permMode string, timeout time.Duration,
	in *bufio.Reader, out io.Writer, newView func(string) view, tp *taskPerception) (cancelled bool, err error) {
	for i, sub := range subs {
		fmt.Fprintf(out, "-- subtask %d/%d: %s --\n", i+1, len(subs), truncate(sub, 60))
		failed, canc, err := runSubtaskSequential(ctx, s, parentSID, sub, i, len(subs),
			providerName, capability, size, parentIntent, permMode, timeout, in, out, newView, tp)
		if err != nil {
			return false, err
		}
		if canc {
			return true, nil
		}
		if failed {
			fmt.Fprintf(out, "split: subtask %d/%d failed — remaining subtasks not started\n", i+1, len(subs))
			return false, nil
		}
	}
	return false, nil
}

// runSubtaskSequential opens one subtask, runs it (provider stream or human),
// and records its objective outcome (ADR-0028 Decision 5). Its provider text
// reaches the terminal unlabeled — a sequential run has no interleaving to
// disambiguate, unlike the parallel [n:provider] streams. gi is the subtask's
// zero-based position in the flat order (for its 1/total prompt framing).
// Reports failed (for the caller's fail-stop) and cancelled (SIGINT — child and
// parent cancellation already recorded).
//
// newView follows executeSplit's own doc: nil (do, plain/TTY chat) keeps
// providerSink's raw echo; a chat's NDJSON view stream builds one fresh view
// per subtask (its provider can differ from the parent's own, under auto) and
// drives begin/show/end around the run exactly as an ordinary turn does.
func runSubtaskSequential(ctx context.Context, s *store.Store, parentSID, sub string, gi, total int,
	providerName, capability, size, parentIntent, permMode string, timeout time.Duration,
	in *bufio.Reader, out io.Writer, newView func(string) view, tp *taskPerception) (failed, cancelled bool, err error) {
	sid, adapter, human, err := openSubtask(s, out, providerName, capability, size, sub, parentSID, tp)
	if err != nil {
		return false, false, err
	}
	prompt := subtask.Prompt(parentIntent, sub, gi, total)

	var result executor.Result
	var runErr error
	if human {
		fmt.Fprintln(out, prompt)
		result, runErr = runHuman(s, out, sid, in)
		if runErr != nil {
			return false, false, runErr
		}
	} else {
		var v view
		if newView != nil {
			v = newView(adapter.Name())
			v.begin()
		}
		sink := subtaskSink(s, sid, out, v)
		ex := &executor.Executor{Adapter: adapter, Stderr: os.Stderr, Warn: os.Stderr}
		if os.Getenv("TOMOBIT_DEBUG") != "" {
			ex.Debug = os.Stderr
		}
		result, runErr = ex.Run(ctx, executor.Request{
			Prompt: prompt, PermissionMode: permMode, Timeout: timeout,
		}, sink)
		if v != nil {
			v.end(result)
		}
	}

	if ctx.Err() != nil {
		if err := cancelChildrenAndParent(s, parentSID, []string{sid}); err != nil {
			return false, false, err
		}
		return false, true, nil
	}

	failed, err = recordSubtaskOutcome(s, sid, runErr, result, time.Now().UnixMilli())
	return failed, false, err
}

// subtaskSink builds the Sink one sequential subtask run writes through. v nil
// (do, plain/TTY chat) is providerSink's raw text echo, unchanged. A non-nil v
// is a chat's NDJSON view stream (ADR-0032 Decision 1): echoing the subtask's
// raw text through the note framer there would collapse a structured Provider
// turn into an opaque "note", asking the GUI to parse it back out — v.show(ev)
// instead classifies the event into the same text/tool/tool_result vocabulary
// the parent turn already emits, so a subtask reads as a nested turn frame
// rather than a wall of chat annotation.
func subtaskSink(s *store.Store, sid string, out io.Writer, v view) executor.Sink {
	if v == nil {
		return providerSink(s, sid, out, nil)
	}
	return func(ev executor.Event, ts int64) error {
		v.show(ev)
		return recordEvent(s, sid, ev, ts)
	}
}

// runGroupParallel runs one independent group (ADR-0028 Decision 4). Its
// provider-backed members run at once — goroutine + WaitGroup, the duel
// mechanism — bounded by parallelWidthCap so a five-wide group never launches
// five CLIs at once. The group runs to completion: a neighbor's failure does not
// abandon the rest, because the Provider declared them independent (unlike a
// sequential split, where a failure stops the rest). Any auto-routed human
// members run sequentially after the parallel providers — the single terminal
// cannot host a human beside the streams, and the independence declaration makes
// the reorder legal (Decision 4). base is the group's first subtask's position
// in the flat order, so labels and prompts stay 1/total across the whole split.
// Returns failed (any member failed — the caller then stops before the next
// group) and cancelled (SIGINT — children and parent already hold task.cancelled).
func runGroupParallel(ctx context.Context, s *store.Store, parentSID string, group []string, base, total int,
	providerName, capability, size, parentIntent, permMode string, timeout time.Duration,
	in *bufio.Reader, out io.Writer, mu *sync.Mutex, tp *taskPerception) (failed, cancelled bool, err error) {
	type member struct {
		sid     string
		adapter executor.Adapter
		human   bool
		gi      int
		sub     string
	}
	members := make([]member, len(group))
	sids := make([]string, len(group))
	// This loop is sequential — every openSubtask call (and so every possible
	// tp.semanticTokens ask) happens before the goroutines below ever start —
	// so tp needs no locking of its own beyond sync.Once's.
	for k, sub := range group {
		sid, adapter, human, err := openSubtask(s, out, providerName, capability, size, sub, parentSID, tp)
		if err != nil {
			return false, false, err
		}
		members[k] = member{sid: sid, adapter: adapter, human: human, gi: base + k, sub: sub}
		sids[k] = sid
	}

	results := make([]executor.Result, len(members))
	runErrs := make([]error, len(members))

	var wg sync.WaitGroup
	sem := make(chan struct{}, parallelWidthCap)
	for k := range members {
		if members[k].human {
			continue // a human has no stream to run in a goroutine — handled below
		}
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			m := members[k]
			ex := &executor.Executor{Adapter: m.adapter, Stderr: os.Stderr, Warn: os.Stderr}
			if os.Getenv("TOMOBIT_DEBUG") != "" {
				ex.Debug = os.Stderr
			}
			results[k], runErrs[k] = ex.Run(ctx, executor.Request{
				Prompt: subtask.Prompt(parentIntent, m.sub, m.gi, total), PermissionMode: permMode, Timeout: timeout,
			}, splitSink(s, m.sid, out, mu, m.gi+1, m.adapter.Name()))
		}(k)
	}
	wg.Wait()

	if ctx.Err() != nil {
		if err := cancelChildrenAndParent(s, parentSID, sids); err != nil {
			return false, false, err
		}
		return false, true, nil
	}

	for k := range members {
		if !members[k].human {
			continue
		}
		fmt.Fprintln(out, subtask.Prompt(parentIntent, members[k].sub, members[k].gi, total))
		results[k], runErrs[k] = runHuman(s, out, members[k].sid, in)
		if runErrs[k] != nil {
			return false, false, runErrs[k]
		}
		if ctx.Err() != nil {
			if err := cancelChildrenAndParent(s, parentSID, sids); err != nil {
				return false, false, err
			}
			return false, true, nil
		}
	}

	for k := range members {
		f, err := recordSubtaskOutcome(s, members[k].sid, runErrs[k], results[k], time.Now().UnixMilli())
		if err != nil {
			return false, false, err
		}
		if f {
			failed = true
		}
	}
	return failed, false, nil
}

// splitSink records one parallel subtask's stream to its own session and echoes
// its text to the shared terminal with an [n:provider] prefix (ADR-0028
// Decision 4) — a form deliberately distinct from the duel's [provider], since
// parallel subtasks may share a provider (auto can pick the same winner twice)
// and the shape itself marks "work, not experiment". Like duelSink, the store
// side is unsynchronized on purpose — its single connection already serializes —
// so the mutex guards only the io.Writer.
func splitSink(s *store.Store, sid string, out io.Writer, mu *sync.Mutex, n int, provider string) executor.Sink {
	return func(ev executor.Event, ts int64) error {
		if ev.Type == executor.EventProviderOutput {
			if text, ok := ev.Payload["text"].(string); ok && text != "" {
				mu.Lock()
				fmt.Fprintf(out, "[%d:%s] %s\n", n, provider, text)
				mu.Unlock()
			}
		}
		return recordEvent(s, sid, ev, ts)
	}
}
