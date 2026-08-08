package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/config"
	"github.com/Rererr/tomobit/internal/executor"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/workspace"
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

// ---- ADR-0054 Decision 3: a split's children work where the parent works ----

// The declared isolated workspace becomes the children's cwd. Before this, the
// child's Request carried no WorkDir at all, so it ran in tomobit's own process
// cwd — under the GUI, a different repository entirely, where it would not fail
// but would quietly work on the wrong code.
func TestSubtaskWorkDirFollowsTheDeclaredIsolation(t *testing.T) {
	iso := t.TempDir()
	decl := &workspace.Declaration{Isolated: true, Kind: "git worktree", Path: iso}

	if got := subtaskWorkDir(decl, "/parent/repo"); got != iso {
		t.Errorf("children work inside the isolated workspace: got %q, want %q", got, iso)
	}
}

// No isolation declared — including an honest isolated:false — leaves the
// parent's own place in charge (ADR-0047). "Nowhere in particular" stays
// nowhere in particular: an empty parent place keeps the process cwd, which is
// what a run with no /cd already used.
func TestSubtaskWorkDirFallsBackToTheParentsPlace(t *testing.T) {
	for name, decl := range map[string]*workspace.Declaration{
		"nothing declared": nil,
		"declined":         {Isolated: false, Reason: "no version control here"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := subtaskWorkDir(decl, "/parent/repo"); got != "/parent/repo" {
				t.Errorf("got %q, want the parent's place", got)
			}
			if got := subtaskWorkDir(decl, ""); got != "" {
				t.Errorf("an empty parent place must stay empty (process cwd), got %q", got)
			}
		})
	}
}

// A declared path that is not a directory we can enter falls back rather than
// turning every child into a chdir failure. This is not verification — whether
// it really is a worktree stays unknown and unknowable (ADR-0050 Decision 2) —
// it is the same existence check /cd already runs.
func TestSubtaskWorkDirFallsBackWhenTheDeclaredPathIsNotThere(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-created")
	decl := &workspace.Declaration{Isolated: true, Path: missing}
	if got := subtaskWorkDir(decl, "/parent/repo"); got != "/parent/repo" {
		t.Errorf("a path that is not there must not be used: got %q", got)
	}

	// A file is not a place to work either.
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := subtaskWorkDir(&workspace.Declaration{Isolated: true, Path: file}, "/parent/repo"); got != "/parent/repo" {
		t.Errorf("a regular file is not a workspace: got %q", got)
	}
}

// pwdAdapter launches `pwd`, so a child's real working directory comes back as
// its own provider.output — the assertion lands on where the process actually
// ran, not on the struct that was supposed to describe it. That distinction is
// the whole bug: the wiring existed on the parent and simply never reached the
// child's Request.
type pwdAdapter struct{}

func (pwdAdapter) Name() string { return "pwd-adapter" }

func (pwdAdapter) Command(executor.Request) (string, []string, []string) {
	return "sh", []string{"-c", "pwd"}, nil
}

func (pwdAdapter) Translate(line []byte) ([]executor.Event, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	return []executor.Event{{
		Type: executor.EventProviderOutput, Payload: map[string]any{"text": string(line)},
	}}, nil
}

// grantEchoAdapter echoes the grants its Request carried. Same trick as
// pwdAdapter: the only way to see from the ledger whether a piece of the
// parent's wiring reached the child's launch is to have the child print it.
type grantEchoAdapter struct{}

func (grantEchoAdapter) Name() string { return "grant-adapter" }

func (grantEchoAdapter) Command(req executor.Request) (string, []string, []string) {
	return "sh", []string{"-c", "echo " + shellQuote("tools="+strings.Join(req.AllowedTools, ","))}, nil
}

func (grantEchoAdapter) Translate(line []byte) ([]executor.Event, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil, nil
	}
	return []executor.Event{{
		Type: executor.EventProviderOutput, Payload: map[string]any{"text": string(line)},
	}}, nil
}

// TestSplitChildrenInheritTheSessionsGrants: 許可の寿命はこのタスクで
// (ADR-0053 Decision 3)、分割の子はそのタスクの内訳である (ADR-0054) —
// 人が答えた「はい」は、内訳に分かれた瞬間には消えない。
//
// 直しているのは TestSplitChildrenRunInTheParentsPlace と同型の漏れである:
// 配線は親にあり、子の Request に載っていなかった。permMode だけが継がれて
// AllowedTools が落ちていたので、子は許可済みの道具を訊き直すか、誰も
// 端末にいなければ人が既に許した作業を拒否で落としていた。
func TestSplitChildrenInheritTheSessionsGrants(t *testing.T) {
	s := openTestStore(t)
	registerFakeProvider(t, "grant-adapter", grantEchoAdapter{})

	const parentSID = "parent-grants"
	if err := s.AppendEvent(parentSID, "task.started", 1000,
		map[string]any{"intent": "big task", "source": "production"}); err != nil {
		t.Fatal(err)
	}

	w := namedWiring("grant-adapter")
	w.allowedTools = []string{"Read", "Edit"}
	// 逐次の1本と並走の2本、どちらの起動経路も通す。
	if err := runSplit(context.Background(), s, parentSID,
		plain([][]string{{"one"}, {"two", "three"}}), "big task", w,
		bufio.NewReader(strings.NewReader("")), io.Discard,
		&fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	kids := subtaskSessionIDs(t, s, parentSID)
	if len(kids) != 3 {
		t.Fatalf("three children, got %d", len(kids))
	}
	for _, sid := range kids {
		if got := outputTextOf(t, s, sid); !strings.Contains(got, "tools=Read,Edit") {
			t.Errorf("child %s launched without the session's grants: %q", sid, got)
		}
	}
}

func TestSplitChildrenRunInTheParentsPlace(t *testing.T) {
	s := openTestStore(t)
	place, err := filepath.EvalSymlinks(t.TempDir()) // macOS: /var → /private/var
	if err != nil {
		t.Fatal(err)
	}
	registerFakeProvider(t, "pwd-adapter", pwdAdapter{})

	const parentSID = "parent-place"
	if err := s.AppendEvent(parentSID, "task.started", 1000,
		map[string]any{"intent": "big task", "source": "production"}); err != nil {
		t.Fatal(err)
	}

	w := namedWiring("pwd-adapter")
	w.workDir = place
	// A lone group and a wide one, so the sequential and the parallel launch
	// are both covered.
	if err := runSplit(context.Background(), s, parentSID,
		plain([][]string{{"one"}, {"two", "three"}}), "big task", w,
		bufio.NewReader(strings.NewReader("y\n")), io.Discard,
		&fakePerceiveExtractor{semantic: map[string]string{"lang": "go"}}); err != nil {
		t.Fatalf("runSplit: %v", err)
	}

	kids := subtaskSessionIDs(t, s, parentSID)
	if len(kids) != 3 {
		t.Fatalf("three children, got %d", len(kids))
	}
	for _, sid := range kids {
		evs, err := s.EventsBySession(sid)
		if err != nil {
			t.Fatal(err)
		}
		var ran string
		for _, e := range evs {
			if e.Type == "provider.output" {
				if text, ok := e.Payload["text"].(string); ok {
					ran = text
				}
			}
		}
		if ran != place {
			t.Errorf("child %s ran in %q, want the parent's place %q", sid, ran, place)
		}
	}
}
