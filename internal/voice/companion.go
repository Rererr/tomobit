package voice

// Companion lines (ADR-0019): the calibrated voice and the return greeting.
// Every "emotion" here is a translation of a derived value — 嘘をつける感情
// は、感情ではない. Confidence follows the judgment's wobble (ADR-0016),
// surprise follows the ledger's excess surprisal (ADR-0002), absence follows
// lazy decay. Nothing new is stored.

import (
	"fmt"

	"github.com/Rererr/tomobit/internal/core"
)

// Knobs (ADR-0019 Consequences: 確信度の段階数・驚きの閾値).
const (
	// UnsureWobble: at or above this winner-split rate the decision speaks
	// without confidence. Two tiers for v1 — 薄い/厚い.
	UnsureWobble = 0.25

	// MissSmall / MissBig grade the reaction to a miss by the batch's
	// sharpest excess surprisal (nats).
	MissSmall = 0.2
	MissBig   = 1.0
)

// Decided is the decision-time line (ADR-0019 Decision 1): a thin posterior
// admits it, a thick one claims it. 弱音を聞くだけでどの島が未熟か分かる。
func Decided(provider string, wobble float64) string {
	if wobble >= UnsureWobble {
		return fmt.Sprintf("自信ないけど、%sでいってみる", provider)
	}
	return fmt.Sprintf("これは%sだね", provider)
}

// Missed grades the reaction to a missed prediction. ok=false when the
// surprise is too small to deserve a line.
func Missed(maxExcess float64) (text string, ok bool) {
	switch {
	case maxExcess >= MissBig:
		return "……意外だった。ちょっと考え直す", true
	case maxExcess >= MissSmall:
		return "まあ、たまにはね", true
	}
	return "", false
}

// RouteHuman is the honest routing line (ADR-0018 Decision 2): the ledger
// says the human is the better provider for this island.
func RouteHuman() string {
	return "これはあなたがやった方が早いと思う。終わったら教えて — Enterで結果を聞くよ"
}

// Okaeri is the return greeting (ADR-0019 Decision 2): the translation of
// the lazy-decay diff across an absence. faded names the island whose
// confidence thinned the most; empty means nothing noticeably faded.
func Okaeri(faded core.Scope) string {
	if len(faded) == 0 {
		return "おかえり"
	}
	return fmt.Sprintf("おかえり。しばらく見ない間に、%sの自信がちょっと薄れた。勘を取り戻すから、最初は軽いのから頼む",
		ScopeDisplay(faded))
}
