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
