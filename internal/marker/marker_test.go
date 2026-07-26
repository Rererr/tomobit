package marker

import "testing"

const key = `"tomobit_split"`

func TestFencedStripsTheLanguageTagButKeepsBareBodies(t *testing.T) {
	blocks := Fenced("前置き\n```json\n{\"a\": 1}\n```\n中\n```\n{\"b\": 2}\n```\n")
	if len(blocks) != 2 {
		t.Fatalf("both blocks must be found: %q", blocks)
	}
	if got := blocks[0]; got != "{\"a\": 1}\n" {
		t.Errorf("the json tag line is stripped: %q", got)
	}
	// An untagged fence has no tag line to strip, so its body keeps the newline
	// that followed the opening ```. Removing it unconditionally would eat the
	// first line of a tagless block; the leading whitespace is harmless because
	// every caller trims before unmarshalling.
	if got := blocks[1]; got != "\n{\"b\": 2}\n" {
		t.Errorf("an untagged fence keeps its whole body: %q", got)
	}
}

func TestFencedStopsAtAnUnclosedFence(t *testing.T) {
	// Whatever came before a broken fence is still a legal candidate; the scan
	// simply stops rather than discarding everything.
	blocks := Fenced("```json\n{\"a\": 1}\n```\n```json\n{\"unclosed\": true}\n")
	if len(blocks) != 1 {
		t.Fatalf("only the closed block is returned: %q", blocks)
	}
}

func TestObjectsFindsTheObjectFramingTheKey(t *testing.T) {
	objs := Objects(`話 {"tomobit_split": ["a", "b"]} 続き`, key)
	if len(objs) == 0 {
		t.Fatalf("the framing object must be found")
	}
	if objs[0] != `{"tomobit_split": ["a", "b"]}` {
		t.Errorf("got %q", objs[0])
	}
}

// The nearest `{` is usually the object's start but not always: it may sit
// inside an earlier member's string value. A failed nearest anchor must not
// hide the real object at a farther one.
func TestObjectsTriesFartherAnchorsWhenTheNearestIsInsideAString(t *testing.T) {
	text := `{"note": "a { in prose", "tomobit_split": ["a", "b"]}`
	objs := Objects(text, key)

	var found bool
	for _, o := range objs {
		if o == text {
			found = true
		}
	}
	if !found {
		t.Errorf("the whole object must be among the candidates: %q", objs)
	}
}

// A brace inside a JSON string must not desync the depth count, or the object
// would appear to close early.
func TestObjectsTreatsQuotedBracesAsOpaque(t *testing.T) {
	text := `{"tomobit_split": ["a }", "b"]}`
	objs := Objects(text, key)
	if len(objs) == 0 || objs[0] != text {
		t.Errorf("a quoted brace must not close the object: %q", objs)
	}
}

func TestObjectsIsEmptyWithoutTheKey(t *testing.T) {
	if objs := Objects(`{"something": "else"}`, key); len(objs) != 0 {
		t.Errorf("no key, no candidates: %q", objs)
	}
}

// Two protocols now share this scan (ADR-0023's split and ADR-0050's
// workspace), so the key must be a parameter and not baked in.
func TestObjectsWorksForAnyProtocolKey(t *testing.T) {
	const wsKey = `"tomobit_workspace"`
	text := `{"tomobit_workspace": {"isolated": true}}`
	if objs := Objects(text, wsKey); len(objs) == 0 || objs[0] != text {
		t.Errorf("a second protocol's key must work the same: %q", objs)
	}
}
