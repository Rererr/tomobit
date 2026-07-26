package facelock

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
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

// AcquireWithin は「機械に1本ずつ」を待って通す第3の使い手（知覚のキュー、
// GUI ADR-0009 Decision 5）のための入口。窓が4つあれば境界も4つ同時に来うるが、
// ローカルのモデルは1つしかない。
func TestAcquireWithinWaitsForTheSlotThenTakesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perceive.lock")

	held, err := Acquire(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	// 先客が居るあいだは取れない。
	if _, err := AcquireWithin(path, 50*time.Millisecond); !errors.Is(err, ErrHeld) {
		t.Fatalf("待ちきれなければ ErrHeld、got %v", err)
	}

	go func() {
		time.Sleep(120 * time.Millisecond)
		held.Release()
	}()
	lock, err := AcquireWithin(path, 3*time.Second)
	if err != nil {
		t.Fatalf("空いたら取れる: %v", err)
	}
	lock.Release()
}

// 待ちきれないことは失敗ではない: 呼び出し側はセッションを pending に残し、
// あとで `tomobit perceive` が消化する。境界を人質に取らないための降り方。
func TestAcquireWithinReportsHeldRatherThanHanging(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perceive.lock")
	held, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	start := time.Now()
	if _, err := AcquireWithin(path, 200*time.Millisecond); !errors.Is(err, ErrHeld) {
		t.Fatalf("ErrHeld を返す: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("期限を大きく超えて待った: %v", elapsed)
	}
}
