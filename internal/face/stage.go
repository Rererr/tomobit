// Package face derives Tomo's growth stage and mood as a View over the
// Knowledge Network (ADR-0008). Nothing here is stored: the same connections
// always reduce to the same stage and mood, and a changed world can walk a
// stage back down — that is honesty, not a bug. The stage/mood these produce
// drive the desktop window's sprite (its shape and face, ADR-0025); the
// terminal shows the stage only when it changes (voice.Growth), no longer as a
// standing `Tomo · <stage>` line.
package face

import (
	"strings"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/decide"
)

// Growth stages (ADR-0008 Decision 2), youngest first.
const (
	StageEgg = iota
	StageChick
	StageChild
	StageYouth
	StageAdult
	StagePartner
)

// The Go constants keep their v1 bird names (StageEgg, StageChick) — they
// are internal identifiers. The labels follow the v2 dog design
// (SPRITES-WINDOW.md: たまご改め毛玉).
var stageNames = [...]string{
	StageEgg:     "毛玉",
	StageChick:   "あかちゃん",
	StageChild:   "こども",
	StageYouth:   "わかもの",
	StageAdult:   "おとな",
	StagePartner: "あいぼう",
}

// StageName returns the Japanese label for a stage, or "" if out of range.
func StageName(stage int) string {
	if stage < 0 || stage >= len(stageNames) {
		return ""
	}
	return stageNames[stage]
}

// Knobs (ADR-0017 Consequences: 実測で決める — these are the starting
// values, tuned via dogfood).
const (
	// ThetaEvidence is the decayed evidence a single connection needs for
	// S2 (ADR-0017: 幼少期は喰って育つ — a quantity gate, not a quality one).
	ThetaEvidence = 3.0

	// ThetaCal caps the decayed mean excess surprisal for S3: a calibrated
	// forecaster self-balances near 0 (ADR-0002), so a mean持続的に above
	// this is "predicting a world that isn't there".
	ThetaCal = 0.15

	// ThetaCalMin is the decayed ledger mass isCalibrated needs before its
	// mean is a claim at all (ADR-0045 Decision 2): calibration is a
	// statistical statement, and a statement needs a sample. Below this the
	// gate answers 未定義＝不成立 — which is what makes こども a stage that
	// actually gets stepped on. Value from the stagecurve sweep (ADR-0045
	// ノブの較正): at 8 the single-provider 1/day curve spends 5 sessions in
	// こども instead of skipping it.
	ThetaCalMin = 8.0

	// ThetaSharp caps the judgment wobble on every frequent island for S4.
	// A blank lottery wobbles near 0.5; a settled one near 0.
	ThetaSharp = 0.2

	// islandMinFreq is the decayed arrival frequency that makes a scope a
	// 頻繁な島 — shared with the Curiosity gate's FMin (ADR-0017:
	// 「頻繁な島」の定義は到来頻度、VoIと共有).
	islandMinFreq = 2.5

	// sharpDraws / stageSeed: the sharpness lottery's M and fixed seed. The
	// seed is constant so the stage stays a deterministic View — the same
	// database always shows the same face.
	sharpDraws = 128
	stageSeed  = 1
)

// Gate names in the growth view (ADR-0046 Decision 1). They are the JSON
// vocabulary GUI matches on, so they live here as constants rather than as
// ad-hoc literals in the renderer.
const (
	GateConnection          = "connection"
	GateEvidence            = "evidence"
	GateCalibrationSample   = "calibration_sample"
	GateCalibration         = "calibration"
	GateSharpness           = "sharpness"
	GatePreferenceWithHuman = "preference_with_human"
)

// Gate is one stage gate exactly as the stage decision evaluated it: the
// value it read, the threshold it compared against, and whether it held
// (ADR-0046 Decision 1 — nothing here is computed for the view's sake).
// Value is nil when the gate is unmeasurable — ADR-0045 Decision 1's
// 「競争のある島が無い」 is 測定不能, not 未達, and the two must not wear
// the same face.
type Gate struct {
	Name      string
	Value     *float64
	Threshold float64
	Met       bool
}

// Hint translates an unmet gate into the one action that moves its value
// (ADR-0046 Decision 3): a fixed Go template, no LLM (ADR-0007 Decision 4).
// This is disclosure, not instruction — the caller shows it, the user picks.
// Met gates need no hint, and an unknown gate name yields "" rather than a
// made-up move.
func (g Gate) Hint() string {
	if g.Met {
		return ""
	}
	switch g.Name {
	case GateConnection, GateEvidence, GateCalibrationSample:
		return "もっと一緒に仕事をする"
	case GateCalibration:
		if g.Value == nil {
			// No ledger mass at all: the mean is not off, it does not
			// exist yet — same move as the sample gate, not the ズレ line.
			return "もっと一緒に仕事をする"
		}
		return "予測が外れている文脈がある"
	case GateSharpness:
		if g.Value == nil {
			return "二人目のProviderに会わせる（autoに任せる）"
		}
		return "duelや質問に答えて好みを教える"
	case GatePreferenceWithHuman:
		return "自分でやった仕事をTomoに見せる（--provider human）"
	}
	return ""
}

// Growth is the stage plus the gate evaluation that decided it — what
// StageFrom always computed and used to throw away (ADR-0046 Context).
// Gates holds, in ladder order, every gate the next stage requires (gates
// are cumulative), so a renderer can show 「次の段に何が足りないか」 without
// re-deriving anything. Still a View: derived, never stored (ADR-0008).
type Growth struct {
	Stage int
	Gates []Gate
}

// Next returns the stage Gates are the requirements for. ok is false at the
// top — あいぼう has no next, and no fake 100% progress either (ADR-0046
// Decision 1).
func (g Growth) Next() (next int, ok bool) {
	if g.Stage >= StagePartner {
		return 0, false
	}
	return g.Stage + 1, true
}

// StageFrom derives the growth stage from the whole Knowledge Network
// (ADR-0017, precision-revised by ADR-0045): quantity gates for the infancy
// (S0〜S2 — 卵は較正しようがない, 幼少期は喰って育つ), calibration for S3,
// calibration + sharpness for S4, human-pair preference evidence for S5.
func StageFrom(repo core.Repo, nowMs int64) (int, error) {
	g, err := GrowthFrom(repo, nowMs)
	if err != nil {
		return 0, err
	}
	return g.Stage, nil
}

// measured pins a gate value at evaluation time — Gate.Value aliasing a
// variable the ladder keeps updating would let the view drift from what the
// decision actually read.
func measured(v float64) *float64 { return &v }

// GrowthFrom runs the stage ladder and keeps the evaluation instead of
// discarding it (ADR-0046 Decision 1). The ladder stops at the first unmet
// gate exactly as StageFrom always did, so Gates never contains a gate the
// next stage does not require — a わかもの is not shown the S5 preference
// gate as if it were the next step.
func GrowthFrom(repo core.Repo, nowMs int64) (Growth, error) {
	conns, err := repo.AllConnections()
	if err != nil {
		return Growth{}, err
	}

	// S0→S1's gate is the birth of any connection — not evidence≥ThetaEvidence,
	// which is the S1→S2 gate. Disclosing the evidence gate here would
	// overstate the distance: the first job hatches the egg even at
	// evidence < ThetaEvidence, and a demand the next stage does not make is
	// the 偽の遠さ mirror of the fake progress ADR-0046 refuses.
	connection := Gate{
		Name: GateConnection, Value: measured(float64(len(conns))),
		Threshold: 1, Met: len(conns) >= 1,
	}
	if !connection.Met {
		return Growth{Stage: StageEgg, Gates: []Gate{connection}}, nil
	}

	maxEvidence := 0.0
	maxHumanPrefEvidence := 0.0
	for _, c := range conns {
		if ev := c.Evidence(nowMs); ev > maxEvidence {
			maxEvidence = ev
		}
		if c.Kind == core.ConnPreference && pairHasHuman(c.Target) {
			if ev := c.Evidence(nowMs); ev > maxHumanPrefEvidence {
				maxHumanPrefEvidence = ev
			}
		}
	}
	evidence := Gate{
		Name: GateEvidence, Value: measured(maxEvidence),
		Threshold: ThetaEvidence, Met: maxEvidence >= ThetaEvidence,
	}
	if !evidence.Met {
		return Growth{Stage: StageChick, Gates: []Gate{evidence}}, nil
	}

	calSample, calMean, err := calibrationGates(repo, conns, nowMs)
	if err != nil {
		return Growth{}, err
	}
	if !calSample.Met || !calMean.Met {
		return Growth{Stage: StageChild, Gates: []Gate{evidence, calSample, calMean}}, nil
	}

	sharpness, err := sharpnessGate(repo, conns, nowMs)
	if err != nil {
		return Growth{}, err
	}
	if !sharpness.Met {
		return Growth{Stage: StageYouth, Gates: []Gate{evidence, calSample, calMean, sharpness}}, nil
	}

	// The S5 threshold is the curiosity re-ask ceiling (core.PrefKnownMin ==
	// curiosity.EMax), not 1.0: one answered question leaves the human-pair
	// evidence at exactly 1.0, which decay pushes below 1.0 the next day
	// while the gap stays closed until 0.5 — a ≥1.0 gate would make あいぼう
	// a stage that expires overnight and cannot be re-earned for ~89 days
	// (ADR-0045 Decision 3 追記). At EMax the stage holds exactly as long as
	// Tomo would not want to ask that question again.
	pref := Gate{
		Name: GatePreferenceWithHuman, Value: measured(maxHumanPrefEvidence),
		Threshold: core.PrefKnownMin, Met: maxHumanPrefEvidence >= core.PrefKnownMin,
	}
	if pref.Met {
		// No Gates at the top: there is no next stage to need them, and an
		// all-met list would be the 「達成率100%」 ADR-0046 refuses to fake.
		return Growth{Stage: StagePartner}, nil
	}
	return Growth{Stage: StageAdult, Gates: []Gate{evidence, calSample, calMean, sharpness, pref}}, nil
}

// humanTarget is the ledger's name for the user on the provider side —
// the same literal the duel/chat/reflection paths use (ADR-0018: human runs
// on the same ledger).
const humanTarget = "human"

// pairHasHuman reports whether a canonical "a~b" preference target
// (core.Experience.Target) has the human on either side. Knowing your own
// user's 得手不得手 is what separates あいぼう from おとな (ADR-0045
// Decision 3, ADR-0018 Decision 4: Companionshipの核心).
func pairHasHuman(target string) bool {
	for _, side := range strings.Split(target, "~") {
		if side == humanTarget {
			return true
		}
	}
	return false
}

// calibrationGates is the S3 gate, split along its two failure reasons
// (ADR-0046 Decision 1): the decayed ledger mass (a mean below ThetaCalMin
// is a claim without a sample — calibration is undefined, and undefined is
// not calibrated, ADR-0045 Decision 2) and the decayed mean of excess
// surprisal itself, which under calibration is zero-mean by construction
// (ADR-0002) and so measures 「70%成功すると思った場面で、本当に7割成功
// したか」. One combined bool could not tell 標本不足 from 予測のズレ, and
// the two ask for different moves (ADR-0046 Decision 3). With no mass at
// all the mean is not low or high but nonexistent — nil, not 0.
func calibrationGates(repo core.Repo, conns []*core.Connection, nowMs int64) (sample, mean Gate, err error) {
	var sumWS, sumW float64
	for _, c := range conns {
		entries, err := repo.LedgerFor(c.Kind, c.ScopeKey, c.Target)
		if err != nil {
			return Gate{}, Gate{}, err
		}
		for _, e := range entries {
			w := core.DecayFactor(e.TS, nowMs)
			sumWS += w * e.SExcess
			sumW += w
		}
	}
	sample = Gate{Name: GateCalibrationSample, Value: measured(sumW), Threshold: ThetaCalMin, Met: sumW >= ThetaCalMin}
	mean = Gate{Name: GateCalibration, Threshold: ThetaCal}
	if sumW > 0 {
		m := sumWS / sumW
		mean.Value = &m
		mean.Met = m <= ThetaCal
	}
	return sample, mean, nil
}

// sharpnessGate is the S4 gate: on every contested frequent island the
// judgment lottery barely wobbles (ADR-0016's flicker, ADR-0017: 迷わず、
// しかも当たる). Only islands where two or more targets hold applicable
// capability knowledge count (ADR-0045 Decision 1): with a single candidate
// the tournament inside decide.Wobble never runs and the lottery is
// structurally still — 一人しか候補がいない選挙の支持率100% is not a
// measurement. No contested frequent island means sharpness is undefined —
// Value nil, not a low score, the same honesty calibrationGates applies to
// an empty sample. The gate's value is the worst (largest) wobble: the
// decision fails on any island over ThetaSharp, so the binding island is
// the one worth disclosing. Every contested island is evaluated instead of
// stopping at the first failure — the decision is unchanged (max > θ iff
// any > θ) and, unlike a first-hit value, the max does not depend on map
// iteration order, which the View property requires.
func sharpnessGate(repo core.Repo, conns []*core.Connection, nowMs int64) (Gate, error) {
	exps, err := repo.CurrentExperiences()
	if err != nil {
		return Gate{}, err
	}

	islandSet := map[string]bool{}
	for _, c := range conns {
		if c.Kind == core.ConnCapability {
			islandSet[c.ScopeKey] = true
		}
	}

	gate := Gate{Name: GateSharpness, Threshold: ThetaSharp}
	worst := 0.0
	contested := 0
	for key := range islandSet {
		scope := core.ParseScopeKey(key)
		freq := 0.0
		for _, e := range exps {
			if e.Kind == core.KindExecution && scope.SubsetOf(e.Tokens()) {
				freq += core.DecayFactor(e.TS, nowMs)
			}
		}
		if freq < islandMinFreq {
			continue
		}
		rivals := islandRivals(conns, scope)
		if len(rivals) < 2 {
			continue
		}
		contested++
		if w := decide.Wobble(conns, rivals, scope, "", sharpDraws, stageSeed, nowMs); w > worst {
			worst = w
		}
	}
	if contested == 0 {
		return gate, nil
	}
	gate.Value = &worst
	gate.Met = worst <= ThetaSharp
	return gate, nil
}

// islandRivals lists the targets whose capability knowledge applies to the
// island's scope — the subset match a real Choose on these tokens reads
// (decide.finestMatch), not an exact scope-key match: a rival betting from a
// coarser connection still competes in this island's lottery. Order is
// irrelevant downstream (Choose sorts its candidates).
func islandRivals(conns []*core.Connection, scope core.Scope) []string {
	set := map[string]bool{}
	for _, c := range conns {
		if c.Kind == core.ConnCapability && c.Scope().SubsetOf(scope) {
			set[c.Target] = true
		}
	}
	rivals := make([]string, 0, len(set))
	for t := range set {
		rivals = append(rivals, t)
	}
	return rivals
}
