// Package subtask implements the split-proposal protocol (ADR-0023): a
// Provider that judges a --split task too large returns a small JSON marker
// instead of doing the work, and this package turns that marker into the
// subtask strings tomobit runs next. Pure — no I/O, no store — so the
// protocol's parsing and framing can be pinned by recorded-text fixtures the
// same way an Adapter's Translate is (ADR-0006 Decision 3).
package subtask

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Min and Max bound a legal proposal's subtask count (ADR-0023 Decision 1).
const (
	Min = 2
	Max = 5
)

// key is the marker the protocol looks for, quoted exactly as it appears in
// JSON — searching for the quoted form skips any plain-prose mention of the
// word "tomobit_split" that isn't actually a JSON key.
const key = `"tomobit_split"`

// exampleSubs are the elements of the JSON example the instruction embeds.
// The example must exist (a shape shown once beats a shape described), but it
// necessarily satisfies the protocol's own schema — so a model that merely
// quotes its instructions back would otherwise hand Parse a "legal" proposal,
// and tomobit would run these placeholders as production tasks. Parse treats
// a candidate equal to this as the echo it is, never as a proposal.
var exampleSubs = []string{"サブタスク1の指示", "サブタスク2の指示"}

// Instruction appends the split-proposal protocol to prompt. The text is
// deterministic harness text (ADR-0023 Decision 1): a model-authored version
// of this offer would make the offer itself unauditable — the harness always
// asks the same way, so a proposal's absence is a model decision, not a
// wording accident.
func Instruction(prompt string) string {
	return fmt.Sprintf("%s"+
		"\n\n---\n"+
		"[tomobit] もしこのタスクが1回の実行では難しすぎる、または独立したサブタスクに分割した方が確実だと判断した場合は、作業を行わず、次の形式のJSONコードブロックだけを出力して分割を提案せよ:\n\n"+
		"```json\n"+
		"{\"tomobit_split\": [\"%s\", \"%s\"]}\n"+
		"```\n\n"+
		"- サブタスクは%d〜%d個。各サブタスクは単独で実行可能な自己完結した指示にする（順に実行される）\n"+
		"- 分割が不要ならこの指示は無視し、タスクを完遂せよ",
		prompt, exampleSubs[0], exampleSubs[1], Min, Max)
}

// Prompt frames one subtask deterministically for its own run (the
// stepPrompt harness-text pattern, cmd/tomobit/main.go): the parent intent
// rides along so the subtask's session records what it was cut from, but no
// split protocol text is added — a subtask cannot itself propose a further
// split (ADR-0023 Decision 4: depth stays 1).
func Prompt(parentIntent, sub string, i, total int) string {
	return fmt.Sprintf("%s\n\n[tomobit subtask %d/%d] 元タスク「%s」を分割したサブタスクの一つ。このサブタスクだけを完遂せよ。",
		sub, i+1, total, parentIntent)
}

// Parse reads text (a run's concatenated provider.output) for a split
// proposal. Three outcomes distinguish "nothing was offered" from "something
// was offered but is broken" (ADR-0023 Decision 1: 警告して通常フロー続行,
// never a silent fallback either way):
//
//   - (nil, nil): no marker anywhere — an ordinary run, use its output as-is
//   - (nil, err): the marker is present but no legal proposal could be read
//     from it (too few/many subtasks, a non-string or blank element, or the
//     surrounding text isn't valid JSON)
//   - (subs, nil): accepted; each element is trimmed
//
// It reads both a fenced ```json block and bare JSON with no fence at all —
// providers are not guaranteed to wrap their marker in a code fence just
// because the instruction showed one. A candidate equal to the instruction's
// own example is the harness's text quoted back (models routinely cite their
// prompt while declining to split) and reads as no proposal, not a proposal.
func Parse(text string) ([]string, error) {
	if !strings.Contains(text, key) {
		return nil, nil
	}

	var lastErr error
	var sawBroken, sawEcho bool
	tryCandidate := func(c string) []string {
		subs, err := parseObject(c)
		if err != nil {
			sawBroken = true
			lastErr = err
			return nil
		}
		if isExampleEcho(subs) {
			sawEcho = true
			return nil
		}
		return subs
	}

	for _, block := range fencedBlocks(text) {
		if !strings.Contains(block, key) {
			continue
		}
		if subs := tryCandidate(block); subs != nil {
			return subs, nil
		}
	}
	for _, obj := range braceObjects(text) {
		if subs := tryCandidate(obj); subs != nil {
			return subs, nil
		}
	}

	if sawBroken {
		return nil, lastErr
	}
	if sawEcho {
		return nil, nil
	}
	return nil, fmt.Errorf("subtask: found %s but no JSON object could be extracted around it", key)
}

// isExampleEcho reports whether a parsed candidate is exactly the
// instruction's embedded example. Compared post-parse (element-wise, after
// trimming) so an echo survives whatever reformatting the quoting model
// applied to the JSON itself.
func isExampleEcho(subs []string) bool {
	if len(subs) != len(exampleSubs) {
		return false
	}
	for i := range subs {
		if subs[i] != exampleSubs[i] {
			return false
		}
	}
	return true
}

// parseObject validates one candidate JSON object against the protocol's
// shape. A map (not a struct) unmarshal target so a missing key is
// distinguishable from a present-but-empty one.
func parseObject(candidate string) ([]string, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(candidate), &m); err != nil {
		return nil, fmt.Errorf("subtask: not valid JSON: %w", err)
	}
	raw, ok := m["tomobit_split"]
	if !ok {
		return nil, fmt.Errorf("subtask: %s key missing from candidate object", key)
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("subtask: %s must be an array, got %T", key, raw)
	}
	if len(arr) < Min || len(arr) > Max {
		return nil, fmt.Errorf("subtask: %d subtasks proposed, want %d-%d", len(arr), Min, Max)
	}
	subs := make([]string, len(arr))
	for i, v := range arr {
		str, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("subtask: element %d is not a string, got %T", i, v)
		}
		trimmed := strings.TrimSpace(str)
		if trimmed == "" {
			return nil, fmt.Errorf("subtask: element %d is blank", i)
		}
		subs[i] = trimmed
	}
	return subs, nil
}

// fencedBlocks returns the body of every ``` ... ``` block in text, in
// order, with a language tag on the opening fence's own line (e.g. "json")
// stripped. Malformed nesting (an unclosed fence) simply stops the scan —
// whatever came before is still a legal candidate.
func fencedBlocks(text string) []string {
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

// maxAnchors bounds how many `{` positions before a key occurrence
// braceObjects will try. The nearest one is usually the object's start, but
// not always — it may sit inside an earlier member's string value
// (`{"note": "a { in prose", "tomobit_split": ...}`) — so a failed anchor
// gets a farther one tried instead of rejecting a legal proposal. The cap
// keeps a brace-heavy output from turning the scan quadratic.
const maxAnchors = 8

// braceObjects extracts, for every occurrence of key in text, candidate JSON
// objects framing it: each `{` before the occurrence, nearest first, matched
// forward against its closing `}` (ADR-0023: a provider that skips the code
// fence entirely still gets read — the harness only suggests the fence, it
// cannot enforce it). A span that balances before reaching the key cannot be
// framing it and is skipped; whether a span actually parses is the caller's
// judgment, so garbage from a mid-string anchor never masks the real object
// at a farther one.
func braceObjects(text string) []string {
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
