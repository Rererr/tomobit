package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rererr/tomobit/internal/config"
	"github.com/Rererr/tomobit/internal/store"
)

func workspacePayload(t *testing.T, s *store.Store, sid string) map[string]any {
	t.Helper()
	events, err := s.EventsBySession(sid)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	for _, e := range events {
		if e.Type == "task.workspace" {
			return e.Payload
		}
	}
	return nil
}

func TestRecordWorkspaceFilesADeclaration(t *testing.T) {
	s := openTestStore(t)
	const sid = "ws-1"

	recordWorkspace(s, sid, []string{
		"作業した。",
		"```json\n{\"tomobit_workspace\": {\"isolated\": true, \"kind\": \"git worktree\", \"path\": \"/tmp/wt/ws-1\"}}\n```",
	})

	p := workspacePayload(t, s, sid)
	if p == nil {
		t.Fatalf("a declaration must be recorded")
	}
	if p["isolated"] != true || p["path"] != "/tmp/wt/ws-1" {
		t.Errorf("payload: %v", p)
	}
	// The ledger says the Provider declared this, never that tomobit checked
	// it — nothing here goes near a filesystem (ADR-0050 Decision 2).
	if p["kind"] != "git worktree" {
		t.Errorf("kind is recorded verbatim: %v", p["kind"])
	}
}

func TestRecordWorkspaceIsSilentWhenNothingWasDeclared(t *testing.T) {
	s := openTestStore(t)
	const sid = "ws-none"

	recordWorkspace(s, sid, []string{"普通に作業して終わった。"})

	if p := workspacePayload(t, s, sid); p != nil {
		t.Errorf("an ordinary run records nothing, got %v", p)
	}
}

// A declined isolation is a normal answer and must reach the ledger: without
// it, "no entry" would mean both "never asked" and "could not" (ADR-0050 D2).
func TestRecordWorkspaceFilesADecline(t *testing.T) {
	s := openTestStore(t)
	const sid = "ws-declined"

	recordWorkspace(s, sid, []string{`{"tomobit_workspace": {"isolated": false, "reason": "VCSが無い"}}`})

	p := workspacePayload(t, s, sid)
	if p == nil {
		t.Fatalf("a decline must be recorded")
	}
	if p["isolated"] != false || p["reason"] != "VCSが無い" {
		t.Errorf("payload: %v", p)
	}
}

// Malformed warns and continues — never a silent fallback, never a guess
// (ADR-0050 Decision 2 / ADR-0023 Decision 1's 警告して通常フロー続行).
func TestRecordWorkspaceRefusesToGuessAtAMalformedDeclaration(t *testing.T) {
	s := openTestStore(t)
	const sid = "ws-broken"

	// `isolated` is missing: reading it as false would invent an answer.
	recordWorkspace(s, sid, []string{`{"tomobit_workspace": {"kind": "git worktree"}}`})

	if p := workspacePayload(t, s, sid); p != nil {
		t.Errorf("a malformed declaration records nothing, got %v", p)
	}
}

// AddDirs gets the parent (which must exist for the scope to mean anything)
// while the prompt names the leaf the Provider creates. Measured 2026-07-26:
// this exact split is what claude followed on the first try.
func TestIsolationDirCreatesOnlyTheParent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	parent, child, err := isolationDir("sess-42")
	if err != nil {
		t.Fatalf("isolationDir: %v", err)
	}
	if filepath.Dir(child) != parent {
		t.Errorf("child must sit in parent: %q / %q", parent, child)
	}
	if fi, err := os.Stat(parent); err != nil || !fi.IsDir() {
		t.Errorf("the parent must exist for AddDirs to name it: %v", err)
	}
	// `git worktree add` and its equivalents want to make the leaf themselves.
	if _, err := os.Stat(child); !os.IsNotExist(err) {
		t.Errorf("the leaf must NOT be pre-created: %v", err)
	}
}

func TestIsolateProtocolKillSwitch(t *testing.T) {
	saved := cfg
	t.Cleanup(func() { cfg = saved })

	cfg = config.Config{}
	if !isolateProtocolEnabled() {
		t.Errorf("an absent key must leave the protocol on (ADR-0050 Decision 5)")
	}

	no := false
	cfg = config.Config{IsolateProtocol: &no}
	if isolateProtocolEnabled() {
		t.Errorf("an explicit false must stop the protocol")
	}
}
