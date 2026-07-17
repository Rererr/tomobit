package facewin

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/voice"
)

const nowMs = int64(1_800_000_000_000)

// TestPollerMissingDB: the window outlives its database — a missing file is
// the empty view, never an error (the CLI may not have run yet).
func TestPollerMissingDB(t *testing.T) {
	p := &Poller{Path: filepath.Join(t.TempDir(), "absent.db")}
	u, err := p.Poll(nowMs)
	if err != nil {
		t.Fatalf("missing DB must not error: %v", err)
	}
	if u.Stage != 0 || len(u.Lines) != 0 {
		t.Errorf("missing DB: want silent S0, got stage %d lines %v", u.Stage, u.Lines)
	}
}

// TestPollerFirstMeeting: the first successful poll of an empty network
// speaks catalog #6 exactly once.
func TestPollerFirstMeeting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	s := mustOpen(t, path)
	defer s.Close()

	p := &Poller{Path: path}
	u, err := p.Poll(nowMs)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Lines) != 1 || u.Lines[0] != voice.FirstMeeting() {
		t.Errorf("want FirstMeeting once, got %v", u.Lines)
	}
	u, err = p.Poll(nowMs + 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Lines) != 0 {
		t.Errorf("second poll must be silent, got %v", u.Lines)
	}
}

// TestPollerAskedBubble: a tomo.asked event lands in the tail and re-derives
// the same question line the terminal asked (ADR-0020 Decision 2).
func TestPollerAskedBubble(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	s := mustOpen(t, path)
	defer s.Close()

	p := &Poller{Path: path}
	if _, err := p.Poll(nowMs); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent("ask", "tomo.asked", nowMs+100, map[string]any{
		"scope": []string{"lang=go"},
		"pair":  []string{"claude-code", "codex"},
	}); err != nil {
		t.Fatal(err)
	}
	u, err := p.Poll(nowMs + 500)
	if err != nil {
		t.Fatal(err)
	}
	want := voice.Asked(core.NewScope("lang=go"), "claude-code", "codex")
	if len(u.Lines) != 1 || u.Lines[0] != want {
		t.Errorf("want %q, got %v", want, u.Lines)
	}
	// The tail advanced: the same event never bubbles twice.
	if u, _ = p.Poll(nowMs + 1000); len(u.Lines) != 0 {
		t.Errorf("event replayed: %v", u.Lines)
	}
}

// TestPollerGrowthLine: a stage transition between two polls speaks the
// growth line — the window-side version of the Apply-boundary comparison.
func TestPollerGrowthLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	s := mustOpen(t, path)
	defer s.Close()

	p := &Poller{Path: path}
	u, err := p.Poll(nowMs)
	if err != nil {
		t.Fatal(err)
	}
	if u.Stage != 0 {
		t.Fatalf("empty network should be S0, got %d", u.Stage)
	}

	if err := s.UpsertConnection(&core.Connection{
		Kind: core.ConnCapability, ScopeKey: "lang=go", Target: "claude-code",
		Alpha: 4, Beta: 1, LastUpdate: nowMs, BornTS: nowMs,
	}); err != nil {
		t.Fatal(err)
	}
	u, err = p.Poll(nowMs + 500)
	if err != nil {
		t.Fatal(err)
	}
	if u.Stage == 0 {
		t.Fatalf("stage should have grown, still %d", u.Stage)
	}
	if len(u.Lines) != 1 || !strings.Contains(u.Lines[0], "育った") {
		t.Errorf("want a growth line, got %v", u.Lines)
	}
}

// TestPollerDormantMarker: an all-dormant network maps to the "z" marker —
// the same face.Mood the terminal uses.
func TestPollerDormantMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	s := mustOpen(t, path)
	defer s.Close()

	old := nowMs - 2*core.HalfLifeMs - 1000
	if err := s.UpsertConnection(&core.Connection{
		Kind: core.ConnCapability, ScopeKey: "lang=go", Target: "claude-code",
		Alpha: 4, Beta: 1, LastUpdate: old, BornTS: old,
	}); err != nil {
		t.Fatal(err)
	}
	p := &Poller{Path: path}
	u, err := p.Poll(nowMs)
	if err != nil {
		t.Fatal(err)
	}
	if u.Marker != "z" {
		t.Errorf("want dormant marker %q, got %q", "z", u.Marker)
	}
}

// TestPollerActiveThoughtsDuel: two running siblings become two thoughts (a
// duel, ADR-0026 Decision 5), each folded to its provider and latest text,
// ordered by session id for a stable left/right. The duel parent — started but
// with no provider.output of its own — contributes no thought.
func TestPollerActiveThoughtsDuel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	s := mustOpen(t, path)
	defer s.Close()

	p := &Poller{Path: path}
	if _, err := p.Poll(nowMs); err != nil {
		t.Fatal(err)
	}

	must := func(sid, typ string, payload map[string]any) {
		if err := s.AppendEvent(sid, typ, nowMs+100, payload); err != nil {
			t.Fatal(err)
		}
	}
	// Parent: running, no output of its own.
	must("parent", "task.started", map[string]any{"intent": "x"})
	must("parent", "task.duel", map[string]any{"pair": []string{"claude-code", "codex"}})
	// Child a.
	must("a-child", "task.started", map[string]any{"parent": "parent"})
	must("a-child", "provider.selected", map[string]any{"provider": "claude-code"})
	must("a-child", "provider.output", map[string]any{"text": "まず全体を読む"})
	must("a-child", "provider.output", map[string]any{"text": "実装に入る"}) // latest wins
	// Child b.
	must("b-child", "task.started", map[string]any{"parent": "parent"})
	must("b-child", "provider.selected", map[string]any{"provider": "codex"})
	must("b-child", "provider.output", map[string]any{"text": "テストから書く"})

	u, err := p.Poll(nowMs + 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Thoughts) != 2 {
		t.Fatalf("two running siblings should be two thoughts, got %d: %+v", len(u.Thoughts), u.Thoughts)
	}
	if u.Thoughts[0].Provider != "claude-code" || u.Thoughts[0].Text != "実装に入る" {
		t.Errorf("thought[0] = %+v, want claude-code/実装に入る (latest)", u.Thoughts[0])
	}
	if u.Thoughts[1].Provider != "codex" || u.Thoughts[1].Text != "テストから書く" {
		t.Errorf("thought[1] = %+v, want codex/テストから書く", u.Thoughts[1])
	}
}

// TestPollerActiveThoughtsClearOnFinish: a thought is present only while its
// session runs — task.finished removes it the next poll (ADR-0026 Decision 5).
func TestPollerActiveThoughtsClearOnFinish(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	s := mustOpen(t, path)
	defer s.Close()

	p := &Poller{Path: path}
	if _, err := p.Poll(nowMs); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent("d", "task.started", nowMs+100, map[string]any{"intent": "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent("d", "provider.output", nowMs+101, map[string]any{"text": "考え中"}); err != nil {
		t.Fatal(err)
	}
	if u, _ := p.Poll(nowMs + 200); len(u.Thoughts) != 1 {
		t.Fatalf("a running session should show one thought, got %d", len(u.Thoughts))
	}
	if err := s.AppendEvent("d", "task.finished", nowMs+300, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if u, _ := p.Poll(nowMs + 400); len(u.Thoughts) != 0 {
		t.Errorf("a finished session should show no thought, got %d", len(u.Thoughts))
	}
}

func mustOpen(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
