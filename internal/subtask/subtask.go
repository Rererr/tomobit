// Package subtask implements the split-proposal protocol (ADR-0023, always-on
// since ADR-0028): every `do` target run carries the protocol, and a Provider
// that judges the task too large returns a small JSON marker instead of doing
// the work. This package turns that marker into the subtask groups tomobit
// runs next — a group being subtasks the Provider declared independent
// (ADR-0028 Decision 2). Pure — no I/O, no store — so the protocol's parsing
// and framing can be pinned by recorded-text fixtures the same way an
// Adapter's Translate is (ADR-0006 Decision 3).
package subtask

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Rererr/tomobit/internal/marker"
)

// Min and Max bound a legal proposal's subtask count (ADR-0023 Decision 1).
// The bound is on the flattened total — sum across all groups — not the group
// count, since a group is only a parallelism declaration (ADR-0028 Decision 2).
const (
	Min = 2
	Max = 5
)

// key is the marker the protocol looks for, quoted exactly as it appears in
// JSON — searching for the quoted form skips any plain-prose mention of the
// word "tomobit_split" that isn't actually a JSON key.
const key = `"tomobit_split"`

// Element is one proposed subtask: the instruction, plus the executor the
// proposing Provider named for it (ADR-0060 Decision 2). An empty Provider is
// the unchanged default — the child runs on the parent's wiring (ADR-0054
// Decision 1) — so a plain string element and a named one differ in exactly
// one field.
//
// This package does not judge the name. Which names exist is the caller's
// registry, not the protocol's, and "is this subtask really codex work?" is a
// question of meaning that belongs to the Provider (ADR-0028 Decision 2's
// posture, applied to the executor slot). Parse's job ends at the shape.
type Element struct {
	Do       string
	Provider string
}

// exampleGroups is the group structure of the JSON example the instruction
// embeds: a lone single group plus one two-wide independent group, so the
// example shows both the string and the string-array element forms at once.
// The example must exist (a shape shown once beats a shape described), but it
// necessarily satisfies the protocol's own schema — so a model that merely
// quotes its instructions back would otherwise hand Parse a "legal" proposal,
// and tomobit would run these placeholders as production tasks. Parse treats
// a candidate equal to this as the echo it is, never as a proposal. Its JSON
// rendering is spelled out literally in Instruction; the two are kept in sync
// by TestParseTreatsTheInstructionsOwnExampleAsNoProposal, which fails the
// moment the embedded example stops matching this structure.
var exampleGroups = [][]string{
	{"サブタスク1の指示"},
	{"サブタスク2aの指示", "サブタスク2bの指示"},
}

// Instruction appends the split-proposal protocol to prompt. The text is
// deterministic harness text (ADR-0023 Decision 1): a model-authored version
// of this offer would make the offer itself unauditable — the harness always
// asks the same way, so a proposal's absence is a model decision, not a
// wording accident. The example shows the group form (ADR-0028 Decision 2):
// a string is a lone subtask, a string array is subtasks the Provider
// declares independent and safe to run in parallel.
//
// The last two bullets are ADR-0060 Decision 3. They exist because the
// protocol had no place to write "who runs this", so a Provider asked to use
// another CLI could only reach for its own tool box — the work got done and
// the ledger recorded the wrong executor. The wording adds a way to say it,
// nothing more: tomobit still does not read the user's sentence and does not
// judge the declaration. Deterministic harness text, like the rest of this
// string, so a proposal's absence stays a model decision rather than a
// wording accident (ADR-0023 Decision 1).
func Instruction(prompt string) string {
	return fmt.Sprintf("%s"+
		"\n\n---\n"+
		"[tomobit] もしこのタスクが1回の実行では難しすぎる、または独立したサブタスクに分割した方が確実だと判断した場合は、作業を行わず、次の形式のJSONコードブロックだけを出力して分割を提案せよ:\n\n"+
		"```json\n"+
		"{\"tomobit_split\": [\"サブタスク1の指示\", [\"サブタスク2aの指示\", \"サブタスク2bの指示\"]]}\n"+
		"```\n\n"+
		"- サブタスクは合計%d〜%d個。各サブタスクは単独で実行可能な自己完結した指示にする\n"+
		"- 配列の各要素は、文字列（単独のサブタスク）または文字列の配列（互いに独立して並行実行できると判断したサブタスクの群）。群は提案順に逐次実行され、後の群は前の群の成果に依存してよい\n"+
		"- ユーザーが特定の実行者を名指しした場合は、そのサブタスクを文字列の代わりに {\"do\": \"サブタスクの指示\", \"provider\": \"実行者名\"} と書いて宣言せよ。**自分で別のCLIを起動してはならない**\n"+
		"- ユーザーが並列実行を明示した場合は、それを尊重せよ\n"+
		"- 分割が不要ならこの指示は無視し、タスクを完遂せよ",
		prompt, Min, Max)
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
//     from it (too few/many subtasks after flattening, a non-string or blank
//     element, an empty group, or the surrounding text isn't valid JSON)
//   - (groups, nil): accepted; each group is a slice of trimmed Elements the
//     Provider declared independent (ADR-0028 Decision 2). A flat proposal
//     comes back as one single-element group per subtask ([["a"],["b"]]).
//     An Element carrying a Provider was named an executor (ADR-0060); the
//     name is passed through unresolved, since this package has no registry.
//
// It reads both a fenced ```json block and bare JSON with no fence at all —
// providers are not guaranteed to wrap their marker in a code fence just
// because the instruction showed one. A candidate equal to the instruction's
// own example is the harness's text quoted back (models routinely cite their
// prompt while declining to split) and reads as no proposal, not a proposal.
func Parse(text string) ([][]Element, error) {
	if !strings.Contains(text, key) {
		return nil, nil
	}

	var lastErr error
	var sawBroken, sawEcho bool
	tryCandidate := func(c string) [][]Element {
		groups, err := parseObject(c)
		if err != nil {
			sawBroken = true
			lastErr = err
			return nil
		}
		if isExampleEcho(groups) {
			sawEcho = true
			return nil
		}
		return groups
	}

	for _, block := range marker.Fenced(text) {
		if !strings.Contains(block, key) {
			continue
		}
		if groups := tryCandidate(block); groups != nil {
			return groups, nil
		}
	}
	for _, obj := range marker.Objects(text, key) {
		if groups := tryCandidate(obj); groups != nil {
			return groups, nil
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
// instruction's embedded example (group structure included). Compared
// post-parse (element-wise, after trimming) so an echo survives whatever
// reformatting the quoting model applied to the JSON itself.
// A named executor makes a candidate not an echo: the embedded example
// carries none, so a model that wrote one wrote something of its own.
func isExampleEcho(groups [][]Element) bool {
	if len(groups) != len(exampleGroups) {
		return false
	}
	for i := range groups {
		if len(groups[i]) != len(exampleGroups[i]) {
			return false
		}
		for j := range groups[i] {
			if groups[i][j].Do != exampleGroups[i][j] || groups[i][j].Provider != "" {
				return false
			}
		}
	}
	return true
}

// parseObject validates one candidate JSON object against the protocol's
// group shape (ADR-0028 Decision 2): the array's elements are subtasks (a
// lone one) or arrays of them (a group the Provider declared independent),
// and the Min/Max bound is on the flattened total, not the element count. A
// map (not a struct) unmarshal target so a missing key is distinguishable
// from a present-but-empty one.
func parseObject(candidate string) ([][]Element, error) {
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
	groups := make([][]Element, 0, len(arr))
	total := 0
	for i, elem := range arr {
		members, isGroup := elem.([]any)
		if !isGroup {
			e, err := readElement(elem, i, -1)
			if err != nil {
				return nil, err
			}
			groups = append(groups, []Element{e})
			total++
			continue
		}
		if len(members) == 0 {
			return nil, fmt.Errorf("subtask: element %d is an empty group", i)
		}
		group := make([]Element, len(members))
		for j, gv := range members {
			e, err := readElement(gv, i, j)
			if err != nil {
				return nil, err
			}
			group[j] = e
			total++
		}
		groups = append(groups, group)
	}
	if total < Min || total > Max {
		return nil, fmt.Errorf("subtask: %d subtasks proposed, want %d-%d", total, Min, Max)
	}
	return groups, nil
}

// readElement reads one subtask in either form the protocol accepts: a bare
// string, or an object naming the executor (ADR-0060 Decision 2). j == -1
// marks a top-level lone subtask so errors name element i alone; a
// non-negative j names the group member i.j.
//
// The object form is read at the top level too, not only inside a group. A
// lone subtask is a one-member group everywhere else in this protocol, and an
// exception here would mean "you may name an executor only if you also
// declared independence" — a rule with nothing behind it.
func readElement(v any, i, j int) (Element, error) {
	switch t := v.(type) {
	case string:
		do, err := cleanDo(t, i, j)
		if err != nil {
			return Element{}, err
		}
		return Element{Do: do}, nil
	case map[string]any:
		return readNamedElement(t, i, j)
	}
	return Element{}, fmt.Errorf(`subtask: %s is neither a string nor a {"do": …} object, got %T`, elementName(i, j), v)
}

// readNamedElement reads the object form. `do` is the instruction and is
// required — an object without it declared an executor for nothing. An absent
// or blank `provider` is the ordinary default rather than an error: a blank
// name asks for exactly what a bare string asks for, and failing a whole
// proposal over an empty string would trade four subtasks for a typo the
// protocol can simply read as "inherit" (the same reasoning ADR-0060 Decision
// 2 applies to an unknown name, which the caller resolves).
func readNamedElement(m map[string]any, i, j int) (Element, error) {
	raw, ok := m["do"]
	if !ok {
		return Element{}, fmt.Errorf("subtask: %s is an object with no `do` field", elementName(i, j))
	}
	s, ok := raw.(string)
	if !ok {
		return Element{}, fmt.Errorf("subtask: %s has a non-string `do`, got %T", elementName(i, j), raw)
	}
	do, err := cleanDo(s, i, j)
	if err != nil {
		return Element{}, err
	}
	e := Element{Do: do}
	if raw, ok := m["provider"]; ok {
		name, ok := raw.(string)
		if !ok {
			return Element{}, fmt.Errorf("subtask: %s has a non-string `provider`, got %T", elementName(i, j), raw)
		}
		e.Provider = strings.TrimSpace(name)
	}
	return e, nil
}

// cleanDo trims one subtask instruction and rejects a blank one.
func cleanDo(s string, i, j int) (string, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", fmt.Errorf("subtask: %s is blank", elementName(i, j))
	}
	return trimmed, nil
}

// elementName spells an element's position for an error message: "element 2"
// for a top-level lone subtask (j == -1), "element 2.1" for a group member.
func elementName(i, j int) string {
	if j < 0 {
		return fmt.Sprintf("element %d", i)
	}
	return fmt.Sprintf("element %d.%d", i, j)
}
