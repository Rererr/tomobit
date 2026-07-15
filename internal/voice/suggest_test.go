package voice

import (
	"testing"

	"github.com/Rererr/tomobit/internal/core"
)

func cap(scopeKey, target string, alpha, beta float64) *core.Connection {
	return &core.Connection{Kind: core.ConnCapability, ScopeKey: scopeKey, Target: target, Alpha: alpha, Beta: beta}
}

func TestSuggestStableWinsOnEvidence(t *testing.T) {
	cands := []Candidate{
		{Conn: cap("lang=rust", "claude", 11, 1), State: core.StateStable},
		{Conn: cap("lang=go", "claude", 20, 1), State: core.StateStable},
	}
	text, ok := Suggest(cands, 0)
	if !ok {
		t.Fatal("want a suggestion")
	}
	if want := "goはclaudeとうまくいってるね"; text != want {
		t.Errorf("got %q, want %q (higher evidence should win)", text, want)
	}
}

func TestSuggestQuestionedWinsOnLedgerSum(t *testing.T) {
	cands := []Candidate{
		{Conn: cap("lang=rust", "claude", 5, 5), State: core.StateQuestioned, LedgerSum: 2.1},
		{Conn: cap("lang=go", "claude", 5, 5), State: core.StateQuestioned, LedgerSum: 5.0},
	}
	text, ok := Suggest(cands, 0)
	if !ok {
		t.Fatal("want a suggestion")
	}
	if want := "goとclaude、最近ちょっと様子が違う気がする"; text != want {
		t.Errorf("got %q, want %q (higher ledger sum should win)", text, want)
	}
}

func TestSuggestFallsBackToGuessWhenNothingSettled(t *testing.T) {
	cands := []Candidate{
		{Conn: cap("lang=rust", "claude", 2, 1), State: core.StateBorn},
		{Conn: cap("lang=go", "claude", 4, 3), State: core.StateGrow},
	}
	text, ok := Suggest(cands, 0)
	if !ok {
		t.Fatal("want a suggestion")
	}
	if want := "goはclaudeと相性いいかも"; text != want {
		t.Errorf("got %q, want %q (higher evidence should win)", text, want)
	}
}

func TestSuggestStableOutranksQuestionedAndOther(t *testing.T) {
	cands := []Candidate{
		{Conn: cap("lang=rust", "claude", 11, 1), State: core.StateStable},
		{Conn: cap("lang=go", "claude", 50, 1), State: core.StateQuestioned, LedgerSum: 100},
		{Conn: cap("lang=py", "claude", 50, 1), State: core.StateBorn},
	}
	text, ok := Suggest(cands, 0)
	if !ok {
		t.Fatal("want a suggestion")
	}
	if want := "rustはclaudeとうまくいってるね"; text != want {
		t.Errorf("stable should outrank the others, got %q, want %q", text, want)
	}
}

func TestSuggestIgnoresPreferenceConnections(t *testing.T) {
	cands := []Candidate{
		{
			Conn:  &core.Connection{Kind: core.ConnPreference, ScopeKey: "lang=rust", Target: "claude~codex", Alpha: 11, Beta: 1},
			State: core.StateStable,
		},
	}
	if _, ok := Suggest(cands, 0); ok {
		t.Error("preference connections must not produce a capability suggestion")
	}
}

func TestSuggestEmptyIsSilent(t *testing.T) {
	if _, ok := Suggest(nil, 0); ok {
		t.Error("no connections must not speak")
	}
}
