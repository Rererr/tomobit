package subtask

import (
	"strings"
	"testing"
)

// flatten collapses a group structure into its subtasks in execution order,
// so a test asserting on the flat result stays readable when the input is a
// mix of lone subtasks and independent groups.
func flatten(groups [][]string) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func TestParseNoMarkerIsNotASplit(t *testing.T) {
	groups, err := Parse("done. changed three files, all tests pass.")
	if groups != nil || err != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", groups, err)
	}
}

// TestParseFlatProposalBecomesOneGroupPerSubtask: a proposal with no nested
// arrays is every subtask in its own single-element group ([["a"],["b"]]) —
// the all-sequential default, no independence declared (ADR-0028 Decision 2).
func TestParseFlatProposalBecomesOneGroupPerSubtask(t *testing.T) {
	text := "この作業はいったん分割したい。\n\n" +
		"```json\n" +
		`{"tomobit_split": ["A: setup the schema", "B: implement the endpoint"]}` +
		"\n```\n\n以上、提案でした。"
	groups, err := Parse(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := [][]string{{"A: setup the schema"}, {"B: implement the endpoint"}}
	if !equalGroups(groups, want) {
		t.Fatalf("got %v, want %v", groups, want)
	}
}

// TestParseAcceptsMixedLoneAndGroupElements pins the group schema: a string is
// a lone subtask, a nested array is an independent group, and the two mix in
// one proposal (ADR-0028 Decision 2). The flattened order is the proposal
// order.
func TestParseAcceptsMixedLoneAndGroupElements(t *testing.T) {
	text := `{"tomobit_split": ["a", ["b", "c"], "d"]}`
	groups, err := Parse(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := [][]string{{"a"}, {"b", "c"}, {"d"}}
	if !equalGroups(groups, want) {
		t.Fatalf("got %v, want %v", groups, want)
	}
	if got := flatten(groups); strings.Join(got, ",") != "a,b,c,d" {
		t.Fatalf("flattened execution order = %v, want a,b,c,d", got)
	}
}

func TestParseAcceptsBareJSONWithNoFence(t *testing.T) {
	text := `sure, here is the split: {"tomobit_split": ["step one", "step two", "step three"]} let me know.`
	groups, err := Parse(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(flatten(groups)) != 3 {
		t.Fatalf("got %v, want 3 subtasks", groups)
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

// TestParseBoundsTheFlattenedTotalNotTheGroupCount: two groups is below the
// element-count floor but six flattened subtasks is above Max — the bound
// reads the flattened total (ADR-0028 Decision 2).
func TestParseBoundsTheFlattenedTotalNotTheGroupCount(t *testing.T) {
	_, err := Parse(`{"tomobit_split": [["a","b","c"], ["d","e","f"]]}`)
	if err == nil {
		t.Fatal("six subtasks across two groups is above Max and should error")
	}
}

func TestParseRejectsEmptyGroup(t *testing.T) {
	_, err := Parse(`{"tomobit_split": ["real task", []]}`)
	if err == nil {
		t.Fatal("an empty group declares nothing to run and should error")
	}
}

func TestParseRejectsBlankElement(t *testing.T) {
	_, err := Parse(`{"tomobit_split": ["real task", "   "]}`)
	if err == nil {
		t.Fatal("a whitespace-only element should error")
	}
}

func TestParseRejectsBlankElementInsideAGroup(t *testing.T) {
	_, err := Parse(`{"tomobit_split": ["real task", ["ok", "   "]]}`)
	if err == nil {
		t.Fatal("a whitespace-only element inside a group should error")
	}
}

func TestParseRejectsNonStringElement(t *testing.T) {
	_, err := Parse(`{"tomobit_split": ["real task", 42]}`)
	if err == nil {
		t.Fatal("a non-string element should error")
	}
}

func TestParseRejectsNonStringInsideAGroup(t *testing.T) {
	_, err := Parse(`{"tomobit_split": ["real task", ["ok", 42]]}`)
	if err == nil {
		t.Fatal("a non-string element inside a group should error")
	}
}

func TestParseRejectsBrokenJSONNearTheMarker(t *testing.T) {
	_, err := Parse(`the proposal is {"tomobit_split": ["a", "b", ] broken and unparsable`)
	if err == nil {
		t.Fatal("marker present but malformed JSON should error, not silently pass")
	}
}

func TestParseTrimsWhitespaceFromAcceptedElements(t *testing.T) {
	groups, err := Parse(`{"tomobit_split": ["  leading and trailing  ", ["  inside a group  ", "second"]]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if groups[0][0] != "leading and trailing" {
		t.Errorf("lone element should be trimmed: got %q", groups[0][0])
	}
	if groups[1][0] != "inside a group" {
		t.Errorf("group element should be trimmed: got %q", groups[1][0])
	}
}

func TestParseSurvivesClosingBraceInsideStringValue(t *testing.T) {
	text := `{"tomobit_split": ["fix the a}b edge case", "write a regression test"]}`
	groups, err := Parse(text)
	if err != nil {
		t.Fatalf("a literal '}' inside a string value broke brace extraction: %v", err)
	}
	want := "fix the a}b edge case"
	if flat := flatten(groups); len(flat) != 2 || flat[0] != want {
		t.Fatalf("got %v, want first element %q", groups, want)
	}
}

func equalGroups(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

// TestParseTreatsTheInstructionsOwnExampleAsNoProposal guards the
// self-reference hole: the example JSON the instruction embeds satisfies the
// protocol's schema, so a model that merely quotes its instructions back
// (e.g. while explaining why it did NOT split) must read as "no proposal" —
// never as placeholder subtasks to run as production tasks.
func TestParseTreatsTheInstructionsOwnExampleAsNoProposal(t *testing.T) {
	groups, err := Parse(Instruction("implement the big feature"))
	if groups != nil || err != nil {
		t.Fatalf("the instruction text itself must parse as (nil, nil), got (%v, %v)", groups, err)
	}
}

// TestParseEchoedExampleDoesNotMaskARealProposal: a model may quote the
// instruction's group-form example AND then make its actual proposal — the
// echo must be skipped, not returned in the real proposal's place.
func TestParseEchoedExampleDoesNotMaskARealProposal(t *testing.T) {
	text := `指示には {"tomobit_split": ["サブタスク1の指示", ["サブタスク2aの指示", "サブタスク2bの指示"]]} の形式とあるので、こう分割する:` +
		"\n```json\n" +
		`{"tomobit_split": ["design the schema", "implement the parser"]}` +
		"\n```"
	groups, err := Parse(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flat := flatten(groups); len(flat) != 2 || flat[0] != "design the schema" {
		t.Fatalf("the real proposal should win over the echoed example, got %v", groups)
	}
}

// TestParseFindsTheObjectPastAnOpenBraceInAnEarlierStringValue: the nearest
// '{' before the marker key can sit inside another member's string value —
// anchoring there alone would reject a legal proposal.
func TestParseFindsTheObjectPastAnOpenBraceInAnEarlierStringValue(t *testing.T) {
	text := `{"note": "curly brace { appears here in a string", "tomobit_split": ["task one here", "task two here"]}`
	groups, err := Parse(text)
	if err != nil {
		t.Fatalf("an earlier in-string '{' broke object extraction: %v", err)
	}
	if flat := flatten(groups); len(flat) != 2 || flat[0] != "task one here" {
		t.Fatalf("got %v, want the two proposed tasks", groups)
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
