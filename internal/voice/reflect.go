package voice

// Reflection lines (ADR-0015): the mirror's tellings, one template per
// ledger-event type, plus the reaction prompt wording (ADR-0015 knob:
// 反応の選択肢の文言はvoice管轄). Deterministic templates for v1 — the LLM's
// verbalization seat (Decision 4) stays reserved, but the mirror must not
// depend on Ollama being awake to speak.

import (
	"fmt"

	"github.com/Rererr/tomobit/internal/core"
)

// Reflection candidate types — shared vocabulary between the detector, the
// mirror ledger (Outcome.Insight), and these templates.
const (
	InsightSplit         = "split"
	InsightReversal      = "reversal"
	InsightQuestioned    = "questioned"
	InsightRehabilitated = "rehabilitated"
	InsightReperceived   = "reperceived" // 第5のトリガー (ADR-0019 Decision 4)
)

// ReflectSplit tells a Split birth: a meaningful distinction was discovered.
// diff is the distinguishing scope (child minus parent).
func ReflectSplit(diff core.Scope, provider string) string {
	return fmt.Sprintf("%sは別の話なんだと気付いたんだ。%sとの相性、そこだけ違う",
		ScopeDisplay(diff), provider)
}

// ReflectReversal tells a rank crossing: winner overtook loser at scope.
func ReflectReversal(scope core.Scope, winner, loser string) string {
	return fmt.Sprintf("%sだと、最近は%sより%sの方がうまくいってるみたいだ",
		ScopeDisplay(scope), loser, winner)
}

// ReflectQuestioned tells a surprise-ledger surfacing: what used to work is
// drifting.
func ReflectQuestioned(scope core.Scope, provider string) string {
	return fmt.Sprintf("%sと%s、前は効いてたのに最近崩れてきてる気がする",
		ScopeDisplay(scope), provider)
}

// ReflectRehabilitated tells a gate re-entry (ADR-0012 Decision 3): a
// benched provider earned its way back.
func ReflectRehabilitated(scope core.Scope, provider string) string {
	return fmt.Sprintf("しばらく頼んでなかった%s、%sでまた良さそうだよ",
		provider, ScopeDisplay(scope))
}

// ReflectHumanReversal tells the mirror's proudest line (ADR-0019 Decision
// 3 / ADR-0018 Decision 4): the user's own success rate overtook Tomo's
// providers on an island they used to delegate.
func ReflectHumanReversal(scope core.Scope, over string) string {
	return fmt.Sprintf("%sのこと、前は%sに振ってたけど、最近は自分でやった方がうまくいってるね",
		ScopeDisplay(scope), over)
}

// ReflectReperceived tells a re-perception diff (ADR-0019 Decision 4): idle
// work re-read the past and the attribution moved. oldTok/newTok are the
// display values that changed; both empty falls back to the generic line.
func ReflectReperceived(oldTok, newTok string) string {
	if oldTok != "" && newTok != "" {
		return fmt.Sprintf("前の仕事、見直してたんだ。%sのせいじゃなくて、%sのせいだったかもしれない",
			oldTok, newTok)
	}
	return "前の仕事、見直してたんだ。少し違って見えたよ"
}

// ReflectPrompt is the reaction question shown after a telling. The answer
// becomes a Learning Reality (ADR-0015 Decision 3); skipping is free.
func ReflectPrompt() string {
	return "どうだった? [1=意外 / 2=知ってた / 3=それ違う / Enter=スキップ] "
}
