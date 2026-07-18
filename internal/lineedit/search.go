package lineedit

import "io"

// isearch is the reverse incremental history search (ADR-0024 Decision 2). The
// transitions live here, apart from the raw read loop, so the whole state
// machine is tested without a terminal — only reverseSearch below touches I/O.
//
// history runs oldest..newest (as the editor's own does), so "older" walks
// toward index 0. idx == len(history) is "no match yet"; on a failed step idx
// stays on the last good match while failing turns the prompt red-handed.
type isearch struct {
	history []string
	query   []rune
	idx     int
	start   int // rune offset of the query within history[idx]
	failing bool
}

func newISearch(history []string) *isearch {
	return &isearch{history: history, idx: len(history)}
}

// searchFrom finds the newest entry at or below `from` that contains the query
// and settles the match on it, or marks the search failing while leaving the
// previous match in place.
func (s *isearch) searchFrom(from int) {
	if from >= len(s.history) {
		from = len(s.history) - 1
	}
	for i := from; i >= 0; i-- {
		if off, ok := runeIndex([]rune(s.history[i]), s.query); ok {
			s.idx, s.start, s.failing = i, off, false
			return
		}
	}
	s.failing = true
}

// addRune extends the query and re-searches from the current match, so a still
// matching line stays put while a longer query narrows within it first.
func (s *isearch) addRune(r rune) {
	s.query = append(s.query, r)
	s.searchFrom(s.idx)
}

// older steps to the next match further back. An empty query has nothing to
// repeat, matching readline, where Ctrl-R does nothing until you type.
func (s *isearch) older() {
	if len(s.query) == 0 {
		return
	}
	s.searchFrom(s.idx - 1)
}

// backspace shortens the query and searches afresh from the newest entry: a
// shorter query can match lines the longer one had ruled out.
func (s *isearch) backspace() {
	if len(s.query) == 0 {
		return
	}
	s.query = s.query[:len(s.query)-1]
	s.searchFrom(len(s.history) - 1)
}

func (s *isearch) match() (string, int) {
	if s.idx < 0 || s.idx >= len(s.history) {
		return "", 0
	}
	return s.history[s.idx], s.start
}

func (s *isearch) prompt() string {
	tag := "reverse-i-search"
	if s.failing {
		tag = "failed reverse-i-search"
	}
	return "(" + tag + ")`" + string(s.query) + "': "
}

// runeIndex is the rune offset of the first occurrence of query in text. It
// works on runes, not bytes, so the cursor lands on the right column when the
// query or the match is full-width.
func runeIndex(text, query []rune) (int, bool) {
	if len(query) == 0 {
		return 0, true
	}
	for i := 0; i+len(query) <= len(text); i++ {
		if string(text[i:i+len(query)]) == string(query) {
			return i, true
		}
	}
	return 0, false
}

// reverseSearch runs the search over the input line, drawing the
// (reverse-i-search) prompt with the same wrapping-aware draw the editor uses,
// so a full-width query and a match that wraps land correctly. On return b
// holds either the accepted match (submit true) or the pre-search draft, and
// next is the key that ended the search and must be replayed as ordinary
// editing (KeyUnknown = none). Errors are the reader's, surfaced as-is.
func (e *Editor) reverseSearch(b *buffer, p *paint) (submit bool, next Key, err error) {
	draft := append([]rune(nil), b.runes...)
	draftPos := b.pos
	s := newISearch(e.history)

	redraw := func() {
		text, pos := s.match()
		// No hanging indent here: the reverse-search overlay is its own prompt,
		// and a wrapped query reads better flush under it than under the chat's
		// gutter. No background either — the overlay is not the user's line.
		str, np := draw(*p, s.prompt(), []rune(text), pos, e.width(), 0, "")
		io.WriteString(e.out, str)
		*p = np
	}
	redraw()

	for {
		k, derr := decode(e.r)
		if derr != nil {
			return false, Key{}, derr
		}
		switch k.Type {
		case KeyRune:
			s.addRune(k.Rune)
		case KeySearch:
			s.older()
		case KeyBackspace:
			s.backspace()
		case KeyEnter:
			text, _ := s.match()
			b.set(text)
			return true, Key{}, nil
		case KeyInterrupt, KeyUnknown:
			// Esc and Ctrl-G both decode to KeyUnknown, and Ctrl-C aborts the
			// search here rather than the chat (ADR-0024 D2): all restore the
			// draft the search started from.
			b.runes, b.pos = draft, draftPos
			return false, Key{}, nil
		default:
			// A movement or editing key ends the search: take the match into
			// the buffer, then let the caller replay this key as normal editing.
			if text, pos := s.match(); text != "" {
				b.runes, b.pos = []rune(text), pos
			} else {
				b.runes, b.pos = draft, draftPos
			}
			return false, k, nil
		}
		redraw()
	}
}
