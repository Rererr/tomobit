package presence

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegisterCountsAsOneAndReleaseClearsIt(t *testing.T) {
	dir := t.TempDir()

	h, err := registerIn(dir, os.Getpid())
	if err != nil {
		t.Fatalf("registerIn: %v", err)
	}

	if n, err := CountLive(dir); err != nil || n != 1 {
		t.Fatalf("a registered process counts once: got %d err %v", n, err)
	}

	if err := h.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if n, err := CountLive(dir); err != nil || n != 0 {
		t.Fatalf("after Release the presence is gone: got %d err %v", n, err)
	}
}

func TestReleaseRemovesOwnFile(t *testing.T) {
	dir := t.TempDir()
	h, err := registerIn(dir, 4219)
	if err != nil {
		t.Fatalf("registerIn: %v", err)
	}
	path := filepath.Join(dir, "4219.lock")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("register must create the lock file: %v", err)
	}
	if err := h.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Release must remove the own lock file, stat err = %v", err)
	}
}

func TestCountLiveSweepsStaleUnheldFile(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "999999.lock")
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-staleAfter - time.Second)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	if n, err := CountLive(dir); err != nil || n != 0 {
		t.Fatalf("a stale unheld remnant must not count: got %d err %v", n, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("a stale remnant must be swept, stat err = %v", err)
	}
}

func TestCountLiveKeepsFreshUnheldFile(t *testing.T) {
	dir := t.TempDir()
	// A freshly created unheld file mimics a CLI between open and flock: the
	// count must not remove the newborn, or a live CLI would vanish.
	fresh := filepath.Join(dir, "888888.lock")
	if err := os.WriteFile(fresh, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if n, err := CountLive(dir); err != nil || n != 0 {
		t.Fatalf("a fresh unheld file does not count yet: got %d err %v", n, err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("a fresh unheld file must survive the count: %v", err)
	}
}

func TestRegisterRefreshesReusedPidRemnant(t *testing.T) {
	dir := t.TempDir()
	// A prior life of this pid crashed and left an old-mtime remnant. Reusing
	// the pid must produce a fresh-mtime lock so a concurrent count never sweeps
	// the newborn during the open→flock gap.
	remnant := filepath.Join(dir, "4219.lock")
	if err := os.WriteFile(remnant, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-staleAfter - time.Hour)
	if err := os.Chtimes(remnant, old, old); err != nil {
		t.Fatal(err)
	}

	h, err := registerIn(dir, 4219)
	if err != nil {
		t.Fatalf("registerIn over a remnant: %v", err)
	}
	defer h.Release()

	fi, err := os.Stat(remnant)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if time.Since(fi.ModTime()) >= staleAfter {
		t.Fatalf("reused pid must get a fresh mtime, got age %v", time.Since(fi.ModTime()))
	}
}

func TestCountLiveMissingDirIsZero(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "never-created")
	if n, err := CountLive(dir); err != nil || n != 0 {
		t.Fatalf("a missing sessions dir is 0 live, no error: got %d err %v", n, err)
	}
}
