// Stage curve probe: calibration material for ADR-0017's growth-stage knobs
// (ThetaCal, ThetaSharp, islandMinFreq, the S1->S2 evidence gate). Drives
// core.Engine.Apply directly against scratch ledgers (the reflectprobe
// pattern, tools/dogfood/replay/reflectprobe/main.go) — no perceive, no
// LLM/provider stub — one synthetic execution experience per "session",
// backdated one day apart, and re-evaluates face.StageFrom after every one.
//
// Never touches ~/.tomobit: store.Open takes an explicit scratch path and
// reads no config (internal/store/store.go:119 — no env/HOME dependency),
// so unlike run_dogfood.sh's other probes this needs no env.sh sourcing.
//
// Usage: stagecurve <scratch-dir>
package main

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/decide"
	"github.com/Rererr/tomobit/internal/face"
	"github.com/Rererr/tomobit/internal/store"
)

const day = int64(86400_000)

// Mirrors of internal/face/stage.go's unexported knobs (lines 58-66), needed
// here only to reproduce isSharp's per-island Wobble call for reporting
// (calc3) and for the parameterized sweep (calc4, which must not touch the
// real constants). face.ThetaCal/face.ThetaSharp are exported and used
// as-is; only the three unexported ones are duplicated.
const (
	islandMinFreqDefault = 2.5 // stage.go:60
	sharpDraws           = 128 // stage.go:65
	stageSeed            = 1   // stage.go:66
	evidenceGateDefault  = 3.0 // stage.go:94 (maxEvidence < 3)
)

type providerSpec struct {
	name string
	p    float64 // per-session success probability
}

// checkpoint is everything face.StageFrom read at one session's ts, plus the
// raw per-gate numbers stage.go's isCalibrated/isSharp compute internally
// (reproduced here since they are unexported) — calc3's material, and
// calc4's sweep input (so the knob sweep needs no re-simulation, only a
// different threshold applied to the same measured numbers).
type checkpoint struct {
	session     int
	ts          int64
	provider    string
	success     bool
	stage       int
	maxEvidence float64
	sumW        float64 // >0 iff calibration is defined at all
	calMean     float64 // decayed mean excess surprisal (isCalibrated's statistic)
	maxPref     float64
	freq        map[string]float64 // island scope_key -> decayed arrival frequency
	wobble      map[string]float64 // island scope_key -> decide.Wobble
	rivals      map[string]int     // island scope_key -> capability targets whose scope applies (ADR-0045 Decision 1's contest count)
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func openScratch(dir, name string) *store.Store {
	path := dir + "/" + name
	for _, suf := range []string{"", "-wal", "-shm"} {
		os.Remove(path + suf)
	}
	s, err := store.Open(path)
	check(err)
	return s
}

// applyExec inserts + applies one synthetic execution experience — the same
// insert-then-Apply order the real perceive path uses (reflectprobe's apply
// helper). Single context token "cap=implement" throughout: one capability,
// one island, so the growth curve measures the gates themselves rather than
// multi-island bookkeeping.
func applyExec(s *store.Store, en *core.Engine, seq *int, provider string, ok bool, ts int64) {
	*seq++
	o := core.Outcome{Adopted: "as-is"}
	if !ok {
		o = core.Outcome{Reverted: true}
	}
	e := &core.Experience{
		ID: fmt.Sprintf("exp%05d", *seq), SessionID: fmt.Sprintf("sess%05d", *seq),
		TS: ts, Kind: core.KindExecution, ExtractorVer: 4, ExtractorModel: "probe",
		Context: map[string]string{"cap": "implement"}, Provider: provider,
		Outcome: o, Source: "production",
	}
	check(s.InsertExperiences([]*core.Experience{e}))
	check(en.Apply(e))
	check(en.ReconcileMerges(ts))
}

// applyPref inserts + applies one synthetic preference experience on the
// same single island — the curiosity-ask shape (deterministic answer, one
// pair) that scenario D uses to give the lottery something to settle on.
func applyPref(s *store.Store, en *core.Engine, seq *int, preferred, over string, ts int64) {
	*seq++
	e := &core.Experience{
		ID: fmt.Sprintf("exp%05d", *seq), SessionID: fmt.Sprintf("sess%05d", *seq),
		TS: ts, Kind: core.KindPreference, ExtractorVer: 4, ExtractorModel: "probe",
		Context: map[string]string{"cap": "implement"},
		Outcome: core.Outcome{Preferred: preferred, Over: over},
		Source:  "learning",
	}
	check(s.InsertExperiences([]*core.Experience{e}))
	check(en.Apply(e))
	check(en.ReconcileMerges(ts))
}

// capabilityProviders reproduces stage.go:156-168's provider/island set
// construction from the capability connections alone (the target string is
// whatever OutcomeWeight's Provider was — including "human" if a capability
// connection with Target=="human" exists, per types.go:96-102's
// Experience.Target()).
func capabilityProviders(conns []*core.Connection) (providers, islands []string) {
	providerSet, islandSet := map[string]bool{}, map[string]bool{}
	for _, c := range conns {
		if c.Kind != core.ConnCapability {
			continue
		}
		providerSet[c.Target] = true
		islandSet[c.ScopeKey] = true
	}
	for p := range providerSet {
		providers = append(providers, p)
	}
	for i := range islandSet {
		islands = append(islands, i)
	}
	sort.Strings(providers)
	sort.Strings(islands)
	return
}

// gateValues reproduces isCalibrated's and isSharp's raw numbers (both
// unexported in internal/face) at nowMs — the material calc3/calc4 need,
// plus the per-island rival count ADR-0045's revised gate reads (calc5).
func gateValues(s *store.Store, nowMs int64) (maxEvidence, sumW, calMean, maxPref float64, freq, wobble map[string]float64, rivals map[string]int) {
	conns, err := s.AllConnections()
	check(err)

	for _, c := range conns {
		if ev := c.Evidence(nowMs); ev > maxEvidence {
			maxEvidence = ev
		}
		if c.Kind == core.ConnPreference {
			if ev := c.Evidence(nowMs); ev > maxPref {
				maxPref = ev
			}
		}
	}

	var sumWS float64
	for _, c := range conns {
		entries, err := s.LedgerFor(c.Kind, c.ScopeKey, c.Target)
		check(err)
		for _, e := range entries {
			w := core.DecayFactor(e.TS, nowMs)
			sumWS += w * e.SExcess
			sumW += w
		}
	}
	if sumW > 0 {
		calMean = sumWS / sumW
	}

	providers, islands := capabilityProviders(conns)
	exps, err := s.CurrentExperiences()
	check(err)
	freq, wobble, rivals = map[string]float64{}, map[string]float64{}, map[string]int{}
	for _, key := range islands {
		scope := core.ParseScopeKey(key)
		f := 0.0
		for _, e := range exps {
			if e.Kind == core.KindExecution && scope.SubsetOf(e.Tokens()) {
				f += core.DecayFactor(e.TS, nowMs)
			}
		}
		freq[key] = f
		// Computed for every island regardless of frequency: Wobble itself
		// doesn't depend on islandMinFreq, only the "frequent" filter applied
		// downstream does — decoupling them lets calc4 sweep islandMinFreq
		// without recomputing this. The global providers list is kept here
		// (pre-ADR-0045 shape) because in every probe scenario all providers
		// share the single cap=implement island, so the per-island rival set
		// coincides with it — the recorded wobble serves both clones.
		wobble[key] = decide.Wobble(conns, providers, scope, "", sharpDraws, stageSeed, nowMs)
		set := map[string]bool{}
		for _, c := range conns {
			if c.Kind == core.ConnCapability && c.Scope().SubsetOf(scope) {
				set[c.Target] = true
			}
		}
		rivals[key] = len(set)
	}
	return
}

// stageForKnobs parameterizes the PRE-ADR-0045 (ADR-0017) S1..S4 logic
// exactly over checkpoint's already-measured raw numbers — kept as the
// baseline clone now that internal/face carries the revision, both for
// calc4's original sweeps and for calc6's before/after comparison. It is a
// probe-side clone, not a code path internal/face exposes — the sweeps need
// varying thresholds without editing the real constants, so there is nowhere
// else for this to live. Preference (S4->S5) is left out: none of the
// sweep's scenarios record human-pair preference, so every run tops out at
// Adult.
func stageForKnobs(c checkpoint, evidenceGate, thetaCal, thetaSharp, minFreq float64) int {
	if c.maxEvidence < evidenceGate {
		return face.StageChick
	}
	if !(c.sumW > 0 && c.calMean <= thetaCal) {
		return face.StageChild
	}
	frequent, sharp := 0, true
	for key, f := range c.freq {
		if f < minFreq {
			continue
		}
		frequent++
		if c.wobble[key] > thetaSharp {
			sharp = false
		}
	}
	if frequent == 0 || !sharp {
		return face.StageYouth
	}
	return face.StageAdult
}

// stageRevised parameterizes the ADR-0045 revision (contested-island
// sharpness, the ThetaCalMin sample floor) over the same measured numbers —
// calc5's sweep input for choosing ThetaCalMin. All other knobs stay at
// their defaults; S5 is out for the same reason as stageForKnobs.
func stageRevised(c checkpoint, thetaCalMin float64) int {
	if c.maxEvidence < evidenceGateDefault {
		return face.StageChick
	}
	if !(c.sumW >= thetaCalMin && c.calMean <= face.ThetaCal) {
		return face.StageChild
	}
	contested, sharp := 0, true
	for key, f := range c.freq {
		if f < islandMinFreqDefault || c.rivals[key] < 2 {
			continue
		}
		contested++
		if c.wobble[key] > face.ThetaSharp {
			sharp = false
		}
	}
	if contested == 0 || !sharp {
		return face.StageYouth
	}
	return face.StageAdult
}

func stageLabel(s int) string {
	if s < 0 {
		return "(none)"
	}
	return fmt.Sprintf("%d:%s", s, face.StageName(s))
}

func fmtMap(m map[string]float64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%.3f", k, m[k]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// runScenario accumulates n sessions one day apart (t0 + i*day, i=0..n-1),
// each provider chosen round-robin from providers, success drawn from a
// seeded RNG (rng.Float64() < provider's p) — deterministic and reproducible
// for a given seed, not a hand pattern like seed.py's OK/FAIL cycling, since
// the target success rates (0.55, 0.7, 0.85) don't divide evenly.
func runScenario(dir, name string, providers []providerSpec, n int, seed, t0 int64) (*store.Store, []checkpoint) {
	return runScenarioPref(dir, name, providers, n, seed, t0, 0)
}

// runScenarioPref is runScenario plus a preference drip: every prefEvery-th
// session (0 = never) also answers a curiosity-style ask, preferring the
// first provider over the second — scenario D's way of testing that ADR-0045
// Decision 1 leaves おとな reachable once preference evidence exists.
func runScenarioPref(dir, name string, providers []providerSpec, n int, seed, t0 int64, prefEvery int) (*store.Store, []checkpoint) {
	s := openScratch(dir, name+".db")
	en := &core.Engine{Repo: s}
	rng := rand.New(rand.NewSource(seed))
	seq := 0
	cps := make([]checkpoint, 0, n)
	for i := 0; i < n; i++ {
		prov := providers[i%len(providers)]
		ok := rng.Float64() < prov.p
		ts := t0 + int64(i)*day
		applyExec(s, en, &seq, prov.name, ok, ts)
		if prefEvery > 0 && (i+1)%prefEvery == 0 && len(providers) >= 2 {
			applyPref(s, en, &seq, providers[0].name, providers[1].name, ts+3600_000)
		}

		stage, err := face.StageFrom(s, ts)
		check(err)
		me, sw, cm, mp, fr, wb, rv := gateValues(s, ts)
		cps = append(cps, checkpoint{
			session: i + 1, ts: ts, provider: prov.name, success: ok, stage: stage,
			maxEvidence: me, sumW: sw, calMean: cm, maxPref: mp, freq: fr, wobble: wb, rivals: rv,
		})
	}
	return s, cps
}

// printDetail prints every one of the first `head` sessions unconditionally
// (calc3's raw material) — printGrowth alone only shows transition lines,
// which can silently skip a stage that a session boundary landed on for zero
// sampled checkpoints in between (evidenceGate and isCalibrated can both
// clear within the same session once evidence accumulates fast enough).
func printDetail(name string, cps []checkpoint, head int) {
	fmt.Printf("\n--- %s per-session detail (first %d) ---\n", name, head)
	for _, c := range cps {
		if c.session > head {
			break
		}
		fmt.Printf("  session %3d: %-11s ok=%-5v stage=%-14s maxEvidence=%.3f calMean=%.3f(sumW=%.2f) islandFreq=%s islandWobble=%s maxPrefEv=%.2f\n",
			c.session, c.provider, c.success, stageLabel(c.stage), c.maxEvidence, c.calMean, c.sumW, fmtMap(c.freq), fmtMap(c.wobble), c.maxPref)
	}
}

func printGrowth(name string, cps []checkpoint) {
	fmt.Printf("\n--- %s growth (n=%d sessions, 1/day) ---\n", name, len(cps))
	last := -1
	for _, c := range cps {
		if c.stage == last {
			continue
		}
		fmt.Printf("  session %3d: %-14s -> %-14s | maxEvidence=%.2f calMean=%.3f(sumW=%.2f) islandFreq=%s islandWobble=%s maxPrefEv=%.2f\n",
			c.session, stageLabel(last), stageLabel(c.stage),
			c.maxEvidence, c.calMean, c.sumW, fmtMap(c.freq), fmtMap(c.wobble), c.maxPref)
		last = c.stage
	}
}

// arrivalTable prints, per scenario, the first session index at which each
// stage 1..4 (Chick..Adult) was reached — calc2's headline number. Stage 5
// (Partner) never appears: none of these scenarios record preference
// experience.
func arrivalTable(results map[string][]checkpoint, order []string) {
	fmt.Println("\n=== calc2: stage-arrival session table ===")
	stages := []int{face.StageChick, face.StageChild, face.StageYouth, face.StageAdult}
	fmt.Printf("  %-10s", "scenario")
	for _, st := range stages {
		fmt.Printf(" %-16s", face.StageName(st))
	}
	fmt.Println()
	for _, name := range order {
		cps := results[name]
		fmt.Printf("  %-10s", name)
		arrived := map[int]int{}
		for _, c := range cps {
			if _, ok := arrived[c.stage]; !ok {
				arrived[c.stage] = c.session
			}
		}
		for _, st := range stages {
			if s, ok := arrived[st]; ok {
				fmt.Printf(" %-16s", fmt.Sprintf("session %d", s))
			} else {
				fmt.Printf(" %-16s", fmt.Sprintf("not in %d", len(cps)))
			}
		}
		fmt.Println()
	}

	fmt.Println("\n=== calc2: stage residency (sessions spent in each stage) ===")
	for _, name := range order {
		cps := results[name]
		fmt.Printf("  %s:", name)
		last := -1
		start := 1
		for _, c := range cps {
			if c.stage != last {
				if last >= 0 {
					fmt.Printf(" %s=%d", face.StageName(last), c.session-start)
				}
				last, start = c.stage, c.session
			}
		}
		fmt.Printf(" %s=%d(ongoing at n=%d)\n", face.StageName(last), len(cps)-start+1, len(cps))
	}
}

// sweepKnobs is calc4: one-dimensional sweeps of each of the four gate knobs
// over scenario A's already-measured checkpoints (no re-simulation — the
// underlying ledger is identical regardless of the gate thresholds applied
// to it).
func sweepKnobs(cps []checkpoint) {
	fmt.Println("\n=== calc4: knob sensitivity (scenario A_single, defaults evidenceGate=3 thetaCal=0.15 thetaSharp=0.2 islandMinFreq=2.5) ===")

	firstAdult := func(evidenceGate, thetaCal, thetaSharp, minFreq float64) string {
		for _, c := range cps {
			if stageForKnobs(c, evidenceGate, thetaCal, thetaSharp, minFreq) >= face.StageAdult {
				return fmt.Sprintf("session %d", c.session)
			}
		}
		return fmt.Sprintf("not reached within %d", len(cps))
	}

	type sweep struct {
		knob   string
		levels []float64
		eval   func(v float64) string
	}
	sweeps := []sweep{
		{"thetaCal", []float64{0.10, 0.15, 0.25}, func(v float64) string {
			return firstAdult(evidenceGateDefault, v, face.ThetaSharp, islandMinFreqDefault)
		}},
		{"thetaSharp", []float64{0.10, 0.20, 0.35}, func(v float64) string {
			return firstAdult(evidenceGateDefault, face.ThetaCal, v, islandMinFreqDefault)
		}},
		{"islandMinFreq", []float64{1.5, 2.5, 4.0}, func(v float64) string {
			return firstAdult(evidenceGateDefault, face.ThetaCal, face.ThetaSharp, v)
		}},
		{"evidenceGate(S1->S2)", []float64{2, 3, 5}, func(v float64) string {
			return firstAdult(v, face.ThetaCal, face.ThetaSharp, islandMinFreqDefault)
		}},
	}
	for _, sw := range sweeps {
		fmt.Printf("  %s:\n", sw.knob)
		for _, v := range sw.levels {
			fmt.Printf("    %v -> S4 at %s\n", v, sw.eval(v))
		}
	}
}

// sweepThetaCalMin is calc5: the ADR-0045 Decision 2 knob, swept over
// scenario A's measured checkpoints under the fully revised gates. The
// selection criterion (ADR-0045): with a single provider at 1 session/day,
// あかちゃん・こども・わかもの must each be observed in their own sessions —
// the stages must actually be stepped on, not skipped in one session.
func sweepThetaCalMin(cps []checkpoint) {
	fmt.Println("\n=== calc5: ThetaCalMin sweep (ADR-0045 Decision 2, scenario A_single, revised gates) ===")
	stages := []int{face.StageChick, face.StageChild, face.StageYouth, face.StageAdult}
	for _, v := range []float64{2, 4, 5, 6, 8, 10, 12} {
		arrived := map[int]int{}
		stay := map[int]int{}
		for _, c := range cps {
			st := stageRevised(c, v)
			if _, ok := arrived[st]; !ok {
				arrived[st] = c.session
			}
			stay[st]++
		}
		fmt.Printf("  ThetaCalMin=%4.1f:", v)
		for _, st := range stages {
			a := "never"
			if s, ok := arrived[st]; ok {
				a = fmt.Sprintf("s%d", s)
			}
			fmt.Printf("  %s[arrive=%s stay=%d]", face.StageName(st), a, stay[st])
		}
		fmt.Println()
	}
}

// calc6: a synthetic ledger with the REAL ledger's shape (ADR-0045
// Consequences: 41 execution experiences, effectively one provider, no
// preference) — never the real ~/.tomobit data itself. Prints the
// before/after stage so the release note's "おとな→わかもの" claim rests on
// a measurement, plus a variant with one stray second-provider run (実質1つ
// means the odd foreign row may exist) to show the prediction is not an
// artifact of perfect purity.
func calc6(dir string, t0 int64) {
	fmt.Println("\n=== calc6: real-ledger-shaped synthetic (41 exec, single provider) ===")

	s, cps := runScenario(dir, "R_realshape", []providerSpec{{"claude-code", 0.80}}, 41, 2001, t0)
	last := cps[len(cps)-1]
	fmt.Printf("  pure single provider : pre-ADR-0045 gates -> %-14s revised face.StageFrom -> %s\n",
		stageLabel(stageForKnobs(last, evidenceGateDefault, face.ThetaCal, face.ThetaSharp, islandMinFreqDefault)),
		stageLabel(last.stage))
	fmt.Printf("    gates at session 41: maxEvidence=%.2f calMean=%.3f(sumW=%.2f) islandFreq=%s islandWobble=%s rivals=%v\n",
		last.maxEvidence, last.calMean, last.sumW, fmtMap(last.freq), fmtMap(last.wobble), last.rivals)
	s.Close()

	s2 := openScratch(dir, "R_stray.db")
	en := &core.Engine{Repo: s2}
	rng := rand.New(rand.NewSource(2002))
	seq := 0
	var lastTS int64
	for i := 0; i < 41; i++ {
		ts := t0 + int64(i)*day
		applyExec(s2, en, &seq, "claude-code", rng.Float64() < 0.80, ts)
		if i == 20 { // one odd codex run in the middle — 実質1つ, not 厳密に1つ
			applyExec(s2, en, &seq, "codex", true, ts+3600_000)
		}
		lastTS = ts
	}
	stage, err := face.StageFrom(s2, lastTS)
	check(err)
	me, sw, cm, mp, fr, wb, rv := gateValues(s2, lastTS)
	strayCp := checkpoint{maxEvidence: me, sumW: sw, calMean: cm, maxPref: mp, freq: fr, wobble: wb, rivals: rv}
	fmt.Printf("  plus one stray codex : pre-ADR-0045 gates -> %-14s revised face.StageFrom -> %s\n",
		stageLabel(stageForKnobs(strayCp, evidenceGateDefault, face.ThetaCal, face.ThetaSharp, islandMinFreqDefault)),
		stageLabel(stage))
	fmt.Printf("    gates at session 41: maxEvidence=%.2f calMean=%.3f(sumW=%.2f) islandFreq=%s islandWobble=%s rivals=%v\n",
		me, cm, sw, fmtMap(fr), fmtMap(wb), rv)
	s2.Close()
}

// calc1: is isSharp's Wobble gate self-evidently 0 whenever there is only
// one provider? Checked three ways: (a) a synthetic sole provider that fails
// its own gate (fallback path, decide.go:124-136), (b) a synthetic sole
// provider that passes, (c) the real scenario ledgers.
func calc1(stores map[string]*store.Store) {
	fmt.Println("\n=== calc1: single- vs multi-provider Wobble ===")
	now := time.Now().UnixMilli()

	gatedOut := &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "cap=implement", Target: "claude-code",
		Alpha: 1, Beta: 40, PriorA: 1, PriorB: 1, LastUpdate: now, BornTS: now - 200*day,
	}
	w := decide.Wobble([]*core.Connection{gatedOut}, []string{"claude-code"}, []string{"cap=implement"}, "", sharpDraws, stageSeed, now)
	fmt.Printf("  synthetic: sole provider GATED OUT (alpha=1 beta=40)   -> Wobble=%.4f\n", w)
	fmt.Println("    (decide.go:124-136: len(passers)==0 takes the deterministic fallback — the only")
	fmt.Println("     candidate wins regardless of seed; nothing to flicker against)")

	passing := &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "cap=implement", Target: "claude-code",
		Alpha: 30, Beta: 5, PriorA: 1, PriorB: 1, LastUpdate: now, BornTS: now - 200*day,
	}
	w = decide.Wobble([]*core.Connection{passing}, []string{"claude-code"}, []string{"cap=implement"}, "", sharpDraws, stageSeed, now)
	fmt.Printf("  synthetic: sole provider PASSES (alpha=30 beta=5)      -> Wobble=%.4f\n", w)
	fmt.Println("    (decide.go:142-151: the pairwise tournament loop needs >=2 passers to run at all;")
	fmt.Println("     with len(passers)==1 the inner loop body never executes)")

	for _, name := range []string{"A_single", "B_skewed", "C_parity"} {
		s := stores[name]
		conns, err := s.AllConnections()
		check(err)
		providers, islands := capabilityProviders(conns)
		scope := core.ParseScopeKey(islands[0])
		w := decide.Wobble(conns, providers, scope, "", sharpDraws, stageSeed, now)
		fmt.Printf("  real ledger %-10s providers=%v -> Wobble(%s)=%.4f\n", name, providers, islands[0], w)
	}

	fmt.Println("\n  does \"human\" enter providers? stage.go:156-168 walks EVERY ConnCapability")
	fmt.Println("  connection's Target with no allow-list, and Experience.Target() (types.go:96-102)")
	fmt.Println("  returns e.Provider verbatim for execution experiences — so a capability connection")
	fmt.Println("  with Target==\"human\" enters providers exactly like any other string, whenever the")
	fmt.Println("  ledger has an execution experience with Provider=\"human\" (reflectprobe's S4 fixture")
	fmt.Println("  does this). Demonstrated: add one human capability connection alongside claude-code —")
	human := &core.Connection{
		Kind: core.ConnCapability, ScopeKey: "cap=implement", Target: "human",
		Alpha: 10, Beta: 2, PriorA: 1, PriorB: 1, LastUpdate: now, BornTS: now - 100*day,
	}
	twoProv := []*core.Connection{passing, human}
	providers, _ := capabilityProviders(twoProv)
	w = decide.Wobble(twoProv, providers, []string{"cap=implement"}, "", sharpDraws, stageSeed, now)
	fmt.Printf("  synthetic: claude-code (pass) + human (pass), NO preference connection -> providers=%v Wobble=%.4f\n", providers, w)
	fmt.Println("    (decide.go:212-224 firstWins: no matching preference connection -> Beta(1,1) blank-slate")
	fmt.Println("     sample every draw — the maximum-flicker case, not near 0)")
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: stagecurve <scratch-dir>")
		os.Exit(2)
	}
	dir := os.Args[1]
	check(os.MkdirAll(dir, 0o755))

	t0 := time.Now().UnixMilli() - 700*day
	const n = 90

	scenarios := []struct {
		name      string
		providers []providerSpec
		seed      int64
		prefEvery int
	}{
		{"A_single", []providerSpec{{"claude-code", 0.80}}, 1001, 0},
		{"B_skewed", []providerSpec{{"claude-code", 0.85}, {"codex", 0.55}}, 1002, 0},
		{"C_parity", []providerSpec{{"claude-code", 0.70}, {"codex", 0.70}}, 1003, 0},
		// D: B plus a preference drip — ADR-0045 Decision 1 must leave おとな
		// reachable for a Tomo that is actually given choices to settle.
		{"D_pref", []providerSpec{{"claude-code", 0.85}, {"codex", 0.55}}, 1004, 5},
	}
	order := make([]string, 0, len(scenarios))
	results := map[string][]checkpoint{}
	stores := map[string]*store.Store{}
	for _, sc := range scenarios {
		s, cps := runScenarioPref(dir, sc.name, sc.providers, n, sc.seed, t0, sc.prefEvery)
		stores[sc.name], results[sc.name] = s, cps
		order = append(order, sc.name)
		printGrowth(sc.name, cps)
	}

	calc1(stores)
	arrivalTable(results, order)

	fmt.Println("\n=== calc3: gate values, session by session (raw material — transition lines above name only the jump) ===")
	for _, name := range order {
		printDetail(name, results[name], 10)
	}

	sweepKnobs(results["A_single"])
	sweepThetaCalMin(results["A_single"])
	calc6(dir, t0)

	for _, s := range stores {
		s.Close()
	}
}
