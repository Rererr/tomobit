package main

// Split parallelism (ADR-0028 Phase 2, ADR-0056): a Provider may declare that
// some subtasks are independent (a group wider than one — ADR-0028 Decision 2),
// and when it does, tomobit runs that group's members at once **without
// asking**. The y/N gate that used to guard this was retracted (ADR-0056
// Decision 1): it asked a person who had not read the subtask texts to
// second-guess one judgment of a Provider they had already trusted with the
// whole task. What survives is the number — parallelism front-loads K provider
// runs, so the group says its width and estimated cost before it starts
// (Decision 2), as a fact rather than a question.
//
// The run mechanism is the duel's (ADR-0028 Decision 4): goroutine + WaitGroup,
// the store's single connection serializes the ledger writes, so only the shared
// terminal needs a mutex — the NDJSON stream carries its own lock, which is what
// lets every member hold an open view frame at the same time.

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

// subtaskViewFactory builds one subtask's view (nil off the NDJSON stream).
// It takes the subtask's zero-based flat position and the split's total so the
// frame can name itself — the correlation a consumer needs once a group runs in
// parallel and K frames are open at the same time (ADR-0056 の宿題).
type subtaskViewFactory func(name string, gi, total int) view

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
func parallelWidth(groups [][]subtask.Element) int {
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
func parallelCostEstimate(s *store.Store, groups [][]subtask.Element) (usd float64, ok bool) {
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

// parallelNotice is the line a wide group prints before it starts (ADR-0056
// Decision 2). It says what is about to happen and what it is expected to cost
// — **it does not ask**.
//
// The question it replaced (ADR-0028 Decision 3's y/N gate) was retracted for
// two reasons: it asked a person who had not read the subtask texts and had no
// basis for judging independence, and it second-guessed exactly one judgment of
// a Provider the human had already trusted with the whole task. What survives
// is the number: a run that is about to spend K× at once should say so, the way
// GUI ADR-0009 decided to state a shared working place as a fact rather than a
// verdict.
//
// An empty cost sample yields no dollar figure at all rather than a made-up one
// (ADR-0028 Decision 3's 作り話の金額を出さない, unchanged).
func parallelNotice(members, gi, total int, est float64, haveEst bool) string {
	if haveEst {
		return fmt.Sprintf("-- group %d/%d: %d本を並走（概算 $%.2f = 直近実測の中央値 × 並走幅）--\n",
			gi, total, members, est)
	}
	return fmt.Sprintf("-- group %d/%d: %d本を並走 --\n", gi, total, members)
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
func readSplitProposal(texts []string) [][]subtask.Element {
	groups, parseErr := subtask.Parse(strings.Join(texts, "\n"))
	if parseErr != nil {
		fmt.Fprintln(os.Stderr, "split: proposal ignored —", parseErr)
		return nil
	}
	return groups
}

// executeSplit records the accepted proposal and runs its subtasks — the shared
// core of the do path (runSplit) and the chat path (chat.splitAndFold). It
// records task.split with the SCHEMA.md R4 payload and executes the subtasks
// (a declared independent group → group-by-group with that group parallel; a
// flat proposal, or the ADR-0056 kill switch → the flat sequential fail-stop).
// It returns the flat subtask list and whether a SIGINT cancelled the run
// (children and parent already hold task.cancelled — the caller then skips its
// own boundary work). What happens after is the caller's: do finishes the parent
// task, chat folds the subtask results back into the thread (ADR-0028 Decision
// 5). Splitting the tail out of runSplit is what keeps the do path's behavior
// byte-for-byte while chat reuses the same execution — no do-only regression.
//
// newView, when non-nil, is a chat's NDJSON view stream: a subtask's Provider
// output must land in the same typed text/tool/tool_result vocabulary the parent
// turn uses, not providerSink's raw echo. do and a plain/TTY chat pass nil and
// see the unchanged echo. **It reaches the parallel path too** — the older doc
// here said it never could, because the gate leading there required a terminal
// a view session structurally never has. ADR-0056 removed the gate, so the view
// entry is now the one where parallelism actually happens, and its frames
// correlate by sub.
//
// w carries everything the children share — see subtaskWiring.
func executeSplit(ctx context.Context, s *store.Store, parentSID string, groups [][]subtask.Element,
	parentIntent string, w subtaskWiring,
	in *bufio.Reader, out io.Writer, newView subtaskViewFactory) (subs []string, cancelled bool, err error) {
	elems, idxGroups := flattenGroups(groups)
	subs = instructions(elems)
	now := time.Now().UnixMilli()

	// 独立宣言された群は訊かずに並走する (ADR-0056 Decision 1)。走るかどうかが
	// 人の答えに依らなくなったので、費用の概算は「見せるために」ではなく
	// 「言うために」計算する — 並走が起きるときは常に一度だけ。
	wide := declaresGroups(groups) && parallelSubtasksEnabled()
	est, haveEst := 0.0, false
	if wide {
		est, haveEst = parallelCostEstimate(s, groups)
	}

	payload := map[string]any{"subtasks": subs}
	// A flat proposal records subtasks alone — SCHEMA.md R4 omits "groups 以降"
	// when no independence was declared, since a flat proposal's index groups
	// would be [[0],[1],…] and it never runs anything in parallel. A wide
	// proposal records the index groups (the audit signal that parallelism was
	// declared, and — since ADR-0056 — that it ran) and est_cost_usd when a real
	// cost was measured (omitted otherwise: no fabricated figure).
	//
	// parallel_offered / parallel_accepted are gone (ADR-0056 Decision 4): there
	// is no offer and no answer to record, and writing false forever would read
	// as "offered and declined". Old rows keep them; readers handle both.
	if wide {
		payload["groups"] = idxGroups
		if haveEst {
			payload["est_cost_usd"] = est
		}
	}
	if err := s.AppendEvent(parentSID, "task.split", now, payload); err != nil {
		return nil, false, err
	}
	fmt.Fprintf(out, "split: %d個のサブタスクとして実行\n", len(subs))

	if wide {
		cancelled, err = runGroups(ctx, s, parentSID, groups, len(elems), parentIntent, w, in, out, newView, est, haveEst)
	} else {
		cancelled, err = runSubtasksSequential(ctx, s, parentSID, elems, parentIntent, w, in, out, newView)
	}
	return subs, cancelled, err
}

// subtaskWiring is everything a split's children share, decided once for the
// parent (ADR-0054 Decisions 1 and 3). A split is one task's breakdown, so the
// relationship with a Provider is one — the adapter comes from the parent's own
// task boundary and every child runs on it — and the place is one, inherited
// from the parent's turn rather than defaulting to this process's cwd.
//
// It is a struct rather than five more parameters because it is a single idea:
// **the parent's decision**, threaded down. The functions below already carry a
// dozen arguments each, and splitting this one across them is what let the
// working place quietly go missing on the child path in the first place.
type subtaskWiring struct {
	// adapter is nil exactly when human is true.
	adapter    executor.Adapter
	human      bool
	capability string
	permMode   executor.Permission
	timeout    time.Duration
	// workDir / addDirs are the parent's working place (ADR-0047 / ADR-0054
	// Decision 3). An empty workDir keeps the process cwd, exactly as an
	// ordinary turn with no /cd does.
	workDir string
	addDirs []string
	// named is the executor this child was declared to run on (ADR-0060), empty
	// on every child that inherited. It exists to be recorded, not to be acted
	// on — adapter above already carries the routing — so a fallback leaves it
	// empty and the ledger never claims a declaration was honoured.
	named string
}

// forElement resolves the wiring one child runs on. A declared executor
// (ADR-0060 Decision 2) replaces the parent's adapter for that child alone;
// everything else inherits, which is the unchanged ADR-0054 Decision 1 default.
//
// It returns the line to show when a declaration was not honoured. Dropping one
// in silence would leave the Provider — and the person reading the output —
// believing work was routed somewhere it was not. Every fallback is per-child:
// a whole proposal is never thrown away over one name, because one typo is not
// worth four subtasks.
//
// Two refusals are deliberate. `human` cannot be named: ADR-0054 Decision 1
// keeps a breakdown from being handed back to a person piecemeal, and the way
// to give a person the work is to give them the task. And a human parent keeps
// its children whatever the element says — a person's split carries no
// Provider declaration to begin with, so one appearing there is not a routing
// tomobit should start honouring.
func (w subtaskWiring) forElement(e subtask.Element) (subtaskWiring, string) {
	if e.Provider == "" {
		return w, ""
	}
	const tail = "— 親の相手で走る"
	switch {
	case !namedExecutorEnabled():
		return w, fmt.Sprintf("split: 実行者の指名「%s」は使わない（config named_executor=false）%s", e.Provider, tail)
	case w.human:
		return w, fmt.Sprintf("split: 実行者の指名「%s」は使わない（このタスクは人が担っている）%s", e.Provider, tail)
	case e.Provider == "human":
		return w, fmt.Sprintf("split: 実行者に human は指名できない（内訳の一部だけを人へ差し戻さない）%s", tail)
	}
	a, err := resolveProvider(e.Provider)
	if err != nil {
		return w, fmt.Sprintf("split: 実行者の指名を解決できない（%v）%s", err, tail)
	}
	w.adapter = a
	w.named = e.Provider
	return w, ""
}

// request builds one child's launch. Every field but the prompt is the
// parent's, which is the whole point of the type.
func (w subtaskWiring) request(prompt string) executor.Request {
	return executor.Request{
		Prompt: prompt, PermissionMode: w.permMode, Timeout: w.timeout,
		WorkDir: w.workDir, AddDirs: w.addDirs,
	}
}

// runGroups runs an accepted proposal group by group (ADR-0028 Decision 4):
// groups run in proposal order, a wide group's members run in parallel, and a
// failure anywhere in a group stops the next group from starting — a later
// group may depend on this one's result (ADR-0023 Decision 4 preserved between
// groups). A lone group is a single sequential run. cancelled reports a SIGINT,
// after which the children and parent already hold task.cancelled and the
// caller skips finishTask.
func runGroups(ctx context.Context, s *store.Store, parentSID string, groups [][]subtask.Element, total int,
	parentIntent string, w subtaskWiring, in *bufio.Reader, out io.Writer,
	newView subtaskViewFactory, est float64, haveEst bool) (cancelled bool, err error) {
	var mu sync.Mutex // guards the shared terminal, not the store (its single connection serializes writes)
	base := 0
	for gi, g := range groups {
		if len(g) > 1 {
			// 訊かずに、始める前に言う (ADR-0056 Decision 2)。
			fmt.Fprint(out, parallelNotice(len(g), gi+1, len(groups), est, haveEst))
			failed, canc, err := runGroupParallel(ctx, s, parentSID, g, base, total,
				parentIntent, w, in, out, &mu, newView)
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
			fmt.Fprintf(out, "-- subtask %d/%d: %s --\n", base+1, total, truncate(g[0].Do, 60))
			// A lone group is one subtask, and it gets the same framed view its
			// flat-proposal twin does. The old comment here said a view was
			// "structurally never available" on this path because the gate
			// required a terminal — ADR-0056 removed the gate, so this path is
			// now the ordinary one under the GUI.
			failed, canc, err := runSubtaskSequential(ctx, s, parentSID, g[0], base, total,
				parentIntent, w, in, out, newView)
			base++
			if err != nil {
				return false, err
			}
			if canc {
				return true, nil
			}
			if failed {
				fmt.Fprintf(out, "split: subtask %d/%d failed — remaining subtasks not started\n", base, total)
				return false, nil
			}
		}
	}
	return false, nil
}

// runSubtasksSequential runs the flat proposal order one subtask at a time —
// the path a flat proposal takes, and the one the ADR-0056 kill switch falls
// back to. It is exactly the ADR-0023 fail-stop: a failed subtask stops the
// loop before the next one opens, so a task that never started leaves no
// half-run in the ledger.
func runSubtasksSequential(ctx context.Context, s *store.Store, parentSID string, elems []subtask.Element,
	parentIntent string, w subtaskWiring,
	in *bufio.Reader, out io.Writer, newView subtaskViewFactory) (cancelled bool, err error) {
	for i, e := range elems {
		fmt.Fprintf(out, "-- subtask %d/%d: %s --\n", i+1, len(elems), truncate(e.Do, 60))
		failed, canc, err := runSubtaskSequential(ctx, s, parentSID, e, i, len(elems),
			parentIntent, w, in, out, newView)
		if err != nil {
			return false, err
		}
		if canc {
			return true, nil
		}
		if failed {
			fmt.Fprintf(out, "split: subtask %d/%d failed — remaining subtasks not started\n", i+1, len(elems))
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
// per subtask and drives begin/show/end around the run exactly as an ordinary
// turn does. The view is built per subtask, and it names the provider this
// child actually runs on — which since ADR-0060 is the parent's unless the
// proposal declared otherwise.
func runSubtaskSequential(ctx context.Context, s *store.Store, parentSID string, e subtask.Element, gi, total int,
	parentIntent string, w subtaskWiring,
	in *bufio.Reader, out io.Writer, newView subtaskViewFactory) (failed, cancelled bool, err error) {
	cw, warn := w.forElement(e)
	if warn != "" {
		fmt.Fprintln(out, warn)
	}
	sid, err := openSubtask(s, cw.capability, e.Do, parentSID, cw.named)
	if err != nil {
		return false, false, err
	}
	prompt := subtask.Prompt(parentIntent, e.Do, gi, total)

	var result executor.Result
	var runErr error
	if cw.human {
		fmt.Fprintln(out, prompt)
		result, runErr = runHuman(s, out, sid, in)
		if runErr != nil {
			return false, false, runErr
		}
	} else {
		var v view
		if newView != nil {
			v = newView(cw.adapter.Name(), gi, total)
			v.begin()
		}
		sink := subtaskSink(s, sid, out, v)
		ex := &executor.Executor{Adapter: cw.adapter, Stderr: os.Stderr, Warn: os.Stderr}
		if os.Getenv("TOMOBIT_DEBUG") != "" {
			ex.Debug = os.Stderr
		}
		result, runErr = ex.Run(ctx, cw.request(prompt), sink)
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
// sequential split, where a failure stops the rest). A human-run split has no
// parallel form at all — the single terminal cannot host two people at once — so
// when the parent itself is the human, the group runs one member after another
// (before ADR-0054 a group could be part human and part provider, because each
// child drew its own routing lottery; now the whole split shares one).
// base is the group's first subtask's position
// in the flat order, so labels and prompts stay 1/total across the whole split.
// Returns failed (any member failed — the caller then stops before the next
// group) and cancelled (SIGINT — children and parent already hold task.cancelled).
//
// newView, when non-nil, gives every member its own framed view. The frames are
// open at the same time, which is exactly why ndjsonView carries `sub`: without
// it the stream would be K interleaved voices under one turn (ADR-0056's
// remaining homework, closed here). The stream's own lock — not this mutex —
// is what keeps those concurrent emits from tearing; mu still guards the
// terminal's `[n:provider]` fallback, which is a layout concern.
func runGroupParallel(ctx context.Context, s *store.Store, parentSID string, group []subtask.Element, base, total int,
	parentIntent string, w subtaskWiring, in *bufio.Reader, out io.Writer, mu *sync.Mutex,
	newView subtaskViewFactory) (failed, cancelled bool, err error) {
	type member struct {
		sid string
		gi  int
		e   subtask.Element
		w   subtaskWiring
	}
	members := make([]member, len(group))
	sids := make([]string, len(group))
	// Sessions are opened before any goroutine starts, so their ledger writes
	// keep the proposal's order — and so does the wiring each member resolves
	// (ADR-0060 Decision 2). Resolving here rather than inside the goroutines
	// is what lets a fallback line reach the terminal in proposal order,
	// without the mutex the parallel section needs.
	for k, e := range group {
		cw, warn := w.forElement(e)
		if warn != "" {
			fmt.Fprintln(out, warn)
		}
		sid, err := openSubtask(s, cw.capability, e.Do, parentSID, cw.named)
		if err != nil {
			return false, false, err
		}
		members[k] = member{sid: sid, gi: base + k, e: e, w: cw}
		sids[k] = sid
	}

	results := make([]executor.Result, len(members))
	runErrs := make([]error, len(members))

	if w.human {
		for k := range members {
			fmt.Fprintln(out, subtask.Prompt(parentIntent, members[k].e.Do, members[k].gi, total))
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
	} else {
		var wg sync.WaitGroup
		sem := make(chan struct{}, parallelWidthCap)
		for k := range members {
			wg.Add(1)
			go func(k int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				m := members[k]
				ex := &executor.Executor{Adapter: m.w.adapter, Stderr: os.Stderr, Warn: os.Stderr}
				if os.Getenv("TOMOBIT_DEBUG") != "" {
					ex.Debug = os.Stderr
				}
				// 並走の子も自分のフレームを持つ。K本が同時に開くので、
				// フレームの同一性は sub が運ぶ（ndjsonView.tag）。相手は
				// 子ごとに違いうるので、名乗る名前も子のものを使う (ADR-0060)。
				var v view
				if newView != nil {
					v = newView(m.w.adapter.Name(), m.gi, total)
					v.begin()
				}
				results[k], runErrs[k] = ex.Run(ctx,
					m.w.request(subtask.Prompt(parentIntent, m.e.Do, m.gi, total)),
					splitSink(s, m.sid, out, mu, m.gi+1, m.w.adapter.Name(), v))
				if v != nil {
					v.end(results[k])
				}
			}(k)
		}
		wg.Wait()

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
// v non-nil is the NDJSON view stream: the member's events go through its own
// framed view instead of the terminal's `[n:provider]` prefix, which is layout
// for a shared terminal and has no meaning to a structured consumer. Same split
// of responsibilities subtaskSink draws on the sequential path.
func splitSink(s *store.Store, sid string, out io.Writer, mu *sync.Mutex, n int, provider string, v view) executor.Sink {
	return func(ev executor.Event, ts int64) error {
		if v != nil {
			v.show(ev)
			return recordEvent(s, sid, ev, ts)
		}
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
