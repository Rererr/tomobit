package lineedit

import "unicode"

// buffer is the text being edited and the cursor's rune index into it.
// A buffer may hold newlines (a pasted block, or `\`+Enter), so every
// movement that a human thinks of as "this line" works on the run between
// newlines, not on the whole buffer.
type buffer struct {
	runes []rune
	pos   int
}

func (b *buffer) String() string { return string(b.runes) }
func (b *buffer) empty() bool    { return len(b.runes) == 0 }

func (b *buffer) set(s string) {
	b.runes = []rune(s)
	b.pos = len(b.runes)
}

func (b *buffer) insert(rs ...rune) {
	tail := append([]rune(nil), b.runes[b.pos:]...)
	b.runes = append(b.runes[:b.pos], rs...)
	b.runes = append(b.runes, tail...)
	b.pos += len(rs)
}

func (b *buffer) backspace() {
	if b.pos == 0 {
		return
	}
	b.runes = append(b.runes[:b.pos-1], b.runes[b.pos:]...)
	b.pos--
}

func (b *buffer) del() {
	if b.pos >= len(b.runes) {
		return
	}
	b.runes = append(b.runes[:b.pos], b.runes[b.pos+1:]...)
}

func (b *buffer) left() {
	if b.pos > 0 {
		b.pos--
	}
}

func (b *buffer) right() {
	if b.pos < len(b.runes) {
		b.pos++
	}
}

// lineStart is the index just after the newline that precedes pos.
func (b *buffer) lineStart(pos int) int {
	for i := pos - 1; i >= 0; i-- {
		if b.runes[i] == '\n' {
			return i + 1
		}
	}
	return 0
}

// lineEnd is the index of the newline at or after pos (or the buffer end).
func (b *buffer) lineEnd(pos int) int {
	for i := pos; i < len(b.runes); i++ {
		if b.runes[i] == '\n' {
			return i
		}
	}
	return len(b.runes)
}

func (b *buffer) home() { b.pos = b.lineStart(b.pos) }
func (b *buffer) end()  { b.pos = b.lineEnd(b.pos) }

// up moves to the line above, keeping the column. It reports false when
// there is no line above — the caller then reads that key as "history",
// which is what a single-line buffer's Up must mean.
func (b *buffer) up() bool {
	start := b.lineStart(b.pos)
	if start == 0 {
		return false
	}
	col := b.pos - start
	prevStart := b.lineStart(start - 1)
	prevLen := start - 1 - prevStart
	if col > prevLen {
		col = prevLen
	}
	b.pos = prevStart + col
	return true
}

func (b *buffer) down() bool {
	end := b.lineEnd(b.pos)
	if end == len(b.runes) {
		return false
	}
	col := b.pos - b.lineStart(b.pos)
	nextStart := end + 1
	nextLen := b.lineEnd(nextStart) - nextStart
	if col > nextLen {
		col = nextLen
	}
	b.pos = nextStart + col
	return true
}

// wordLeftFrom is the index one word back from pos: over any spaces, then
// over the word itself.
func (b *buffer) wordLeftFrom(pos int) int {
	i := pos
	for i > 0 && isSpace(b.runes[i-1]) {
		i--
	}
	for i > 0 && !isSpace(b.runes[i-1]) {
		i--
	}
	return i
}

func (b *buffer) wordLeft() { b.pos = b.wordLeftFrom(b.pos) }

func (b *buffer) wordRight() {
	i := b.pos
	for i < len(b.runes) && isSpace(b.runes[i]) {
		i++
	}
	for i < len(b.runes) && !isSpace(b.runes[i]) {
		i++
	}
	b.pos = i
}

// killToEnd deletes to the end of the current line, matching Ctrl-K in a
// shell: on a multi-line buffer it clears the line, not everything below. It
// returns the removed text so the caller can hold it for a later yank; an
// empty return means nothing was removed and the kill slot must stay as it was.
func (b *buffer) killToEnd() string {
	end := b.lineEnd(b.pos)
	if end == b.pos {
		return ""
	}
	killed := string(b.runes[b.pos:end])
	b.runes = append(b.runes[:b.pos], b.runes[end:]...)
	return killed
}

// clear throws the whole input away, every line of it — what Ctrl-U means in
// zsh (kill-whole-line) and in the chat this editor imitates. Readline's
// "kill to the start of the line" is the other reading, but after a pasted
// multi-line block it leaves the earlier lines with no key that can finish
// them off. Ctrl-W is still there for taking back one word at a time. The
// removed text is returned for the yank slot, as killToEnd does.
func (b *buffer) clear() string {
	if len(b.runes) == 0 {
		return ""
	}
	killed := string(b.runes)
	b.runes, b.pos = nil, 0
	return killed
}

func (b *buffer) killWord() string {
	start := b.wordLeftFrom(b.pos)
	if start == b.pos {
		return ""
	}
	killed := string(b.runes[start:b.pos])
	b.runes = append(b.runes[:start], b.runes[b.pos:]...)
	b.pos = start
	return killed
}

// stackKill folds a freshly killed word into the kill slot. A backward kill
// takes the word before the cursor, so a run of Ctrl-W (accum) prepends each
// earlier word to keep the recovered text in reading order; a break in the run
// starts the slot over.
func stackKill(slot, killed string, accum bool) string {
	if accum {
		return killed + slot
	}
	return killed
}

func isSpace(r rune) bool { return unicode.IsSpace(r) }
