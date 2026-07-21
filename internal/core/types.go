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

// Experience kinds (SCHEMA.md D7; reflection added by ADR-0015).
const (
	KindExecution  = "execution"
	KindPreference = "preference"
	KindReflection = "reflection"
)

// Connection kinds. ConnPlan is the second bet target (ADR-0014 Decision 1:
// 台帳は賭ける対象を選ばない — the right side of a Connection is not limited
// to providers).
const (
	ConnCapability = "capability"
	ConnPreference = "preference"
	ConnPlan       = "plan"
)

// Outcome holds the raw observations of an experience. Weights are NOT
// stored here; they are resolved by OutcomeWeight (SCHEMA.md D11).
//
// Insight/Reaction belong to reflection experiences (ADR-0015). They live in
// Outcome, not Context, on purpose: a context token like "insight=split"
// would enter the Split candidate vocabulary and let the judgment partition
// capability worlds on the mirror's own bookkeeping.
type Outcome struct {
	TestsPassed *bool  `json:"tests_passed,omitempty"`
	Adopted     string `json:"adopted,omitempty"` // "as-is" | "with-edits"
	Reverted    bool   `json:"reverted,omitempty"`
	// Failed is the objective execution-failure signal (provider.error /
	// exit≠0), kept apart from Reverted on purpose (ADR-0028 Decision 5): a
	// revert is the user's subjective pushback, a failure is the process not
	// delivering — folding one into the other would blur two meanings rebuild
	// could never separate again. It is the only signal a split subtask or a
	// duel child carries (both have an empty task.finished).
	Failed    bool   `json:"failed,omitempty"`
	Verdict   string `json:"verdict,omitempty"` // "up" | "down"
	Cancelled bool   `json:"cancelled,omitempty"`
	Preferred string `json:"preferred,omitempty"` // preference kind
	Over      string `json:"over,omitempty"`      // preference kind
	Insight   string `json:"insight,omitempty"`   // reflection: candidate type told
	Reaction  string `json:"reaction,omitempty"`  // reflection: "unexpected" | "known" | "wrong"
}

// Experience is the immutable asset (SCHEMA.md: experiences table).
//
// Plan is the harness-known plan the run followed (ADR-0014) — a machine
// attribute like Provider, never asked of the model. Empty for runs that
// used no plan machinery.
type Experience struct {
	ID             string
	SessionID      string
	TS             int64 // unix ms of the underlying work
	Kind           string
	ExtractorVer   int
	ExtractorModel string
	Context        map[string]string
	Provider       string // execution only
	Plan           string // execution only (ADR-0014)
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
	// Layer 1: objective signals and the human's Feedback. Reverted and a failed
	// test lead — a differenced-out result and a red test are verdicts on the
	// deliverable itself, above an adoption grade (ADR-0003, unchanged).
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
	// Failed is the final conclusion only when no subjective Feedback was given —
	// a subtask/duel child scores y=0 off its objective error alone (ADR-0028
	// Decision 5 / C1), since its empty task.finished leaves Adopted "". But when
	// a human returned Feedback, their sovereign judgment (ADR-0018 experience
	// sovereignty) outranks a transient provider.error: the do/chat boundary asks
	// for Feedback even after a failed run, and a "文句なし" on the whole session —
	// especially a multi-turn chat that erred early and recovered — must win over
	// an earlier stray error rather than be crushed to y=0 by it.
	if o.Failed {
		return 0, true
	}
	if o.TestsPassed != nil && *o.TestsPassed {
		return 0.9, true
	}
	return 0, false
}

// Connection is a projection row: the substance is Beta(α,β) plus the lazy
// decay anchor. Everything else is a derived view (One Ledger).
//
// PriorA/PriorB are this connection's own prior (ADR-0013): a split child
// inherits Beta(μ·m₀, (1−μ)·m₀) from its parent's posterior mean, and decay
// sinks back to it — 忘却の底は白紙ではなく、家系の記憶である.
type Connection struct {
	Kind       string
	ScopeKey   string
	Target     string
	Alpha      float64
	Beta       float64
	LastUpdate int64
	BornTS     int64
	ParentKey  string
	PriorA     float64
	PriorB     float64
}

func (c *Connection) Scope() Scope { return ParseScopeKey(c.ScopeKey) }

// Prior returns the connection's own prior, falling back to Beta(1,1) for
// parentless connections and legacy rows where the pair was never set
// (ADR-0003: 親を持たずに生まれるConnectionの初期値).
func (c *Connection) Prior() (a, b float64) {
	if c.PriorA <= 0 || c.PriorB <= 0 {
		return PriorAlpha, PriorBeta
	}
	return c.PriorA, c.PriorB
}

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

// CanonValue canonicalizes a single attribute value: trim, lowercase, strip
// every Unicode control character (ESC/CSI/BEL and friends), and map '|' to
// '-'. Control chars are stripped before trimming so a stray one doesn't
// hide whitespace at the new edge. LLM-extracted values are the entry point
// for this — a hostile "value" could otherwise carry a terminal escape
// sequence all the way to a rendered scope (SCHEMA.md D5).
//
// '|' gets the same treatment for a different reason: it is the scope_key
// token separator (Scope.Key, SCHEMA.md D5). An unconstrained lang/framework/
// topic value containing it (e.g. an extractor returning "CI|CD") would
// silently re-split into two tokens on the way through ParseScopeKey,
// changing the token count and orphaning the Connection — it would never
// SubsetOf-match its own scope again. Mapped to '-' rather than dropped so
// "ci|cd" and "cicd" stay distinguishable, mirroring the control-char
// stripping being a drop only because dropping there loses nothing. '=' is
// deliberately left alone: it separates key from value within one token
// (CanonToken), not tokens within a scope_key, and stripping it would mangle
// legitimate values like "a=b" instead of protecting anything.
func CanonValue(s string) string {
	stripped := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r):
			return -1
		case r == '|':
			return '-'
		default:
			return r
		}
	}, s)
	return strings.ToLower(strings.TrimSpace(stripped))
}

// CanonToken canonicalizes a context attribute into "key=value"
// (SCHEMA.md D5: lowercase, trimmed).
func CanonToken(key, val string) string {
	return fmt.Sprintf("%s=%s", CanonValue(key), CanonValue(val))
}
