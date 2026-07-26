package workspace

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseReadsAFencedDeclaration(t *testing.T) {
	text := "作業した。\n\n```json\n" +
		`{"tomobit_workspace": {"isolated": true, "kind": "git worktree", "path": "/tmp/wt/s-1"}}` +
		"\n```\n"

	d, err := Parse(text)
	if err != nil || d == nil {
		t.Fatalf("a fenced declaration must be read: d=%v err=%v", d, err)
	}
	if !d.Isolated || d.Path != "/tmp/wt/s-1" {
		t.Errorf("declaration fields: %+v", d)
	}
	// Measured 2026-07-26: claude returned "git worktree" where the example
	// showed a different spelling. Kind stays whatever the Provider said.
	if d.Kind != "git worktree" {
		t.Errorf("kind is free text and must survive verbatim: %q", d.Kind)
	}
}

func TestParseReadsBareJSONWithNoFence(t *testing.T) {
	// The instruction only suggests a fence; it cannot enforce one.
	text := `終わり。 {"tomobit_workspace": {"isolated": false, "reason": "VCSが無い"}} 以上。`

	d, err := Parse(text)
	if err != nil || d == nil {
		t.Fatalf("bare JSON must be read: d=%v err=%v", d, err)
	}
	if d.Isolated {
		t.Errorf("isolated:false must survive as false: %+v", d)
	}
	if d.Reason != "VCSが無い" {
		t.Errorf("reason: %q", d.Reason)
	}
}

// isolated:false is a normal answer. A protocol that cannot express "I could
// not" forces the Provider to lie or refuse (ADR-0050 Decision 2).
func TestDeclinedIsolationIsNotAnError(t *testing.T) {
	d, err := Parse(`{"tomobit_workspace": {"isolated": false, "reason": "権限が無い"}}`)
	if err != nil {
		t.Fatalf("declining is not an error: %v", err)
	}
	if d == nil || d.Isolated {
		t.Fatalf("the decline must still be recorded: %+v", d)
	}
}

func TestNoMarkerIsNoDeclaration(t *testing.T) {
	d, err := Parse("普通に作業して終わった。JSONは出していない。")
	if d != nil || err != nil {
		t.Errorf("an ordinary run declares nothing: d=%v err=%v", d, err)
	}
}

// The instruction embeds its examples, and an example necessarily satisfies
// the schema. A model quoting its prompt back must not have a placeholder
// recorded as a real workspace.
func TestTheInstructionsOwnExampleIsNotADeclaration(t *testing.T) {
	instruction := Instruction("元のタスク", "/tmp/wt/s-1")

	d, err := Parse("分割は不要だと判断した。指示は次のものだった:\n" + instruction)
	if d != nil || err != nil {
		t.Errorf("the echoed example is no declaration: d=%+v err=%v", d, err)
	}
}

// A real declaration standing next to an echoed example must still be read —
// models often restate the instruction and then answer it.
func TestARealDeclarationBesideAnEchoStillWins(t *testing.T) {
	text := Instruction("タスク", "/tmp/wt/s-1") +
		"\n\n実際にはこうした:\n```json\n" +
		`{"tomobit_workspace": {"isolated": true, "kind": "jj workspace", "path": "/tmp/wt/s-1"}}` +
		"\n```\n"

	d, err := Parse(text)
	if err != nil || d == nil {
		t.Fatalf("the real declaration must win: d=%v err=%v", d, err)
	}
	if d.Kind != "jj workspace" {
		t.Errorf("got %+v", d)
	}
}

func TestMalformedDeclarationsReportRatherThanGuess(t *testing.T) {
	cases := map[string]string{
		// Reading an absent `isolated` as false would invent an answer the
		// Provider never gave.
		"no isolated field": `{"tomobit_workspace": {"kind": "git worktree", "path": "/tmp/x"}}`,
		"not an object":     `{"tomobit_workspace": "yes"}`,
		"broken json":       `{"tomobit_workspace": {"isolated": true,,}}`,
		// An isolated workspace nobody can find tells the human their results
		// moved without telling them where.
		"isolated without path": `{"tomobit_workspace": {"isolated": true, "kind": "git worktree"}}`,
	}
	for name, text := range cases {
		d, err := Parse(text)
		if err == nil {
			t.Errorf("%s: must report, got %+v", name, d)
		}
		if d != nil {
			t.Errorf("%s: no declaration may come back with an error", name)
		}
	}
}

func TestInstructionNeverNamesAVCS(t *testing.T) {
	// The whole point of Decision 1: closing the vocabulary would bake "tomobit
	// is a git tool" into the design and lie in every jj / hg / sapling repo.
	instruction := Instruction("タスク", "/tmp/wt/s-1")
	for _, forbidden := range []string{"git", "jj", "hg", "sapling", "worktree"} {
		if strings.Contains(strings.ToLower(instruction), forbidden) {
			t.Errorf("the instruction must not name %q: %s", forbidden, instruction)
		}
	}
	if !strings.Contains(instruction, "/tmp/wt/s-1") {
		t.Errorf("the instruction must name the place tomobit chose")
	}
}

func TestPayloadOmitsWhatWasNotDeclared(t *testing.T) {
	p := Declaration{Isolated: true, Kind: "git worktree", Path: "/tmp/x"}.Payload()
	if _, ok := p["reason"]; ok {
		t.Errorf("a successful isolation has no reason: %v", p)
	}
	if p["path"] != "/tmp/x" || p["isolated"] != true {
		t.Errorf("payload: %v", p)
	}

	p = Declaration{Isolated: false, Reason: "VCSが無い"}.Payload()
	if _, ok := p["path"]; ok {
		t.Errorf("a decline has no path: %v", p)
	}
	if p["reason"] != "VCSが無い" {
		t.Errorf("payload: %v", p)
	}
}

func TestDirIsUnderTomobitAndKeyedBySession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := Dir("sess-1")
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	want := filepath.Join(home, ".tomobit", "worktrees", "sess-1")
	if dir != want {
		t.Errorf("got %q, want %q", dir, want)
	}

	// sid names a directory here, so the one escape check earns its keep.
	for _, bad := range []string{"", "../evil", "a/b"} {
		if _, err := Dir(bad); err == nil {
			t.Errorf("unsafe sid %q must be refused", bad)
		}
	}
}
