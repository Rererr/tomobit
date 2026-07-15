// Package core implements the Connection Engine: decaying Beta posteriors,
// the surprise ledger, and the split/merge judgment (ADR-0001..0003).
package core

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Experience kinds (SCHEMA.md D7).
const (
	KindExecution  = "execution"
	KindPreference = "preference"
)

// Connection kinds.
const (
	ConnCapability = "capability"
	ConnPreference = "preference"
)

// Outcome holds the raw observations of an experience. Weights are NOT
// stored here; they are resolved by OutcomeWeight (SCHEMA.md D11).
type Outcome struct {
	TestsPassed *bool  `json:"tests_passed,omitempty"`
	Adopted     string `json:"adopted,omitempty"` // "as-is" | "with-edits"
	Reverted    bool   `json:"reverted,omitempty"`
	Verdict     string `json:"verdict,omitempty"` // "up" | "down"
	Cancelled   bool   `json:"cancelled,omitempty"`
	Preferred   string `json:"preferred,omitempty"` // preference kind
	Over        string `json:"over,omitempty"`      // preference kind
}

// Experience is the immutable asset (SCHEMA.md: experiences table).
type Experience struct {
	ID             string
	SessionID      string
	TS             int64 // unix ms of the underlying work
	Kind           string
	ExtractorVer   int
	ExtractorModel string
	Context        map[string]string
	Provider       string // execution only
	Outcome        Outcome
	Source         string // "production" | "learning"
}

// ConnKind maps an experience kind to the connection kind it feeds.
func (e *Experience) ConnKind() string {
	if e.Kind == KindPreference {
		return ConnPreference
	}
	return ConnCapability
}

// canonicalPair orders two preference kinds lexicographically into the
// "a~b" target, so the pair is identified regardless of which side won.
// preferredIsFirst reports whether the preferred kind is the lexical first,
// which fixes the outcome direction (win on side a => y=1).
func canonicalPair(preferred, over string) (target string, preferredIsFirst bool) {
	a, b := preferred, over
	if a > b {
		a, b = b, a
	}
	return a + "~" + b, preferred == a
}

// Target returns the connection target this experience is evidence for:
// the provider for execution, the canonical pair "a~b" for preference.
func (e *Experience) Target() string {
	if e.Kind == KindPreference {
		target, _ := canonicalPair(e.Outcome.Preferred, e.Outcome.Over)
		return target
	}
	return e.Provider
}

// Tokens returns the canonical attribute tokens of the context.
func (e *Experience) Tokens() []string {
	tokens := make([]string, 0, len(e.Context))
	for k, v := range e.Context {
		if v == "" {
			continue
		}
		tokens = append(tokens, CanonToken(k, v))
	}
	sort.Strings(tokens)
	return tokens
}

// OutcomeWeight resolves raw observations into y in [0,1] (ADR-0003 layer
// weights). Pure function: change it and `tomobit rebuild` reinterprets
// all history.
//
// Returns ok=false when the experience carries no usable outcome signal
// (e.g. cancelled).
func OutcomeWeight(e *Experience) (y float64, ok bool) {
	o := e.Outcome
	if e.Kind == KindPreference {
		if o.Preferred == "" || o.Over == "" {
			return 0, false
		}
		if _, preferredIsFirst := canonicalPair(o.Preferred, o.Over); preferredIsFirst {
			return 1, true
		}
		return 0, true
	}
	// Cancelled is checked before Verdict: a cancelled task never produced a
	// deliverable, so there is nothing the verdict could be judging — the
	// signal is not usable even when a verdict field happens to be present.
	if o.Cancelled {
		return 0, false
	}
	// Layer 2: explicit verdict overrides everything.
	switch o.Verdict {
	case "up":
		return 1, true
	case "down":
		return 0, true
	}
	// Layer 1: objective signals.
	if o.Reverted {
		return 0, true
	}
	if o.TestsPassed != nil && !*o.TestsPassed {
		return 0, true
	}
	switch o.Adopted {
	case "as-is":
		return 1.0, true
	case "with-edits":
		return 0.7, true
	}
	if o.TestsPassed != nil && *o.TestsPassed {
		return 0.9, true
	}
	return 0, false
}

// Connection is a projection row: the substance is Beta(α,β) plus the lazy
// decay anchor. Everything else is a derived view (One Ledger).
type Connection struct {
	Kind       string
	ScopeKey   string
	Target     string
	Alpha      float64
	Beta       float64
	LastUpdate int64
	BornTS     int64
	ParentKey  string
}

func (c *Connection) Scope() Scope { return ParseScopeKey(c.ScopeKey) }

// LedgerEntry is one row of the surprise ledger (ADR-0002).
type LedgerEntry struct {
	Kind         string
	ScopeKey     string
	Target       string
	ExperienceID string
	TS           int64
	P            float64
	Y            float64
	SExcess      float64
}

// MarshalContext / MarshalOutcome keep JSON canonical for storage.
func MarshalContext(ctx map[string]string) string {
	b, _ := json.Marshal(ctx)
	return string(b)
}

func MarshalOutcome(o Outcome) string {
	b, _ := json.Marshal(o)
	return string(b)
}

// CanonValue canonicalizes a single attribute value: trim, lowercase, and
// strip every Unicode control character (ESC/CSI/BEL and friends). Control
// chars are stripped before trimming so a stray one doesn't hide whitespace
// at the new edge. LLM-extracted values are the entry point for this — a
// hostile "value" could otherwise carry a terminal escape sequence all the
// way to a rendered scope (SCHEMA.md D5).
func CanonValue(s string) string {
	stripped := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.ToLower(strings.TrimSpace(stripped))
}

// CanonToken canonicalizes a context attribute into "key=value"
// (SCHEMA.md D5: lowercase, trimmed).
func CanonToken(key, val string) string {
	return fmt.Sprintf("%s=%s", CanonValue(key), CanonValue(val))
}
