package lineedit

import "testing"

// state renders a buffer as "text|text" with | at the cursor, so a test says
// what the user sees rather than which index moved.
func state(b *buffer) string {
	return string(b.runes[:b.pos]) + "|" + string(b.runes[b.pos:])
}

func bufAt(text string, pos int) *buffer {
	return &buffer{runes: []rune(text), pos: pos}
}

func TestInsertPutsRunesAtTheCursor(t *testing.T) {
	b := bufAt("helo", 3)
	b.insert('l')
	if got := state(b); got != "hell|o" {
		t.Errorf("got %q", got)
	}
}

func TestBackspaceDeletesBeforeTheCursorAndDeleteAfterIt(t *testing.T) {
	b := bufAt("abc", 2)
	b.backspace()
	if got := state(b); got != "a|c" {
		t.Errorf("backspace: got %q", got)
	}
	b.del()
	if got := state(b); got != "a|" {
		t.Errorf("delete: got %q", got)
	}
}

func TestMovementStopsAtBothEnds(t *testing.T) {
	b := bufAt("ab", 0)
	b.left()
	if b.pos != 0 {
		t.Errorf("left at start moved to %d", b.pos)
	}
	b.end()
	b.right()
	if b.pos != 2 {
		t.Errorf("right at end moved to %d", b.pos)
	}
}

// Home/End are line-wise, not buffer-wise: a pasted block is several lines,
// and Ctrl-A on the second one must not jump to the top of the paste.
func TestHomeAndEndWorkOnTheCurrentLine(t *testing.T) {
	b := bufAt("one\ntwo", 5)
	b.home()
	if got := state(b); got != "one\n|two" {
		t.Errorf("home: got %q", got)
	}
	b.end()
	if got := state(b); got != "one\ntwo|" {
		t.Errorf("end: got %q", got)
	}
}

func TestUpAndDownMoveBetweenLinesKeepingTheColumn(t *testing.T) {
	b := bufAt("abcd\nefgh", 7) // second line, column 2
	if !b.up() {
		t.Fatal("up should move when a line is above")
	}
	if got := state(b); got != "ab|cd\nefgh" {
		t.Errorf("up: got %q", got)
	}
	if !b.down() {
		t.Fatal("down should move when a line is below")
	}
	if got := state(b); got != "abcd\nef|gh" {
		t.Errorf("down: got %q", got)
	}
}

func TestUpAndDownClampTheColumnToAShorterLine(t *testing.T) {
	b := bufAt("ab\nefgh", 6) // column 3, but the line above has 2
	if !b.up() {
		t.Fatal("up should move")
	}
	if got := state(b); got != "ab|\nefgh" {
		t.Errorf("got %q", got)
	}
}

// A single-line buffer has no line above or below: up/down must report that,
// because the editor reads it as "this key means history instead".
func TestUpAndDownReportNoMoveOnASingleLine(t *testing.T) {
	b := bufAt("only", 2)
	if b.up() || b.down() {
		t.Error("a single line must not consume up/down")
	}
}

func TestWordMovementSkipsSpacesThenTheWord(t *testing.T) {
	b := bufAt("fix the bug", 11)
	b.wordLeft()
	if got := state(b); got != "fix the |bug" {
		t.Errorf("wordLeft: got %q", got)
	}
	b.wordLeft()
	if got := state(b); got != "fix |the bug" {
		t.Errorf("wordLeft twice: got %q", got)
	}
	b.wordRight()
	if got := state(b); got != "fix the| bug" {
		t.Errorf("wordRight: got %q", got)
	}
}

func TestKillWordRemovesTheWordBeforeTheCursor(t *testing.T) {
	b := bufAt("fix the bug", 11)
	b.killWord()
	if got := state(b); got != "fix the |" {
		t.Errorf("got %q", got)
	}
}

func TestKillToEndStopsAtTheLineBreak(t *testing.T) {
	b := bufAt("one\ntwo", 1)
	b.killToEnd()
	if got := state(b); got != "o|\ntwo" {
		t.Errorf("got %q", got)
	}
}

// Ctrl-U throws the whole input away, every line of it: after a pasted block
// there must be one key that empties the prompt (zsh's kill-whole-line).
func TestClearEmptiesEveryLineNotJustTheCurrentOne(t *testing.T) {
	b := bufAt("one\ntwo\nthree", 9)
	b.clear()
	if got := state(b); got != "|" {
		t.Errorf("got %q", got)
	}
	if !b.empty() {
		t.Error("buffer should report empty")
	}
}
