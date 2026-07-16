// Package facelock guards against a second face window: the mascot is one per
// machine (ADR-0025 Decision 2), not one per DB. An exclusive advisory lock on
// ~/.tomobit/face.lock is that guard — held for the owning process's whole
// life and freed by the OS on exit or crash, so a killed face never leaves a
// stale lock that would keep the next one from ever opening.
//
// The lock primitive is platform-split (facelock_unix.go / facelock_windows.go)
// the way the rest of the tree splits syscall use (lineedit suspend, face-win
// stderr silencing): unix uses flock, Windows uses LockFileEx, both with the
// same "held for the process's life, released by the OS" semantics.
package facelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrHeld reports that another process already holds the lock — a face is
// already on the desktop. Callers distinguish it from real I/O failures so
// "someone's already home" stays silent while a broken ~/.tomobit is loud.
var ErrHeld = errors.New("face lock already held")

// DefaultPath is ~/.tomobit/face.lock — one lock per machine, beside the
// config and the default DB. Not DB-relative on purpose: the desktop holds one
// mascot regardless of how many experience DBs this machine has.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tomobit", "face.lock"), nil
}

// openLockFile creates ~/.tomobit (if absent) and opens the lock file — the
// shared prelude both platforms' Acquire runs before taking the OS lock. The
// path is an explicit argument (not DefaultPath baked in) for the same
// testability reason config.LoadFile takes one — a test drives a temp path.
func openLockFile(path string) (*os.File, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("face lock: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("face lock: %w", err)
	}
	return f, nil
}
