package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/store"
)

// verdictWords is the closed vocabulary the command accepts (ADR-0055
// Decision 2). "clear" is the withdrawal: a judgment nobody can take back is a
// trap, and the layers below (ADR-0003) are the honest place to fall to when
// the human no longer has an opinion.
var verdictWords = map[string]bool{"up": true, "down": true, "clear": true}

// cmdVerdict is the second layer's retrospective writer (ADR-0055 Decision 2):
// the human's judgment on a task that is already closed. It is the place
// 「まだ言えない」 goes when it becomes sayable — a week later, when the
// refactor turns out to have broken something, or to have been right after all.
//
// The name is not new. ADR-0006 Decision 3 listed `user.verdict` among what
// Phase 1 would not produce, and named "将来の verdict コマンド" as the writer
// that would. This is that command, six years later.
//
//	tomobit verdict <session-id> up|down|clear
//
// No confirmation gate. forget needs one because it is irreversible
// (ADR-0033); this is an append plus a new generation, and `clear` walks it
// back — a reversible act gets no ceremony.
func cmdVerdict(args []string) error {
	fs := flag.NewFlagSet("verdict", flag.ExitOnError)
	db := dbFlag(fs)
	fs.Parse(args)

	rest := fs.Args()
	if len(rest) != 2 {
		return fmt.Errorf("verdict: usage: tomobit verdict <session-id> up|down|clear")
	}
	sid, word := rest[0], rest[1]
	if !verdictWords[word] {
		return fmt.Errorf("verdict: unknown judgment %q (up | down | clear)", word)
	}

	s, err := openStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()

	if err := verdictAllowed(s, sid); err != nil {
		return err
	}

	now := time.Now().UnixMilli()
	if err := s.AppendEvent(sid, "user.verdict", now, map[string]any{"verdict": word}); err != nil {
		return err
	}

	// The event above is the truth. What follows only makes it effective today:
	// a session already perceived would otherwise carry the old outcome until
	// some future extractor revision re-read the ledger (ADR-0055 Decision 2).
	stored := word
	if word == "clear" {
		stored = ""
	}
	newVer, carried, err := s.CarryVerdictForward(sid, stored, now)
	if err != nil {
		return err
	}
	if !carried {
		// Nothing to carry: the boundary perception has not run for this session
		// (it degraded, or `tomobit perceive` has not been run). The event is
		// filed, and the pending queue will read it — no rebuild is needed
		// because no projection changed.
		fmt.Printf("verdict: %s -> %s (このセッションはまだ知覚されていない — 次の perceive が読む)\n", sid, word)
		return nil
	}

	// Same discipline as forget/amend (ADR-0033 Decision 2): the projection is
	// reconciled inside the command, never left as a step the human must
	// remember. Rebuild replays every experience at its own timestamp, so the
	// judgment lands on the day the work happened rather than today.
	en := &core.Engine{Repo: s}
	if err := en.Rebuild(); err != nil {
		return err
	}
	conns, err := s.AllConnections()
	if err != nil {
		return err
	}
	fmt.Printf("verdict: %s -> %s (ver %d, rebuilt: %d connections)\n", sid, word, newVer, len(conns))
	return nil
}

// verdictAllowed refuses the four sessions a judgment cannot mean anything on
// (ADR-0055 Decision 2). Each refusal names what to do instead — a wall with no
// door is worse than no wall.
func verdictAllowed(s *store.Store, sid string) error {
	evs, err := s.EventsBySession(sid)
	if err != nil {
		return err
	}
	if len(evs) == 0 {
		return fmt.Errorf("verdict: no such session %q", sid)
	}

	var finished, cancelled, amended bool
	var parent string
	for _, e := range evs {
		switch e.Type {
		case "task.started":
			if p, ok := e.Payload["parent"].(string); ok {
				parent = p
			}
		case "task.finished":
			finished = true
		case "task.cancelled":
			cancelled = true
		case "user.amended":
			amended = true
		}
	}

	if cancelled {
		// OutcomeWeight itself treats a cancelled task as unusable before it
		// even looks at Verdict: nothing was delivered, so there is no
		// deliverable for a judgment to be about.
		return fmt.Errorf("verdict: %s は中断されたタスクで、判定する成果物が無い", sid)
	}
	if !finished {
		return fmt.Errorf("verdict: %s はまだ終わっていない — 判定はタスクを閉じてから", sid)
	}
	if amended {
		// 人間の知覚は最終知覚 (ADR-0033 Decision 4). A lighter organ does not
		// get layered on top of the heavier one that already froze this session.
		return fmt.Errorf(
			"verdict: %s は amend 済み（人間の知覚は最終知覚 — ADR-0033）。判定も変えるなら amend --outcome を使う", sid)
	}
	if parent != "" {
		// A split child is the parent task's breakdown and holds no experience
		// (ADR-0054 Decision 2), so a judgment on it would move nothing. A duel
		// side is the exception — deliberately commissioned on its own, with its
		// own execution row (ADR-0026) — and it names itself out through the
		// task.duel its parent recorded.
		duel, err := parentRanADuel(s, parent)
		if err != nil {
			return err
		}
		if !duel {
			return fmt.Errorf(
				"verdict: %s は分割の子で、経験を持たない（ADR-0054）。親の %s を判定する", sid, parent)
		}
	}
	return nil
}

// parentRanADuel reports whether parentSID recorded task.duel — the marker that
// makes its children independent commissions rather than one task's breakdown.
func parentRanADuel(s *store.Store, parentSID string) (bool, error) {
	evs, err := s.EventsBySession(parentSID)
	if err != nil {
		return false, err
	}
	for _, e := range evs {
		if e.Type == "task.duel" {
			return true, nil
		}
	}
	return false, nil
}

// askVerdictOnContradiction is the second layer's boundary writer (ADR-0055
// Decision 1): the one moment in OutcomeWeight's derivation where the machine
// outranks the human.
//
// Reading that derivation top to bottom, ★ below is the only place an observed
// signal beats an answered one:
//
//	Cancelled                → 無効     機械
//	Verdict up/down          → 1 / 0   人
//	Reverted (3=だめだった)    → 0       人
//	TestsPassed=false        → 0       機械  ★
//	Adopted as-is (1=文句なし) → 1.0     人
//	...
//
// So the question has exactly one trigger: a red suite and a 文句なし on the
// same task. Rather than ask the human to learn a command for that moment,
// tomobit asks at it — ADR-0003 Decision 3's pull-type inversion, and the same
// shape ADR-0051 used to put its extra question only on the days that earn it.
//
// 2=まあまあ is deliberately not a trigger (Decision 1, owner's call): a red
// suite on a day the human already said they had trouble is not a sharp enough
// contradiction to spend a question on.
//
// The default is no. Silence is not consent (ADR-0049), so an unanswered prompt
// leaves the red standing — but standing as a y=0 the human declined to
// override, rather than one they were never asked about.
func askVerdictOnContradiction(s *store.Store, sid string, in *bufio.Reader, out io.Writer,
	humanPresent bool, feedback map[string]any) {
	if !humanPresent || feedback["adopted"] != "as-is" {
		return
	}
	red, err := testWentRed(s, sid)
	if err != nil {
		fmt.Fprintln(os.Stderr, "verdict:", err)
		return
	}
	if !red {
		return
	}

	// 効果を隠さない (ADR-0053 の「費用を隠さない」と同じ理由): この y は
	// 「赤を無かったことにする」ではなく「赤より強い判定を置く」である。
	fmt.Fprint(out, "テストは赤だったけど、文句なし? y なら赤より強く記録する [y/N] ")
	line, _ := in.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
	default:
		return
	}
	if err := s.AppendEvent(sid, "user.verdict", time.Now().UnixMilli(),
		map[string]any{"verdict": "up"}); err != nil {
		fmt.Fprintln(os.Stderr, "verdict: 記帳に失敗:", err)
		return
	}
	// No carry-forward here, and none needed: perception has not run for this
	// session yet (perceiveBestEffort is still ahead in finishTask), so the
	// event is read on the first pass like any other deterministic signal.
	fmt.Fprintln(out, dim("赤より強く記録した"))
}

// testWentRed reports whether this session's first-layer observation failed.
// It reads the ledger rather than taking the observation's return value,
// because the boundary's two organs must not grow a data dependency on each
// other's call order — the recorded fact is what the perception will read too.
func testWentRed(s *store.Store, sid string) (bool, error) {
	evs, err := s.EventsBySession(sid)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", sid, err)
	}
	red := false
	for _, e := range evs {
		if e.Type != "test.result" {
			continue
		}
		if passed, ok := e.Payload["passed"].(bool); ok {
			red = !passed // the newest observation is the one that counts
		}
	}
	return red, nil
}
