// Package voice renders Tomo's spoken lines (ADR-0009): deterministic Go
// templates over Connection/Experience/Stage Views, no LLM. Every string
// Tomo can say lives here, so i18n later has exactly one package to touch
// and a text change can never leak into perception or judgment.
package voice

import (
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
