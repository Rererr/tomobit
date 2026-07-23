// Reflection probe (ADR-0015): drives internal/reflection directly against
// scratch ledgers — snapshot/Detect for the trigger types (Split birth,
// reversal, questioned, rehabilitation, human reversal, re-perception),
// the 1/day budget, the reaction mapping (1/2/3), and RecordAndApply's
// verdict routing. Usage: reflectprobe <workdir>
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/reflection"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/voice"
)

const day = int64(86400_000)

var (
	now    = time.Now().UnixMilli()
	failed = 0
	expSeq = 0
)

func check(name string, ok bool, detail string) {
	mark := "ok  "
	if !ok {
		mark = "FAIL"
		failed++
	}
	fmt.Printf("  [%s] %s — %s\n", mark, name, detail)
}

func openScratch(dir, name string) *store.Store {
	path := filepath.Join(dir, name)
	for _, suf := range []string{"", "-wal", "-shm"} {
		os.Remove(path + suf)
	}
	s, err := store.Open(path)
	if err != nil {
		panic(err)
	}
	return s
}

// apply inserts + applies one synthetic execution experience, the same
// insert-then-Apply order the real perceive path uses.
func apply(s *store.Store, en *core.Engine, provider string, ctx map[string]string, ok bool, ts int64) {
	expSeq++
	o := core.Outcome{Adopted: "as-is"}
	if !ok {
		o = core.Outcome{Reverted: true, Failed: true}
	}
	e := &core.Experience{
		ID: fmt.Sprintf("exp%05d", expSeq), SessionID: fmt.Sprintf("sess%05d", expSeq),
		TS: ts, Kind: core.KindExecution, ExtractorVer: 4, ExtractorModel: "probe",
		Context: ctx, Provider: provider, Outcome: o, Source: "production",
	}
	if err := s.InsertExperiences([]*core.Experience{e}); err != nil {
		panic(err)
	}
	if err := en.Apply(e); err != nil {
		panic(err)
	}
}

func detect(s *store.Store, snap *reflection.Snapshot) []reflection.Candidate {
	cands, err := reflection.Detect(snap, s, now)
	if err != nil {
		panic(err)
	}
	return cands
}

func hasType(cands []reflection.Candidate, typ string) (reflection.Candidate, bool) {
	for _, c := range cands {
		if c.Type == typ {
			return c, true
		}
	}
	return reflection.Candidate{}, false
}

func dump(cands []reflection.Candidate) string {
	var parts []string
	for _, c := range cands {
		parts = append(parts, fmt.Sprintf("%s@%s/%s", c.Type, c.Scope.Key(), c.Provider))
	}
	if parts == nil {
		return "(none)"
	}
	return strings.Join(parts, ", ")
}

func snapNow(s *store.Store) *reflection.Snapshot {
	snap, err := reflection.TakeSnapshot(s, now)
	if err != nil {
		panic(err)
	}
	return snap
}

func main() {
	dir := os.Args[1]

	// ---- S1: Split birth (+ parent questioned) --------------------------
	fmt.Println("S1: split birth — confident go-parent hit by a rust failure streak")
	s1 := openScratch(dir, "s1.db")
	en1 := &core.Engine{Repo: s1}
	for i := 0; i < 12; i++ {
		apply(s1, en1, "claude-code", map[string]string{"cap": "implement", "lang": "go"}, true, now-int64(40-i*2)*day)
	}
	snap := snapNow(s1)
	for i := 0; i < 4; i++ {
		apply(s1, en1, "claude-code", map[string]string{"cap": "implement", "lang": "rust"}, false, now-int64(3-i)*day)
	}
	en1.ReconcileMerges(now)
	cands := detect(s1, snap)
	fmt.Println("  candidates:", dump(cands))
	c, ok := hasType(cands, voice.InsightSplit)
	check("split candidate fires", ok, fmt.Sprintf("scope=%s diff=%s", c.Scope.Key(), c.Diff.Key()))
	if ok {
		check("split text renders", c.Text() != "", c.Text())
	}
	s1.Close()

	// ---- S2: reversal ----------------------------------------------------
	fmt.Println("S2: reversal — codex overtakes claude-code on cap=review beyond the 0.1 band")
	s2 := openScratch(dir, "s2.db")
	en2 := &core.Engine{Repo: s2}
	for i := 0; i < 6; i++ {
		apply(s2, en2, "claude-code", map[string]string{"cap": "review"}, true, now-int64(30-i*4)*day)
	}
	for i := 0; i < 3; i++ {
		apply(s2, en2, "codex", map[string]string{"cap": "review"}, true, now-int64(28-i*4)*day)
		apply(s2, en2, "codex", map[string]string{"cap": "review"}, false, now-int64(26-i*4)*day)
	}
	snap = snapNow(s2)
	for i := 0; i < 8; i++ {
		apply(s2, en2, "codex", map[string]string{"cap": "review"}, true, now-int64(8-i)*day)
	}
	for i := 0; i < 5; i++ {
		apply(s2, en2, "claude-code", map[string]string{"cap": "review"}, false, now-int64(5-i)*day)
	}
	en2.ReconcileMerges(now)
	cands = detect(s2, snap)
	fmt.Println("  candidates:", dump(cands))
	c, ok = hasType(cands, voice.InsightReversal)
	check("reversal fires", ok && c.Provider == "codex" && c.Other == "claude-code",
		fmt.Sprintf("winner=%s over=%s", c.Provider, c.Other))
	if ok {
		check("reversal text renders", c.Text() != "", c.Text())
	}
	s2.Close()

	// ---- S2b: hysteresis — a photo-finish flip must stay silent ----------
	fmt.Println("S2b: hysteresis — leader flips by less than 0.1: no telling")
	s2b := openScratch(dir, "s2b.db")
	en2b := &core.Engine{Repo: s2b}
	for i := 0; i < 5; i++ {
		apply(s2b, en2b, "claude-code", map[string]string{"cap": "review"}, true, now-int64(30-i)*day)
		apply(s2b, en2b, "codex", map[string]string{"cap": "review"}, true, now-int64(29-i)*day)
	}
	apply(s2b, en2b, "codex", map[string]string{"cap": "review"}, false, now-25*day)
	snap = snapNow(s2b) // claude leads by a hair
	apply(s2b, en2b, "codex", map[string]string{"cap": "review"}, true, now-2*day)
	apply(s2b, en2b, "codex", map[string]string{"cap": "review"}, true, now-1*day)
	apply(s2b, en2b, "claude-code", map[string]string{"cap": "review"}, false, now)
	en2b.ReconcileMerges(now)
	cands = detect(s2b, snap)
	_, revFired := hasType(cands, voice.InsightReversal)
	// measure the actual margin to report honestly
	conns, _ := s2b.AllConnections()
	means := map[string]float64{}
	for _, cn := range conns {
		if cn.Kind == core.ConnCapability && cn.ScopeKey == "cap=review" {
			means[cn.Target] = cn.Mean(now)
		}
	}
	margin := means["codex"] - means["claude-code"]
	if margin < 0.1 {
		check("photo-finish stays silent", !revFired, fmt.Sprintf("margin=%.3f candidates=%s", margin, dump(cands)))
	} else {
		fmt.Printf("  [skip] seed produced margin %.3f >= 0.1 — hysteresis not exercised\n", margin)
	}
	s2b.Close()

	// ---- S3: rehabilitation ----------------------------------------------
	fmt.Println("S3: rehabilitation — gate re-entry after a failure streak heals")
	s3 := openScratch(dir, "s3.db")
	en3 := &core.Engine{Repo: s3}
	for i := 0; i < 4; i++ {
		apply(s3, en3, "claude-code", map[string]string{"cap": "summarize"}, false, now-int64(20-i*2)*day)
	}
	snap = snapNow(s3)
	for i := 0; i < 10; i++ {
		apply(s3, en3, "claude-code", map[string]string{"cap": "summarize"}, true, now-int64(9-i)*day)
	}
	en3.ReconcileMerges(now)
	cands = detect(s3, snap)
	fmt.Println("  candidates:", dump(cands))
	c, ok = hasType(cands, voice.InsightRehabilitated)
	check("rehabilitation fires", ok, fmt.Sprintf("scope=%s provider=%s", c.Scope.Key(), c.Provider))
	if ok {
		check("rehabilitation text renders", c.Text() != "", c.Text())
	}
	s3.Close()

	// ---- S4: human reversal ----------------------------------------------
	fmt.Println("S4: human reversal — the user overtakes the providers (ADR-0019)")
	s4 := openScratch(dir, "s4.db")
	en4 := &core.Engine{Repo: s4}
	for i := 0; i < 5; i++ {
		apply(s4, en4, "claude-code", map[string]string{"cap": "refactor"}, true, now-int64(20-i*2)*day)
	}
	apply(s4, en4, "human", map[string]string{"cap": "refactor"}, true, now-15*day)
	apply(s4, en4, "human", map[string]string{"cap": "refactor"}, false, now-14*day)
	snap = snapNow(s4)
	for i := 0; i < 9; i++ {
		apply(s4, en4, "human", map[string]string{"cap": "refactor"}, true, now-int64(9-i)*day)
	}
	for i := 0; i < 4; i++ {
		apply(s4, en4, "claude-code", map[string]string{"cap": "refactor"}, false, now-int64(4-i)*day)
	}
	en4.ReconcileMerges(now)
	cands = detect(s4, snap)
	fmt.Println("  candidates:", dump(cands))
	c, ok = hasType(cands, voice.InsightReversal)
	check("human reversal fires", ok && c.Provider == "human", fmt.Sprintf("winner=%s over=%s", c.Provider, c.Other))
	if ok {
		human := c.Text()
		check("human reversal uses its own line", human != "" && human != voice.ReflectReversal(c.Scope, c.Provider, c.Other), human)
	}
	s4.Close()

	// ---- S5: re-perception candidates -------------------------------------
	fmt.Println("S5: re-perception — superseded extraction with moved tokens (5th trigger)")
	before := []*core.Experience{{
		SessionID: "sx", Kind: core.KindExecution, ExtractorVer: 4,
		Context: map[string]string{"cap": "implement", "lang": "go"}, Provider: "claude-code",
	}}
	after := []*core.Experience{{
		SessionID: "sx", Kind: core.KindExecution, ExtractorVer: 5,
		Context: map[string]string{"cap": "implement", "lang": "rust"}, Provider: "claude-code",
	}}
	rc := reflection.ReperceptionCandidates(before, after)
	check("reperception fires", len(rc) == 1 && rc[0].Type == voice.InsightReperceived,
		dump(rc))
	if len(rc) == 1 {
		check("old/new tokens move", rc[0].OldToken == "go" && rc[0].NewToken == "rust",
			fmt.Sprintf("old=%s new=%s text=%s", rc[0].OldToken, rc[0].NewToken, rc[0].Text()))
	}
	same := reflection.ReperceptionCandidates(before, []*core.Experience{{
		SessionID: "sx", Kind: core.KindExecution, ExtractorVer: 5,
		Context: map[string]string{"cap": "implement", "lang": "go"}, Provider: "claude-code",
	}})
	check("same reading stays silent", len(same) == 0, dump(same))

	// ---- S6: budget --------------------------------------------------------
	fmt.Println("S6: 1/day budget (tomo.reflected window)")
	s6 := openScratch(dir, "s6.db")
	ok6, _ := reflection.HasBudget(s6, now)
	check("fresh ledger has budget", ok6, "no tomo.reflected yet")
	s6.AppendEvent("r1", "tomo.reflected", now-25*3600*1000, map[string]any{"type": "t"})
	ok6, _ = reflection.HasBudget(s6, now)
	check("25h-old telling frees the budget", ok6, "window is 24h")
	s6.AppendEvent("r2", "tomo.reflected", now-3600*1000, map[string]any{"type": "t"})
	ok6, _ = reflection.HasBudget(s6, now)
	check("1h-old telling spends the budget", !ok6, "no second telling today")
	s6.Close()

	// ---- S7: reaction mapping + verdict routing ---------------------------
	fmt.Println("S7: reaction 1/2/3 mapping and RecordAndApply verdict")
	rd := func(in string) string {
		return reflection.Ask(bufio.NewReader(strings.NewReader(in)), io.Discard, "t")
	}
	check("1 => unexpected", rd("1\n") == reflection.ReactionUnexpected, rd("1\n"))
	check("2 => known", rd("2\n") == reflection.ReactionKnown, rd("2\n"))
	check("3 => wrong", rd("3\n") == reflection.ReactionWrong, rd("3\n"))
	check("Enter => skip", rd("\n") == "", "empty reaction")
	check("EOF => skip", rd("") == "", "empty reaction")

	s7 := openScratch(dir, "s7.db")
	en7 := &core.Engine{Repo: s7}
	for i := 0; i < 5; i++ {
		apply(s7, en7, "claude-code", map[string]string{"cap": "implement"}, true, now-int64(10-i)*day)
	}
	get := func() *core.Connection {
		cn, err := s7.GetConnection(core.ConnCapability, "cap=implement", "claude-code")
		if err != nil {
			panic(err)
		}
		return cn
	}
	beforeConn := get()
	a0, b0 := beforeConn.Alpha, beforeConn.Beta
	cand := reflection.Candidate{Type: voice.InsightQuestioned,
		Scope: core.NewScope("cap=implement"), Provider: "claude-code"}
	if err := reflection.RecordAndApply(s7, en7, cand, 42, reflection.ReactionWrong, "", 4, now); err != nil {
		panic(err)
	}
	afterConn := get()
	check("それ違う lands as a down verdict", afterConn.Beta > b0 && afterConn.Alpha <= a0+1e-9,
		fmt.Sprintf("alpha %.3f->%.3f beta %.3f->%.3f", a0, afterConn.Alpha, b0, afterConn.Beta))
	var kind, insight, reaction, verdict string
	row := s7.DB.QueryRow(`SELECT kind, json_extract(outcome,'$.insight'),
		json_extract(outcome,'$.reaction'), coalesce(json_extract(outcome,'$.verdict'),'')
		FROM experiences WHERE kind='reflection'`)
	if err := row.Scan(&kind, &insight, &reaction, &verdict); err != nil {
		panic(err)
	}
	check("reflection experience recorded", kind == "reflection" && insight == voice.InsightQuestioned &&
		reaction == "wrong" && verdict == "down",
		fmt.Sprintf("insight=%s reaction=%s verdict=%s", insight, reaction, verdict))
	nConnBefore := countConns(s7)
	cand2 := reflection.Candidate{Type: voice.InsightSplit,
		Scope: core.NewScope("cap=implement", "lang=zig"), Provider: "claude-code"}
	if err := reflection.RecordAndApply(s7, en7, cand2, 43, reflection.ReactionUnexpected, "", 4, now); err != nil {
		panic(err)
	}
	check("reflection births no connection", countConns(s7) == nConnBefore,
		fmt.Sprintf("connections %d -> %d (lang=zig scope had no home)", nConnBefore, countConns(s7)))
	okb, _ := reflection.HasBudget(s7, now)
	check("telling spends the budget", !okb, "tomo.reflected recorded")
	s7.Close()

	// ---- S8: Pick's 選球眼 --------------------------------------------------
	fmt.Println("S8: Pick prefers the insight type with better reactions")
	var exps []*core.Experience
	for i := 0; i < 8; i++ {
		exps = append(exps, &core.Experience{
			Kind: core.KindReflection, TS: now - int64(i)*day,
			Outcome: core.Outcome{Insight: voice.InsightSplit, Reaction: reflection.ReactionKnown},
		})
		exps = append(exps, &core.Experience{
			Kind: core.KindReflection, TS: now - int64(i)*day,
			Outcome: core.Outcome{Insight: voice.InsightQuestioned, Reaction: reflection.ReactionUnexpected},
		})
	}
	pool := []reflection.Candidate{
		{Type: voice.InsightSplit, Scope: core.NewScope("cap=implement"), Provider: "a"},
		{Type: voice.InsightQuestioned, Scope: core.NewScope("cap=implement"), Provider: "b"},
	}
	wins := 0
	for seed := int64(0); seed < 200; seed++ {
		if reflection.Pick(pool, exps, seed, now).Type == voice.InsightQuestioned {
			wins++
		}
	}
	check("unexpected-rich type wins the lottery", wins > 150,
		fmt.Sprintf("questioned won %d/200 draws", wins))

	fmt.Println()
	if failed > 0 {
		fmt.Printf("REFLECTION PROBE: %d FAILURES\n", failed)
		os.Exit(1)
	}
	fmt.Println("REFLECTION PROBE: all checks passed")
}

func countConns(s *store.Store) int {
	conns, err := s.AllConnections()
	if err != nil {
		panic(err)
	}
	return len(conns)
}
