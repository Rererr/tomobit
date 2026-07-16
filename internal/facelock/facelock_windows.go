//go:build windows

package facelock

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// Lock is a held byte-range lock on the lock file. Keep the returned value
// referenced for as long as the lock must live: the handle is closed by
// Release, and dropping the last reference would let the finalizer close it
// early and silently drop the lock.
type Lock struct {
	f  *os.File
	ol windows.Overlapped
}

// Acquire takes an exclusive, immediate byte-range lock on path, creating the
// file (and ~/.tomobit) if absent. On success the returned Lock keeps the
// handle open so the lock lives until Release or process exit. If another
// process holds it the error is ErrHeld; anything else is a genuine failure.
func Acquire(path string) (*Lock, error) {
	f, err := openLockFile(path)
	if err != nil {
		return nil, err
	}
	l := &Lock{f: f}
	// Exclusive + fail-immediately is the Windows analogue of flock's
	// LOCK_EX|LOCK_NB. One byte at offset 0 is enough: the region need only
	// exist, and Windows allows locking past EOF, so an empty file locks fine.
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &l.ol)
	if err != nil {
		f.Close()
		// ERROR_LOCK_VIOLATION is the "held by someone else" signal, the
		// Windows equivalent of unix's EWOULDBLOCK — not a failure to shout.
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrHeld
		}
		return nil, fmt.Errorf("face lock: %w", err)
	}
	return l, nil
}

// Release drops the lock (the exact same region Acquire took) and closes the
// handle. Exit would free it anyway; Release exists for the CLI's
// probe-then-spawn, which must hand the lock back so the spawned face can take
// it.
func (l *Lock) Release() error {
	windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, &l.ol)
	return l.f.Close()
}
