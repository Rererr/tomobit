package facelock

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAcquireBlocksSecondUntilReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "face.lock")

	first, err := Acquire(path)
	if err != nil {
		t.Fatalf("first acquire must succeed on a free lock: %v", err)
	}

	// flock treats each open fd independently, so a second Acquire in the same
	// process is a faithful stand-in for a second face process.
	if _, err := Acquire(path); !errors.Is(err, ErrHeld) {
		t.Fatalf("second acquire on a held lock must report ErrHeld, got %v", err)
	}

	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	again, err := Acquire(path)
	if err != nil {
		t.Fatalf("acquire after release must succeed: %v", err)
	}
	again.Release()
}

func TestAcquireCreatesMissingDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "face.lock")
	l, err := Acquire(path)
	if err != nil {
		t.Fatalf("acquire must create the parent dir: %v", err)
	}
	l.Release()
}
