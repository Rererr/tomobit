package lineedit

import "testing"

// matchState renders the search as "<entry>@<offset>" or "-" when nothing is
// matched, so a test states what the user would see rather than an index.
func matchState(s *isearch) string {
	text, pos := s.match()
	if text == "" {
		return "-"
	}
	return text + "@" + string(rune('0'+pos))
}

func typeQuery(s *isearch, q string) {
	for _, r := range q {
		s.addRune(r)
	}
}

func TestSearchFindsTheNewestMatchAsTheQueryGrows(t *testing.T) {
	s := newISearch([]string{"git status", "make test", "git commit"})
	typeQuery(s, "git")
	if got := matchState(s); got != "git commit@0" {
		t.Errorf("newest containing match: got %q", got)
	}
}

func TestSearchStepsToOlderMatchesOnCtrlR(t *testing.T) {
	s := newISearch([]string{"git status", "make test", "git commit"})
	typeQuery(s, "git")
	s.older()
	if got := matchState(s); got != "git status@0" {
		t.Errorf("older git match: got %q", got)
	}
	// No older match remains: the search fails and the last match holds.
	s.older()
	if !s.failing {
		t.Error("a step past the oldest match should fail")
	}
	if got := matchState(s); got != "git status@0" {
		t.Errorf("failed step keeps the last match: got %q", got)
	}
}

func TestSearchOffsetIsWhereTheQueryStarts(t *testing.T) {
	s := newISearch([]string{"please fix the bug"})
	typeQuery(s, "fix")
	if got := matchState(s); got != "please fix the bug@7" {
		t.Errorf("offset of the match: got %q", got)
	}
}

func TestSearchBackspaceRelaxesTheQuery(t *testing.T) {
	s := newISearch([]string{"deploy", "debug"})
	typeQuery(s, "deb")
	if got := matchState(s); got != "debug@0" {
		t.Errorf("got %q", got)
	}
	s.backspace() // "de" — the newest entry now matches again
	if got := matchState(s); got != "debug@0" {
		t.Errorf("after backspace, newest match: got %q", got)
	}
	typeQuery(s, "p")
	if got := matchState(s); got != "deploy@0" {
		t.Errorf("re-narrow to deploy: got %q", got)
	}
}

func TestSearchWithNoMatchFails(t *testing.T) {
	s := newISearch([]string{"one", "two"})
	typeQuery(s, "zzz")
	if !s.failing {
		t.Error("a query nothing contains must mark the search failing")
	}
	if got := matchState(s); got != "-" {
		t.Errorf("nothing matched yet: got %q", got)
	}
}

// The offset must be a rune index, not a byte index, or the cursor lands mid
// character on a full-width match.
func TestSearchOffsetCountsRunesNotBytes(t *testing.T) {
	s := newISearch([]string{"実装して テスト"})
	typeQuery(s, "テスト")
	_, pos := s.match()
	if pos != 5 {
		t.Errorf("rune offset of テスト: got %d, want 5", pos)
	}
}

func TestSearchPromptShowsQueryAndFailure(t *testing.T) {
	s := newISearch([]string{"git status"})
	typeQuery(s, "git")
	if got := s.prompt(); got != "(reverse-i-search)`git': " {
		t.Errorf("prompt: got %q", got)
	}
	typeQuery(s, "zzz")
	if got := s.prompt(); got != "(failed reverse-i-search)`gitzzz': " {
		t.Errorf("failed prompt: got %q", got)
	}
}

func TestSearchOverEmptyHistoryDoesNotPanic(t *testing.T) {
	s := newISearch(nil)
	typeQuery(s, "x")
	s.older()
	if got := matchState(s); got != "-" {
		t.Errorf("empty history yields no match: got %q", got)
	}
}
