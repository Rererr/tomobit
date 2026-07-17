// Package face derives Tomo's growth stage and mood as a View over the
// Knowledge Network (ADR-0008). Nothing here is stored: the same connections
// always reduce to the same stage and mood, and a changed world can walk a
// stage back down — that is honesty, not a bug. The stage/mood these produce
// drive the desktop window's sprite (its shape and face, ADR-0025); the
// terminal shows the stage only when it changes (voice.Growth), no longer as a
// standing `Tomo · <stage>` line.
package face

import (
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
// (ADR-0017): quantity gates for the infancy (S0〜S2 — 卵は較正しようがない,
// 幼少期は喰って育つ), calibration for S3, calibration + sharpness for S4,
// preference evidence for S5.
func StageFrom(repo core.Repo, nowMs int64) (int, error) {
	conns, err := repo.AllConnections()
	if err != nil {
		return 0, err
	}
	if len(conns) == 0 {
		return StageEgg, nil
	}

	maxEvidence := 0.0
	maxPreferenceEvidence := 0.0
	for _, c := range conns {
		if ev := c.Evidence(nowMs); ev > maxEvidence {
			maxEvidence = ev
		}
		if c.Kind == core.ConnPreference {
			if ev := c.Evidence(nowMs); ev > maxPreferenceEvidence {
				maxPreferenceEvidence = ev
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

	if maxPreferenceEvidence >= 1.0 {
		return StagePartner, nil
	}
	return StageAdult, nil
}

// isCalibrated is the S3 gate: the decayed mean of excess surprisal across
// every ledger entry stays at or below ThetaCal. The excess surprisal is
// zero-mean under calibration by construction (ADR-0002), so this measures
// 「70%成功すると思った場面で、本当に7割成功したか」. No ledger data at all
// means calibration is undefined — not calibrated.
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
	if sumW <= 0 {
		return false, nil
	}
	return sumWS/sumW <= ThetaCal, nil
}

// isSharp is the S4 gate: on every frequent island the judgment lottery
// barely wobbles (ADR-0016's flicker, ADR-0017: 迷わず、しかも当たる). At
// least one frequent island must exist — a Tomo whose contexts are all rare
// has nowhere to demonstrate sharpness yet.
func isSharp(repo core.Repo, conns []*core.Connection, nowMs int64) (bool, error) {
	exps, err := repo.CurrentExperiences()
	if err != nil {
		return false, err
	}

	// Every provider Tomo has capability knowledge about competes in the
	// island lotteries; islands are the capability scopes themselves.
	providerSet := map[string]bool{}
	islandSet := map[string]bool{}
	for _, c := range conns {
		if c.Kind != core.ConnCapability {
			continue
		}
		providerSet[c.Target] = true
		islandSet[c.ScopeKey] = true
	}
	providers := make([]string, 0, len(providerSet))
	for p := range providerSet {
		providers = append(providers, p)
	}

	frequent := 0
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
		frequent++
		w := decide.Wobble(conns, providers, scope, "", sharpDraws, stageSeed, nowMs)
		if w > ThetaSharp {
			return false, nil
		}
	}
	return frequent > 0, nil
}
