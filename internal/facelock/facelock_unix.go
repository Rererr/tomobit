//go:build unix

package facelock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Lock is a held flock. Keep the returned value referenced for as long as the
// lock must live: the fd is closed by Release, and dropping the last reference
// would let the finalizer close it early and silently drop the flock.
type Lock struct {
	f *os.File
}

// Acquire takes an exclusive, non-blocking flock on path, creating the file
// (and ~/.tomobit) if absent. On success the returned Lock keeps the fd open
// so the flock lives until Release or process exit. If another process holds
// it the error is ErrHeld; anything else is a genuine failure worth reporting.
func Acquire(path string) (*Lock, error) {
	f, err := openLockFile(path)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		// EWOULDBLOCK is the "held by someone else" signal, not a failure:
		// separate it so callers can exit quietly instead of shouting.
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrHeld
		}
		return nil, fmt.Errorf("face lock: %w", err)
	}
	return &Lock{f: f}, nil
}

// Release drops the flock and closes the fd. Exit would free it anyway; Release
// exists for the CLI's probe-then-spawn, which takes the lock only to check it
// is free and must hand it back so the face it spawns can take it.
func (l *Lock) Release() error {
	// Closing already drops the flock; an explicit LOCK_UN first makes the
	// intent obvious and lets a close error still surface on its own.
	syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	return l.f.Close()
}
