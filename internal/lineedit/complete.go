package lineedit

import (
	"fmt"
	"io"
	"strings"
)

// complete acts on Tab: it asks the Completer for the token at the cursor and
// either edits the buffer in place or lists the alternatives below the input
// (ADR-0024 Decision 4). Listing reuses the paint reset KeyClearScreen relies
// on, so the prompt reappears under the candidates with the block intact.
func (e *Editor) complete(b *buffer, p *paint) {
	if e.Completer == nil {
		return
	}
	cands, start := e.Completer(b.String(), b.pos)
	newText, newPos, list := applyCompletion(b.runes, b.pos, cands, start)
	b.runes, b.pos = newText, newPos
	if len(list) == 0 {
		return
	}
	if p.endRow > p.curRow {
		fmt.Fprintf(e.out, "\x1b[%dB", p.endRow-p.curRow)
	}
	io.WriteString(e.out, "\r\n"+strings.Join(list, "  ")+"\r\n")
	*p = paint{}
}

// applyCompletion turns the completer's answer into an edit. One candidate
// commits and adds a trailing space; several extend to their common prefix,
// and only when that cannot grow the token does it hand the list back to be
// shown. cands must already be the ones that match text[start:pos].
func applyCompletion(text []rune, pos int, cands []string, start int) (newText []rune, newPos int, list []string) {
	if len(cands) == 0 || start < 0 || start > pos {
		return text, pos, nil
	}
	if len(cands) == 1 {
		nt, np := spliceToken(text, start, pos, cands[0]+" ")
		return nt, np, nil
	}
	lcp := []rune(longestCommonPrefix(cands))
	if len(lcp) > pos-start {
		nt, np := spliceToken(text, start, pos, string(lcp))
		return nt, np, nil
	}
	return text, pos, cands
}

// spliceToken replaces text[start:pos] with repl and returns the buffer and
// the cursor just past the inserted text.
func spliceToken(text []rune, start, pos int, repl string) ([]rune, int) {
	rr := []rune(repl)
	out := make([]rune, 0, len(text)-(pos-start)+len(rr))
	out = append(out, text[:start]...)
	out = append(out, rr...)
	out = append(out, text[pos:]...)
	return out, start + len(rr)
}

func longestCommonPrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	prefix := []rune(ss[0])
	for _, s := range ss[1:] {
		rs := []rune(s)
		if len(rs) < len(prefix) {
			prefix = prefix[:len(rs)]
		}
		for i := range prefix {
			if prefix[i] != rs[i] {
				prefix = prefix[:i]
				break
			}
		}
	}
	return string(prefix)
}
