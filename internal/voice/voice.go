// Package voice renders Tomo's spoken lines (ADR-0009): deterministic Go
// templates over Connection/Experience/Stage Views, no LLM. Every string
// Tomo can say lives here, so i18n later has exactly one package to touch
// and a text change can never leak into perception or judgment.
package voice

import (
	"fmt"
	"strings"

	"github.com/Rererr/tomobit/internal/core"
)

// ScopeDisplay renders a scope for speech as values only (ADR-0009 Decision
// 2): "lang=go" → "go". The key stays on the status table; a token with no
// "=" (defensive — every scope token is produced by core.CanonToken) passes
// through unchanged rather than vanishing.
func ScopeDisplay(scope core.Scope) string {
	vals := make([]string, len(scope))
	for i, tok := range scope {
		if _, v, ok := strings.Cut(tok, "="); ok {
			vals[i] = v
		} else {
			vals[i] = tok
		}
	}
	return strings.Join(vals, "・")
}

// FirstMeeting is catalog #6 (ADR-0009 Decision 2): the one line an empty
// Knowledge Network speaks in the companion view. Silence on an empty
// network would read as the companion's absence, not its honesty.
func FirstMeeting() string {
	return "「はじめまして。まだなにも知らないんだ」"
}

// Asked is the Curiosity question line (catalog #5, ADR-0007 Decision 4).
// curiosity.Ask wraps it with the terminal's answer options; the face window
// re-derives the same line from a tomo.asked event's payload (ADR-0020
// Decision 2: 同じ声のもう一つのスピーカー — 回答チャネルは端末のまま).
func Asked(scope core.Scope, a, b string) string {
	return fmt.Sprintf("最近 %s で %s と %s 両方使ってるけど、どっちが好みだった?",
		ScopeDisplay(scope), a, b)
}
