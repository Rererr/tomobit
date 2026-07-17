// Package presence answers one question for the face window: "is any tomobit
// conversation alive on this machine right now?" (ADR-0027). Each live `chat`
// or `do` holds an exclusive flock on ~/.tomobit/sessions/<pid>.lock for its
// whole life; the OS frees it on exit or crash. The face counts held locks —
// it never writes truth, it reads liveness — and closes itself once none
// remain (ADR-0027 Decision 2).
//
// The lock primitive is facelock, reused verbatim: presence adds no new
// platform split, so the same "held for the process's life, released by the OS"
// semantics carry to Windows unchanged (ADR-0027 Consequences).
package presence

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Rererr/tomobit/internal/facelock"
)

// staleAfter is how old an unheld lock file must be before a count treats it as
// a crash remnant to sweep. A file younger than this that we could acquire is
// more likely a CLI mid-registration (created the file, not yet flocked) than a
// ghost, so the count leaves it alone (see CountLive). 10s dwarfs the
// open→flock gap while still clearing real crash debris promptly.
const staleAfter = 10 * time.Second

// DefaultDir is ~/.tomobit/sessions — machine-wide, beside face.lock and the
// config, DB-independent on purpose: presence is "a conversation is alive on
// this machine", regardless of which experience DB it drives (mirrors
// facelock.DefaultPath).
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tomobit", "sessions"), nil
}

// Handle is one process's held presence. Keep it referenced for the process's
// life — its flock is what makes the process count as alive; call Release when
// the conversation ends.
type Handle struct {
	lock *facelock.Lock
	path string
}

// Register marks this process as a live conversation. The returned Handle must
// stay referenced until the conversation ends; Release (usually deferred) frees
// it. A failure is worth reporting to the caller, which treats presence as
// best-effort (a lost handle only means the face may not learn to close).
func Register() (*Handle, error) {
	dir, err := DefaultDir()
	if err != nil {
		return nil, err
	}
	return registerIn(dir, os.Getpid())
}

// registerIn is Register's testable core over an explicit dir and pid (the same
// reason facelock/config take explicit paths — a test drives a temp dir).
// facelock.Acquire creates the dir and the file, so there is nothing to set up
// first.
func registerIn(dir string, pid int) (*Handle, error) {
	path := filepath.Join(dir, fmt.Sprintf("%d.lock", pid))
	// Why not just Acquire: if a prior life of this pid crashed, an old-mtime
	// <pid>.lock remains. Reopening it (Acquire creates but never truncates)
	// keeps that stale mtime, so a face probing our open→flock gap would read it
	// as a remnant and sweep it — deleting a *live* process's presence. Any
	// pre-existing file at our own pid can only be a remnant (pids are unique
	// among live processes and we have not registered yet), so removing it first
	// guarantees Acquire makes a fresh-mtime newborn the mtime guard will keep.
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("presence: %w", err)
	}
	lock, err := facelock.Acquire(path)
	if err != nil {
		return nil, fmt.Errorf("presence: %w", err)
	}
	return &Handle{lock: lock, path: path}, nil
}

// Release drops the flock and removes this process's own lock file. The lock is
// released first because Windows refuses to delete a file with an open handle
// (facelock holds one for the lock's life); the flock going away is what
// actually ends the presence, so a failed remove leaves only an empty file that
// the next count sweeps as stale.
func (h *Handle) Release() error {
	err := h.lock.Release()
	if rmErr := os.Remove(h.path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) && err == nil {
		err = fmt.Errorf("presence: %w", rmErr)
	}
	return err
}

// CountLive reports how many conversations are alive: it walks dir and probes
// each *.lock. A missing dir is 0 (nobody has registered yet), not an error.
// The walk is best-effort — a probe that fails on one file skips that file and
// keeps counting, so one broken lock never blanks the whole count.
func CountLive(dir string) (int, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count := 0
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".lock") {
			continue
		}
		if probeLive(filepath.Join(dir, e.Name())) {
			count++
		}
	}
	return count, nil
}

// probeLive tests one session lock. ErrHeld means the owner is alive → it
// counts. Acquiring it means the owner is gone OR has not finished registering:
//   - old file → a crash remnant → sweep it (does not count)
//   - fresh file → a CLI between open and flock → leave it untouched
//
// Why not always remove an acquirable file: the register path opens the file
// (mtime = now) before it flocks. A face probing in that gap would acquire the
// newborn; removing it would make a *live* CLI's presence vanish from the dir
// and wrongly close the window. The mtime guard keeps newborns; the grace in
// closeDecision is the second net. Best-effort: an I/O error skips the file.
func probeLive(path string) bool {
	l, err := facelock.Acquire(path)
	if errors.Is(err, facelock.ErrHeld) {
		return true
	}
	if err != nil {
		return false
	}
	fi, statErr := os.Stat(path)
	// Release before removing: Windows will not delete a file with an open
	// handle, and facelock keeps one until Release.
	l.Release()
	if statErr == nil && time.Since(fi.ModTime()) >= staleAfter {
		os.Remove(path)
	}
	return false
}
