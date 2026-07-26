// Package workspace implements the isolation protocol (ADR-0050): every
// eligible run carries a deterministic instruction telling the Provider to
// split its own workspace off with whatever version control the project uses,
// and to declare what it did.
//
// This is the split protocol's shape reused (ADR-0023 / ADR-0028): the harness
// hands over material and writes down the answer; the judgment — whether to
// isolate, and how — belongs to the Provider, because it is a question about
// meaning (ADR-0011). tomobit holds no VCS vocabulary at all: the instruction
// never says "git", and Kind is a free string precisely so that closing it
// into an enum cannot smuggle one in.
//
// What this package deliberately does NOT do is verify. tomobit cannot check
// that a path really is a worktree without learning git, so the ledger records
// that the Provider *said so*, never that it *is so* (ADR-0050 Decision 2, in
// the lineage of ADR-0010 Decision 2: 観測できないものを推測で埋めない).
//
// Pure except for Dir's home lookup — recorded provider text pins the parsing.
package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Rererr/tomobit/internal/marker"
)

// key is the marker the protocol looks for, quoted exactly as it appears in
// JSON — searching the quoted form skips prose that merely mentions the name.
const key = `"tomobit_workspace"`

// The angle-bracket placeholders the instruction's examples carry. A real
// declaration never contains them, so they are an exact echo signature: a
// model that quotes its instructions back (which models routinely do while
// explaining themselves) hands Parse a schema-legal object, and without this
// tomobit would record a placeholder as a real workspace. The split protocol
// hit the identical trap and solved it the same way (ADR-0023 Decision 1).
const (
	placeholderKind   = "<使った手段の名前>"
	placeholderPath   = "<作った場所>"
	placeholderReason = "<理由>"
)

// Declaration is what a Provider said about its workspace.
//
// Isolated false is a normal answer, not a failure: a project with no version
// control, a submodule, an LFS checkout, or a run without permission to write
// outside its cwd genuinely cannot be split off. A protocol with no honest way
// to say so leaves a Provider two options — lie, or refuse the work
// (ADR-0050 Decision 2).
type Declaration struct {
	Isolated bool
	// Kind is free text ("git worktree", "jj workspace", …). Measured
	// 2026-07-26: claude returned "git worktree" where the instruction's
	// example showed a different spelling, which is exactly why this is not
	// an enum.
	Kind string
	// Path is where the isolated workspace was made. Required when Isolated,
	// since a declaration nobody can follow tells the human nothing about
	// where their results are.
	Path string
	// Reason is why isolation was not possible. Only meaningful when the
	// Provider declined.
	Reason string
}

// Instruction appends the isolation protocol to prompt, naming dir as the
// place to build the workspace in.
//
// tomobit picks the place rather than letting the Provider choose: a workspace
// made outside the run's permitted scope (ADR-0047 AddDirs) is one the
// Provider cannot write to. That is a constraint being mapped, not a judgment
// being made — the same distinction ADR-0028 Decision 4 drew when one terminal
// forced human subtasks to run sequentially.
//
// The text says "version control this project uses", never "git". Which tool
// exists here is a fact about the project that the Provider can see and
// tomobit cannot.
func Instruction(prompt, dir string) string {
	return fmt.Sprintf("%s"+
		"\n\n---\n"+
		"[tomobit] この作業場は、他の作業と同時に使われている可能性がある。作業を始める前に、このプロジェクトが使っているバージョン管理の隔離手段で自分の作業場を分け、%s に用意してそこで作業せよ。分けたら、出力の最後に次の形式のJSONコードブロックで宣言せよ:\n\n"+
		"```json\n"+
		"{\"tomobit_workspace\": {\"isolated\": true, \"kind\": \"%s\", \"path\": \"%s\"}}\n"+
		"```\n\n"+
		"- 分けられない場合は、分けずにそのまま作業し、同じ形式でその事実を宣言せよ:\n\n"+
		"```json\n"+
		"{\"tomobit_workspace\": {\"isolated\": false, \"reason\": \"%s\"}}\n"+
		"```\n\n"+
		"- 隔離できないこと自体は失敗ではない。正直に false を返すこと\n"+
		"- 宣言は作業の後で構わないが、必ず1度だけ出力せよ",
		prompt, dir, placeholderKind, placeholderPath, placeholderReason)
}

// ErrEcho reports that the only candidate found was the instruction's own
// example quoted back. Callers treat it as "nothing was declared", not as a
// broken declaration — the run is ordinary and unremarkable.
var ErrEcho = errors.New("workspace: the instruction's own example was echoed")

// Parse reads text (a run's concatenated provider.output) for a declaration.
// Three outcomes keep "nothing was declared" apart from "something was
// declared but is broken" (ADR-0050 Decision 2 / ADR-0023 Decision 1's
// 警告して通常フロー続行 — never a silent fallback either way):
//
//   - (nil, nil): no marker anywhere, or only the example echoed back
//   - (nil, err): the marker is present but no legal declaration could be read
//   - (decl, nil): accepted
//
// Both a fenced ```json block and bare JSON are read: providers are not
// guaranteed to fence their marker just because the instruction showed one.
func Parse(text string) (*Declaration, error) {
	if !strings.Contains(text, key) {
		return nil, nil
	}

	var lastErr error
	var sawBroken, sawEcho bool
	try := func(candidate string) *Declaration {
		d, err := parseObject(candidate)
		switch {
		case errors.Is(err, ErrEcho):
			sawEcho = true
			return nil
		case err != nil:
			sawBroken = true
			lastErr = err
			return nil
		}
		return d
	}

	for _, block := range marker.Fenced(text) {
		if !strings.Contains(block, key) {
			continue
		}
		if d := try(block); d != nil {
			return d, nil
		}
	}
	for _, obj := range marker.Objects(text, key) {
		if d := try(obj); d != nil {
			return d, nil
		}
	}

	if sawBroken {
		return nil, lastErr
	}
	if sawEcho {
		return nil, nil
	}
	return nil, fmt.Errorf("workspace: found %s but no JSON object could be extracted around it", key)
}

// envelope mirrors the wire shape. Isolated is a pointer so an absent key is
// distinguishable from an explicit false: a declaration that never said
// whether it isolated is malformed, and reading it as "did not isolate" would
// invent an answer the Provider never gave.
type envelope struct {
	Workspace *struct {
		Isolated *bool  `json:"isolated"`
		Kind     string `json:"kind"`
		Path     string `json:"path"`
		Reason   string `json:"reason"`
	} `json:"tomobit_workspace"`
}

func parseObject(candidate string) (*Declaration, error) {
	var env envelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(candidate)), &env); err != nil {
		return nil, fmt.Errorf("workspace: %s is not valid JSON: %w", key, err)
	}
	if env.Workspace == nil {
		return nil, fmt.Errorf("workspace: %s is present but not an object", key)
	}
	w := env.Workspace
	if w.Isolated == nil {
		return nil, fmt.Errorf("workspace: %s has no `isolated` field", key)
	}

	kind := strings.TrimSpace(w.Kind)
	path := strings.TrimSpace(w.Path)
	reason := strings.TrimSpace(w.Reason)
	if kind == placeholderKind || path == placeholderPath || reason == placeholderReason {
		return nil, ErrEcho
	}

	if !*w.Isolated {
		return &Declaration{Isolated: false, Reason: reason}, nil
	}
	if path == "" {
		// An isolated workspace nobody can find is worse than none: the human
		// is told their results moved, without being told where.
		return nil, fmt.Errorf("workspace: declared isolated but gave no `path`")
	}
	return &Declaration{Isolated: true, Kind: kind, Path: path}, nil
}

// Payload is the task.workspace event body (SCHEMA.md R4). Absent fields are
// omitted rather than written empty: the ledger says what was declared, and an
// empty `reason` on a successful isolation is not a fact about anything.
func (d Declaration) Payload() map[string]any {
	p := map[string]any{"isolated": d.Isolated}
	if d.Isolated {
		if d.Kind != "" {
			p["kind"] = d.Kind
		}
		p["path"] = d.Path
		return p
	}
	if d.Reason != "" {
		p["reason"] = d.Reason
	}
	return p
}

// Dir is where this session's isolated workspace goes:
// ~/.tomobit/worktrees/<sid>. Under ~/.tomobit for the same reason facelock
// and presence live there — it is machine state beside the config, not
// something to grow inside somebody's repository (ADR-0050 Decision 4) — and
// keyed by sid so the ledger's `path` can be traced back to its session.
func Dir(sid string) (string, error) {
	if strings.ContainsAny(sid, `/\`) || strings.Contains(sid, "..") || sid == "" {
		// sid is harness-generated, but it names a directory here, so the one
		// check that keeps it from escaping the tree is cheap to keep.
		return "", fmt.Errorf("workspace: unsafe session id %q", sid)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tomobit", "worktrees", sid), nil
}
