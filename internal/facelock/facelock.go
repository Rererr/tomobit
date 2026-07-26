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
	"time"
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
// AcquireWithin waits up to d for the lock, polling because flock's blocking
// mode has no timeout and a boundary must not hang on one.
//
// The third user of this primitive (after the face window and presence) is the
// perception queue: several `tomobit chat` processes can reach their boundary
// at once — one per GUI pane (GUI ADR-0009 Decision 5) — and each would fire
// its own local model run. Serialising them costs nothing the ledger cares
// about, since out-of-order perception is already handled (ADR-0041).
//
// ErrHeld comes back when d elapses with the lock still taken. That is not a
// failure: the caller leaves the session pending, exactly as it does when the
// model is unreachable, and `tomobit perceive` picks it up later.
func AcquireWithin(path string, d time.Duration) (*Lock, error) {
	deadline := time.Now().Add(d)
	for {
		lock, err := Acquire(path)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, ErrHeld) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, ErrHeld
		}
		time.Sleep(pollInterval)
	}
}

// pollInterval is short enough that a queued boundary starts promptly once the
// one ahead finishes, and long enough not to spin.
const pollInterval = 150 * time.Millisecond

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
