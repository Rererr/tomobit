// Package face renders Tomo's growth stage, mood, and sprite as a View over
// the Knowledge Network (ADR-0008). Nothing here is stored: the same
// connections always reduce to the same stage and mood, and decay can walk
// a stage back down over time — that is Decision 1 working as intended, not
// a bug.
package face

import "github.com/Rererr/tomobit/internal/core"

// Growth stages (ADR-0008 Decision 2), youngest first.
const (
	StageEgg = iota
	StageChick
	StageChild
	StageYouth
	StageAdult
	StagePartner
)

var stageNames = [...]string{
	StageEgg:     "たまご",
	StageChick:   "ひよこ",
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

// Stage derives the growth stage at nowMs from the ladder in ADR-0008
// Decision 2. Checked strongest-first so a connection set that clears S5
// isn't mistakenly reported at a lower stage by an earlier, weaker match.
func Stage(conns []*core.Connection, nowMs int64) int {
	if len(conns) == 0 {
		return StageEgg
	}

	maxEvidence := 0.0
	stableCount := 0
	splitChildren := 0
	maxPreferenceEvidence := 0.0
	for _, c := range conns {
		if ev := c.Evidence(nowMs); ev > maxEvidence {
			maxEvidence = ev
		}
		switch c.Kind {
		case core.ConnCapability:
			if c.Confidence(nowMs) >= 0.5 {
				stableCount++
			}
			if c.ParentKey != "" {
				splitChildren++
			}
		case core.ConnPreference:
			if ev := c.Evidence(nowMs); ev > maxPreferenceEvidence {
				maxPreferenceEvidence = ev
			}
		}
	}

	grownUp := splitChildren >= 1 && stableCount >= 2
	switch {
	case grownUp && maxPreferenceEvidence >= 1.0:
		return StagePartner
	case grownUp:
		return StageAdult
	case stableCount >= 1 || splitChildren >= 1:
		return StageYouth
	case maxEvidence >= 3.0:
		return StageChild
	default:
		return StageChick
	}
}
