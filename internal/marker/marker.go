// Package marker extracts candidate JSON objects around a protocol key from a
// Provider's free-form output.
//
// tomobit now speaks more than one protocol back to a Provider — the split
// proposal (ADR-0023 / ADR-0028) and the workspace isolation declaration
// (ADR-0050) — and every one of them faces the identical problem: the model
// may fence its JSON, may not, and may bury it in prose that also contains
// braces. Copying that scan into each protocol package would put the same
// truth in two places, which is the drift ADR-0039 and ADR-0048 both refused
// (「正本が割れる」). The protocols keep what is theirs — the instruction text,
// the schema, the self-echo rejection — and share only the scan.
//
// Pure: no I/O, no store. Recorded provider text pins it, the same way an
// Adapter's Translate is pinned (ADR-0006 Decision 3).
package marker

import "strings"

// Fenced returns the body of every ``` ... ``` block in text, in order, with a
// language tag on the opening fence's own line (e.g. "json") stripped.
// Malformed nesting (an unclosed fence) simply stops the scan — whatever came
// before is still a legal candidate.
func Fenced(text string) []string {
	var blocks []string
	rest := text
	for {
		start := strings.Index(rest, "```")
		if start == -1 {
			return blocks
		}
		afterOpen := rest[start+3:]
		end := strings.Index(afterOpen, "```")
		if end == -1 {
			return blocks
		}
		body := afterOpen[:end]
		if nl := strings.IndexByte(body, '\n'); nl != -1 {
			if tag := strings.TrimSpace(body[:nl]); tag != "" && !strings.HasPrefix(tag, "{") {
				body = body[nl+1:]
			}
		}
		blocks = append(blocks, body)
		rest = afterOpen[end+3:]
	}
}

// maxAnchors bounds how many `{` positions before a key occurrence Objects
// will try. The nearest one is usually the object's start, but not always — it
// may sit inside an earlier member's string value
// (`{"note": "a { in prose", "tomobit_split": ...}`) — so a failed anchor gets
// a farther one tried instead of rejecting a legal declaration. The cap keeps
// a brace-heavy output from turning the scan quadratic.
const maxAnchors = 8

// Objects extracts, for every occurrence of key in text, candidate JSON
// objects framing it: each `{` before the occurrence, nearest first, matched
// forward against its closing `}`. A provider that skips the code fence
// entirely still gets read — the harness only suggests the fence, it cannot
// enforce it.
//
// A span that balances before reaching the key cannot be framing it and is
// skipped; whether a span actually parses is the caller's judgment, so garbage
// from a mid-string anchor never masks the real object at a farther one.
//
// key must be the quoted JSON form (`"tomobit_split"`), not the bare word:
// searching for the quoted form skips prose that merely mentions the name.
func Objects(text, key string) []string {
	var out []string
	from := 0
	for {
		idx := strings.Index(text[from:], key)
		if idx == -1 {
			return out
		}
		idx += from
		from = idx + len(key)

		pos := idx
		for tries := 0; tries < maxAnchors; tries++ {
			start := strings.LastIndexByte(text[:pos], '{')
			if start == -1 {
				break
			}
			pos = start
			if end, ok := matchingBrace(text, start); ok && end > idx {
				out = append(out, text[start:end+1])
			}
		}
	}
}

// matchingBrace finds the index of the `}` that closes the `{` at start,
// treating quoted string content (escapes included) as opaque so a brace
// character inside a JSON string value never desyncs the depth count.
func matchingBrace(text string, start int) (int, bool) {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		switch {
		case escaped:
			escaped = false
		case inString:
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
		default:
			switch c {
			case '"':
				inString = true
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return i, true
				}
			}
		}
	}
	return 0, false
}
