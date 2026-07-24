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

// StageFrom derives the growth stage from the whole Knowledge Network
// (ADR-0017, precision-revised by ADR-0045): quantity gates for the infancy
// (S0〜S2 — 卵は較正しようがない, 幼少期は喰って育つ), calibration for S3,
// calibration + sharpness for S4, human-pair preference evidence for S5.
func StageFrom(repo core.Repo, nowMs int64) (int, error) {
	conns, err := repo.AllConnections()
	if err != nil {
		return 0, err
	}
	if len(conns) == 0 {
		return StageEgg, nil
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
	if maxEvidence < 3 {
		return StageChick, nil
	}

	calibrated, err := isCalibrated(repo, conns, nowMs)
	if err != nil {
		return 0, err
	}
	if !calibrated {
		return StageChild, nil
	}

	sharp, err := isSharp(repo, conns, nowMs)
	if err != nil {
		return 0, err
	}
	if !sharp {
		return StageYouth, nil
	}

	// The S5 threshold is the curiosity re-ask ceiling (core.PrefKnownMin ==
	// curiosity.EMax), not 1.0: one answered question leaves the human-pair
	// evidence at exactly 1.0, which decay pushes below 1.0 the next day
	// while the gap stays closed until 0.5 — a ≥1.0 gate would make あいぼう
	// a stage that expires overnight and cannot be re-earned for ~89 days
	// (ADR-0045 Decision 3 追記). At EMax the stage holds exactly as long as
	// Tomo would not want to ask that question again.
	if maxHumanPrefEvidence >= core.PrefKnownMin {
		return StagePartner, nil
	}
	return StageAdult, nil
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

// isCalibrated is the S3 gate: the decayed mean of excess surprisal across
// every ledger entry stays at or below ThetaCal. The excess surprisal is
// zero-mean under calibration by construction (ADR-0002), so this measures
// 「70%成功すると思った場面で、本当に7割成功したか」. Less than ThetaCalMin
// of decayed mass means the mean is a claim without a sample — calibration
// is undefined, and undefined is not calibrated (ADR-0045 Decision 2).
func isCalibrated(repo core.Repo, conns []*core.Connection, nowMs int64) (bool, error) {
	var sumWS, sumW float64
	for _, c := range conns {
		entries, err := repo.LedgerFor(c.Kind, c.ScopeKey, c.Target)
		if err != nil {
			return false, err
		}
		for _, e := range entries {
			w := core.DecayFactor(e.TS, nowMs)
			sumWS += w * e.SExcess
			sumW += w
		}
	}
	if sumW < ThetaCalMin {
		return false, nil
	}
	return sumWS/sumW <= ThetaCal, nil
}

// isSharp is the S4 gate: on every contested frequent island the judgment
// lottery barely wobbles (ADR-0016's flicker, ADR-0017: 迷わず、しかも当たる).
// Only islands where two or more targets hold applicable capability
// knowledge count (ADR-0045 Decision 1): with a single candidate the
// tournament inside decide.Wobble never runs and the lottery is structurally
// still — 一人しか候補がいない選挙の支持率100% is not a measurement. No
// contested frequent island means sharpness is undefined — not sharp, the
// same honesty isCalibrated already applies to an empty sample.
func isSharp(repo core.Repo, conns []*core.Connection, nowMs int64) (bool, error) {
	exps, err := repo.CurrentExperiences()
	if err != nil {
		return false, err
	}

	islandSet := map[string]bool{}
	for _, c := range conns {
		if c.Kind == core.ConnCapability {
			islandSet[c.ScopeKey] = true
		}
	}

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
		w := decide.Wobble(conns, rivals, scope, "", sharpDraws, stageSeed, nowMs)
		if w > ThetaSharp {
			return false, nil
		}
	}
	return contested > 0, nil
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
