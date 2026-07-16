package lineedit

import "testing"

func applyState(text string, pos int, cands []string, start int) (string, int, []string) {
	nt, np, list := applyCompletion([]rune(text), pos, cands, start)
	return string(nt), np, list
}

func TestCompletionCommitsAUniqueCandidateWithASpace(t *testing.T) {
	got, pos, list := applyState("/pro", 4, []string{"/provider"}, 0)
	if got != "/provider " {
		t.Errorf("text: got %q", got)
	}
	if pos != len("/provider ") {
		t.Errorf("cursor should follow the inserted space: got %d", pos)
	}
	if list != nil {
		t.Errorf("a unique commit lists nothing: got %v", list)
	}
}

func TestCompletionExtendsToTheCommonPrefixWhenAmbiguous(t *testing.T) {
	// "/" can grow to the common prefix "/c" of both candidates.
	got, pos, list := applyState("/", 1, []string{"/cap", "/clear"}, 0)
	if got != "/c" {
		t.Errorf("should extend to the common prefix: got %q", got)
	}
	if pos != 2 {
		t.Errorf("cursor at the end of the extension: got %d", pos)
	}
	if list != nil {
		t.Errorf("an extension does not list yet: got %v", list)
	}
}

func TestCompletionListsWhenTheCommonPrefixCannotGrow(t *testing.T) {
	got, pos, list := applyState("/c", 2, []string{"/cap", "/clear"}, 0)
	if got != "/c" || pos != 2 {
		t.Errorf("an un-extendable token is left untouched: got %q @%d", got, pos)
	}
	if len(list) != 2 {
		t.Errorf("both candidates should be handed back to display: got %v", list)
	}
}

func TestCompletionReplacesOnlyTheTokenNotTheTail(t *testing.T) {
	// "/provider cla" with the cursor after "cla" and a following word.
	got, pos, _ := applyState("/provider cla extra", 13, []string{"claude-code"}, 10)
	if got != "/provider claude-code  extra" {
		t.Errorf("only the token is replaced: got %q", got)
	}
	if pos != len("/provider claude-code ") {
		t.Errorf("cursor after the inserted space: got %d", pos)
	}
}

func TestCompletionDeclinesWhenTheCompleterOptsOut(t *testing.T) {
	got, pos, list := applyState("hello", 5, nil, -1)
	if got != "hello" || pos != 5 || list != nil {
		t.Errorf("no candidates means Tab does nothing: got %q @%d %v", got, pos, list)
	}
}

func TestLongestCommonPrefix(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"claude-code", "codex"}, "c"},
		{[]string{"/size", "/status"}, "/s"},
		{[]string{"human"}, "human"},
		{[]string{"a", "b"}, ""},
		{nil, ""},
	} {
		if got := longestCommonPrefix(tc.in); got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.in, got, tc.want)
		}
	}
}
