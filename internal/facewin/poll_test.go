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

func mustOpen(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
