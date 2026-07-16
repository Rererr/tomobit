package subtask

import (
	"strings"
	"testing"
)

func TestParseNoMarkerIsNotASplit(t *testing.T) {
	subs, err := Parse("done. changed three files, all tests pass.")
	if subs != nil || err != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", subs, err)
	}
}

func TestParseAcceptsFencedJSONSurroundedByProse(t *testing.T) {
	text := "この作業はいったん分割したい。\n\n" +
		"```json\n" +
		`{"tomobit_split": ["A: setup the schema", "B: implement the endpoint"]}` +
		"\n```\n\n以上、提案でした。"
	subs, err := Parse(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"A: setup the schema", "B: implement the endpoint"}
	if len(subs) != 2 || subs[0] != want[0] || subs[1] != want[1] {
		t.Fatalf("got %v, want %v", subs, want)
	}
}

func TestParseAcceptsBareJSONWithNoFence(t *testing.T) {
	text := `sure, here is the split: {"tomobit_split": ["step one", "step two", "step three"]} let me know.`
	subs, err := Parse(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(subs) != 3 {
		t.Fatalf("got %v, want 3 subtasks", subs)
	}
}

func TestParseRejectsSingleSubtask(t *testing.T) {
	_, err := Parse(`{"tomobit_split": ["only one"]}`)
	if err == nil {
		t.Fatal("a single subtask is below Min and should error")
	}
}

func TestParseRejectsSixSubtasks(t *testing.T) {
	_, err := Parse(`{"tomobit_split": ["a","b","c","d","e","f"]}`)
	if err == nil {
		t.Fatal("six subtasks is above Max and should error")
	}
}

func TestParseRejectsBlankElement(t *testing.T) {
	_, err := Parse(`{"tomobit_split": ["real task", "   "]}`)
	if err == nil {
		t.Fatal("a whitespace-only element should error")
	}
}

func TestParseRejectsNonStringElement(t *testing.T) {
	_, err := Parse(`{"tomobit_split": ["real task", 42]}`)
	if err == nil {
		t.Fatal("a non-string element should error")
	}
}

func TestParseRejectsBrokenJSONNearTheMarker(t *testing.T) {
	_, err := Parse(`the proposal is {"tomobit_split": ["a", "b", ] broken and unparsable`)
	if err == nil {
		t.Fatal("marker present but malformed JSON should error, not silently pass")
	}
}

func TestParseTrimsWhitespaceFromAcceptedElements(t *testing.T) {
	subs, err := Parse(`{"tomobit_split": ["  leading and trailing  ", "second"]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subs[0] != "leading and trailing" {
		t.Errorf("element should be trimmed: got %q", subs[0])
	}
}

func TestParseSurvivesClosingBraceInsideStringValue(t *testing.T) {
	text := `{"tomobit_split": ["fix the a}b edge case", "write a regression test"]}`
	subs, err := Parse(text)
	if err != nil {
		t.Fatalf("a literal '}' inside a string value broke brace extraction: %v", err)
	}
	want := "fix the a}b edge case"
	if len(subs) != 2 || subs[0] != want {
		t.Fatalf("got %v, want first element %q", subs, want)
	}
}

// TestParseTreatsTheInstructionsOwnExampleAsNoProposal guards the
// self-reference hole: the example JSON the instruction embeds satisfies the
// protocol's schema, so a model that merely quotes its instructions back
// (e.g. while explaining why it did NOT split) must read as "no proposal" —
// never as placeholder subtasks to run as production tasks.
func TestParseTreatsTheInstructionsOwnExampleAsNoProposal(t *testing.T) {
	subs, err := Parse(Instruction("implement the big feature"))
	if subs != nil || err != nil {
		t.Fatalf("the instruction text itself must parse as (nil, nil), got (%v, %v)", subs, err)
	}
}

// TestParseEchoedExampleDoesNotMaskARealProposal: a model may quote the
// instruction's example AND then make its actual proposal — the echo must be
// skipped, not returned in the real proposal's place.
func TestParseEchoedExampleDoesNotMaskARealProposal(t *testing.T) {
	text := `指示には {"tomobit_split": ["サブタスク1の指示", "サブタスク2の指示"]} の形式とあるので、こう分割する:` +
		"\n```json\n" +
		`{"tomobit_split": ["design the schema", "implement the parser"]}` +
		"\n```"
	subs, err := Parse(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(subs) != 2 || subs[0] != "design the schema" {
		t.Fatalf("the real proposal should win over the echoed example, got %v", subs)
	}
}

// TestParseFindsTheObjectPastAnOpenBraceInAnEarlierStringValue: the nearest
// '{' before the marker key can sit inside another member's string value —
// anchoring there alone would reject a legal proposal.
func TestParseFindsTheObjectPastAnOpenBraceInAnEarlierStringValue(t *testing.T) {
	text := `{"note": "curly brace { appears here in a string", "tomobit_split": ["task one here", "task two here"]}`
	subs, err := Parse(text)
	if err != nil {
		t.Fatalf("an earlier in-string '{' broke object extraction: %v", err)
	}
	if len(subs) != 2 || subs[0] != "task one here" {
		t.Fatalf("got %v, want the two proposed tasks", subs)
	}
}

func TestInstructionCarriesThePromptAndTheProtocolMarker(t *testing.T) {
	got := Instruction("do the thing")
	if !containsAll(got, "do the thing", `"tomobit_split"`, "作業を行わず") {
		t.Errorf("Instruction missing required content: %q", got)
	}
}

func TestPromptFramesTheSubtaskWithItsPositionAndParentIntent(t *testing.T) {
	got := Prompt("build the whole feature", "write the schema", 1, 3)
	if !containsAll(got, "write the schema", "2/3", "build the whole feature") {
		t.Errorf("Prompt missing required content: %q", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
