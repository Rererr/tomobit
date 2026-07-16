// Package mdlite renders a provider's markdown text into ANSI for a terminal
// (ADR-0024 Decision 5): bold, inline code, headings, bullets, and code
// fences, and nothing more. It is the line-based, self-written conversion the
// ADR chose over a markdown-renderer dependency (glamour et al.) — the chat
// crosses the line into markup only far enough to make the answer readable,
// not to typeset it, so links, tables, and numbered lists are left untouched.
//
// Render carries no environment knowledge: whether a stream is a TTY, whether
// NO_COLOR is set, is the caller's gate (the same one dim() uses), not this
// package's. Render is a pure function of its input.
package mdlite

import "strings"

const (
	ansiReset      = "\x1b[0m"
	ansiBold       = "\x1b[1m"
	ansiDim        = "\x1b[2m"
	ansiInlineCode = "\x1b[36m" // cyan: distinct from bold and from dim's bookkeeping
)

// Render converts one message's markdown to ANSI. Fence state is scoped to the
// single call — a Provider sends one message as one event, so a fence never
// spans calls, and a caller that reused state across messages would leak an
// unclosed fence into unrelated text.
func Render(text string) string {
	lines := strings.Split(text, "\n")
	insideFence := false
	for i, line := range lines {
		if isFence(line) {
			// The fence line itself dims; its toggle decides whether the
			// following lines are body (left verbatim) or prose.
			lines[i] = ansiDim + line + ansiReset
			insideFence = !insideFence
			continue
		}
		if insideFence {
			// Body is verbatim on purpose: running bold/inline-code inside a
			// code block is exactly the misfire fences exist to prevent.
			continue
		}
		lines[i] = renderLine(line)
	}
	return strings.Join(lines, "\n")
}

func isFence(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " "), "```")
}

func renderLine(line string) string {
	if h, ok := heading(line); ok {
		return h
	}
	return inline(bullet(line))
}

// heading bolds a `#{1,6} ` line's text. The # markers are dropped rather than
// kept: they are markup syntax like the ** and ` this package also removes,
// and once the line reads as bold the glyphs add nothing. A run of #s with no
// text is not a heading and is left alone.
func heading(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " ")
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(trimmed) {
		return "", false
	}
	if trimmed[level] != ' ' && trimmed[level] != '\t' {
		return "", false
	}
	title := strings.TrimSpace(trimmed[level:])
	if title == "" {
		return "", false
	}
	return ansiBold + title + ansiReset, true
}

// bullet swaps a leading `- ` or `* ` marker for `• `, keeping any indent so
// nested lists stay nested. The rest of the line still flows through inline.
func bullet(line string) string {
	indent := 0
	for indent < len(line) && (line[indent] == ' ' || line[indent] == '\t') {
		indent++
	}
	rest := line[indent:]
	if strings.HasPrefix(rest, "- ") || strings.HasPrefix(rest, "* ") {
		return line[:indent] + "• " + rest[2:]
	}
	return line
}

// inline paints `code` cyan and **bold** bold, backticks and stars removed.
// Scanning left to right, the first marker opened wins its whole span: a
// backtick before a ** takes the run as code (stars left literal inside), and
// vice versa — the simple rule ADR-0024 settles for over nesting semantics.
// A marker with no partner is left as written.
func inline(line string) string {
	runes := []rune(line)
	var b strings.Builder
	for i := 0; i < len(runes); {
		if runes[i] == '`' {
			if end := indexRune(runes, '`', i+1); end >= 0 {
				b.WriteString(ansiInlineCode)
				b.WriteString(string(runes[i+1 : end]))
				b.WriteString(ansiReset)
				i = end + 1
				continue
			}
		}
		if runes[i] == '*' && i+1 < len(runes) && runes[i+1] == '*' {
			if end := indexDoubleStar(runes, i+2); end >= 0 {
				b.WriteString(ansiBold)
				b.WriteString(string(runes[i+2 : end]))
				b.WriteString(ansiReset)
				i = end + 2
				continue
			}
		}
		b.WriteRune(runes[i])
		i++
	}
	return b.String()
}

func indexRune(runes []rune, r rune, from int) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == r {
			return i
		}
	}
	return -1
}

func indexDoubleStar(runes []rune, from int) int {
	for i := from; i+1 < len(runes); i++ {
		if runes[i] == '*' && runes[i+1] == '*' {
			return i
		}
	}
	return -1
}
