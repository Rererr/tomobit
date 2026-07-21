package core

import (
	"reflect"
	"strings"
	"testing"
)

func boolp(b bool) *bool { return &b }

func TestOutcomeWeight(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		outcome Outcome
		wantY   float64
		wantOK  bool
	}{
		{"preference preferred sorts first", KindPreference, Outcome{Preferred: "a", Over: "b"}, 1, true},
		{"preference preferred sorts second", KindPreference, Outcome{Preferred: "b", Over: "a"}, 0, true},
		{"preference missing side", KindPreference, Outcome{Preferred: "a"}, 0, false},
		{"preference both empty", KindPreference, Outcome{}, 0, false},
		{"execution cancelled", KindExecution, Outcome{Cancelled: true, Adopted: "as-is"}, 0, false},
		{"verdict up overrides objective signals", KindExecution, Outcome{Verdict: "up", Reverted: true, TestsPassed: boolp(false)}, 1, true},
		{"verdict down overrides adoption", KindExecution, Outcome{Verdict: "down", Adopted: "as-is"}, 0, true},
		// ADR-0028 Decision 5 (C2): the objective failure signal scores 0 only
		// when no subjective Feedback was given — a subtask/duel child (Adopted
		// "") scores 0 off its error alone. But a human's Feedback outranks a
		// transient provider.error (ADR-0018 experience sovereignty): a graded
		// session keeps its grade rather than being crushed to 0 by an early
		// error it recovered from. Failure stays under an explicit verdict and
		// under Cancelled (a cancel is no signal at all).
		{"failed alone (subtask/duel child) scores zero", KindExecution, Outcome{Failed: true}, 0, true},
		{"failed but adopted as-is: the human's Feedback wins", KindExecution, Outcome{Failed: true, Adopted: "as-is"}, 1.0, true},
		{"failed but adopted with-edits: the human's Feedback wins", KindExecution, Outcome{Failed: true, Adopted: "with-edits"}, 0.7, true},
		{"failed and reverted scores zero", KindExecution, Outcome{Failed: true, Reverted: true}, 0, true},
		{"verdict up overrides failure", KindExecution, Outcome{Verdict: "up", Failed: true}, 1, true},
		{"cancelled precedes failure", KindExecution, Outcome{Cancelled: true, Failed: true}, 0, false},
		{"reverted", KindExecution, Outcome{Reverted: true, Adopted: "as-is"}, 0, true},
		{"tests failed", KindExecution, Outcome{TestsPassed: boolp(false), Adopted: "as-is"}, 0, true},
		{"adopted as-is", KindExecution, Outcome{Adopted: "as-is"}, 1.0, true},
		{"adopted with-edits", KindExecution, Outcome{Adopted: "with-edits"}, 0.7, true},
		{"tests passed only", KindExecution, Outcome{TestsPassed: boolp(true)}, 0.9, true},
		{"no signal", KindExecution, Outcome{}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Experience{Kind: tt.kind, Outcome: tt.outcome}
			y, ok := OutcomeWeight(e)
			if ok != tt.wantOK {
				t.Fatalf("ok: got %v, want %v", ok, tt.wantOK)
			}
			if ok && y != tt.wantY {
				t.Errorf("y: got %v, want %v", y, tt.wantY)
			}
		})
	}
}

func TestTargetForExecutionIsProvider(t *testing.T) {
	e := &Experience{Kind: KindExecution, Provider: "claude"}
	if got := e.Target(); got != "claude" {
		t.Errorf("got %q, want %q", got, "claude")
	}
}

func TestTargetForPreferenceIsLexicographicPair(t *testing.T) {
	a := &Experience{Kind: KindPreference, Outcome: Outcome{Preferred: "vue", Over: "react"}}
	b := &Experience{Kind: KindPreference, Outcome: Outcome{Preferred: "react", Over: "vue"}}
	if got := a.Target(); got != "react~vue" {
		t.Errorf("preferred>over: got %q, want %q", got, "react~vue")
	}
	if a.Target() != b.Target() {
		t.Errorf("pair not order-invariant: %q vs %q", a.Target(), b.Target())
	}
}

func TestTokensSkipsEmptyLowercasesAndSorts(t *testing.T) {
	e := &Experience{Context: map[string]string{"framework": "", "Lang": "Rust", "Cap": " Impl "}}
	got := e.Tokens()
	want := []string{"cap=impl", "lang=rust"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCanonTokenLowercasesAndTrims(t *testing.T) {
	if got := CanonToken(" Lang ", " Rust "); got != "lang=rust" {
		t.Errorf("got %q, want %q", got, "lang=rust")
	}
}

// TestCanonValueStripsControlChars guards the terminal-escape entry point:
// an LLM-extracted value carrying ESC/BEL bytes (e.g. an OSC "set title"
// sequence) must never survive canonicalization, wherever it later gets
// printed (SCHEMA.md D5).
func TestCanonValueStripsControlChars(t *testing.T) {
	got := CanonValue("rust\x1b]0;pwned\x07")
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Errorf("control chars survived canonicalization: %q", got)
	}
	if want := "rust]0;pwned"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCanonTokenStripsControlChars(t *testing.T) {
	got := CanonToken("lang", "rust\x1b]0;pwned\x07")
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Errorf("control chars survived canonicalization: %q", got)
	}
	if want := "lang=rust]0;pwned"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestCanonValueMapsPipeToHyphen guards the scope_key entry point: lang/
// framework/topic carry no enum constraint (SCHEMA.md D5), so an extractor
// is free to return a value containing '|', the scope_key token separator
// (Scope.Key). Left untouched, "CI|CD" would canonicalize to "ci|cd" and
// re-split into two tokens on ParseScopeKey, silently orphaning the
// Connection built from it (its own future scope would never SubsetOf-match
// it again).
func TestCanonValueMapsPipeToHyphen(t *testing.T) {
	if got, want := CanonValue("CI|CD"), "ci-cd"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestCanonValueLeavesEqualsUntouched documents the scope boundary: '=' only
// separates key from value inside one token (CanonToken), never tokens
// within a scope_key, so stripping or mapping it here would mangle a
// legitimate value like "a=b" without protecting anything.
func TestCanonValueLeavesEqualsUntouched(t *testing.T) {
	if got, want := CanonValue("a=b"), "a=b"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
