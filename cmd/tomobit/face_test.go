package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Rererr/tomobit/internal/facelock"
)

func TestResolveFaceAutoLaunchEnvOverConfig(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name   string
		envVal string
		envSet bool
		config *bool
		want   bool
	}{
		{"unset+nil config is the default on", "", false, nil, true},
		{"unset honors config false", "", false, &no, false},
		{"unset honors config true", "", false, &yes, true},
		{"env 0 overrides config true", "0", true, &yes, false},
		{"env 1 overrides config false", "1", true, &no, true},
		{"bogus env falls through to config false", "yes", true, &no, false},
		{"bogus env falls through to default on", "yes", true, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveFaceAutoLaunch(tc.envVal, tc.envSet, tc.config, io.Discard)
			if got != tc.want {
				t.Errorf("resolveFaceAutoLaunch = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFindFaceBinaryPrefersSiblingThenPath(t *testing.T) {
	dir := t.TempDir()
	fromPath := func(string) (string, error) { return "/usr/bin/tomobit-face", nil }

	// No sibling yet → fall back to PATH.
	if got, err := findFaceBinary(dir, fromPath); err != nil || got != "/usr/bin/tomobit-face" {
		t.Fatalf("with no sibling, must fall back to PATH: got %q err %v", got, err)
	}

	// A sibling next to the binary wins over PATH.
	sib := filepath.Join(dir, "tomobit-face")
	if err := os.WriteFile(sib, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := findFaceBinary(dir, fromPath); err != nil || got != sib {
		t.Fatalf("a sibling must win over PATH: got %q err %v", got, err)
	}

	// Nowhere on disk or PATH → the error surfaces, never an empty success.
	notFound := func(string) (string, error) { return "", os.ErrNotExist }
	if _, err := findFaceBinary("", notFound); err == nil {
		t.Fatal("a missing binary must return an error, not empty success")
	}
}

func TestProbeThenSpawnSkipsWhenLockHeld(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "face.lock")

	// A face already on the desktop holds the lock: no second window.
	held, err := facelock.Acquire(lockPath)
	if err != nil {
		t.Fatalf("seed lock: %v", err)
	}
	defer held.Release()

	spawned := false
	probeThenSpawn(lockPath, func() { spawned = true }, io.Discard)
	if spawned {
		t.Error("a held lock must skip the spawn — no second window")
	}
}

func TestProbeThenSpawnRunsAndHandsLockBackWhenFree(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "face.lock")

	spawned := false
	probeThenSpawn(lockPath, func() { spawned = true }, io.Discard)
	if !spawned {
		t.Error("a free lock must let the spawn run")
	}

	// The probe must have released the lock so the spawned face can take it.
	after, err := facelock.Acquire(lockPath)
	if err != nil {
		t.Fatalf("probe must hand the lock back after spawning: %v", err)
	}
	after.Release()
}
