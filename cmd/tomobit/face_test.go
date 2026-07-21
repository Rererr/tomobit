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
		tty    bool
		config *bool
		want   bool
	}{
		// TTY: env silent honors config, the ADR-0025 default on nil.
		{"tty unset+nil config is the default on", "", false, true, nil, true},
		{"tty unset honors config false", "", false, true, &no, false},
		{"tty unset honors config true", "", false, true, &yes, true},
		{"tty env 0 overrides config true", "0", true, true, &yes, false},
		{"tty env 1 overrides config false", "1", true, true, &no, true},
		{"tty bogus env falls through to config false", "yes", true, true, &no, false},
		{"tty bogus env falls through to default on", "yes", true, true, nil, true},
		// Pipe (ADR-0032 Decision 3): config never crosses; only an explicit
		// =1 opts a window in, everything else silent stays dark.
		{"pipe env 1 opts a window in even off a terminal", "1", true, false, nil, true},
		{"pipe env 0 stays dark", "0", true, false, &yes, false},
		{"pipe unset+config true stays dark — config never crosses", "", false, false, &yes, false},
		{"pipe unset+nil config stays dark", "", false, false, nil, false},
		{"pipe bogus env warns and stays dark", "yes", true, false, &yes, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveFaceAutoLaunch(tc.envVal, tc.envSet, tc.tty, tc.config, io.Discard)
			if got != tc.want {
				t.Errorf("resolveFaceAutoLaunch = %v, want %v", got, tc.want)
			}
		})
	}
}

// Presence follows the window's reach (ADR-0032 Decision 3): a TTY always, or
// an explicit TOMOBIT_FACE=1 off a terminal — so the GUI's pipe-borne window
// is never the dead window presence at 0 would close.
func TestPresenceRegistrationEligible(t *testing.T) {
	cases := []struct {
		name   string
		tty    bool
		envVal string
		envSet bool
		want   bool
	}{
		{"tty always registers", true, "", false, true},
		{"tty registers even with env 0", true, "0", true, true},
		{"pipe env 1 registers — the GUI's window lifeline", false, "1", true, true},
		{"pipe env 0 registers nothing", false, "0", true, false},
		{"pipe unset registers nothing", false, "", false, false},
		{"pipe bogus env registers nothing", false, "yes", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := presenceRegistrationEligible(tc.tty, tc.envVal, tc.envSet)
			if got != tc.want {
				t.Errorf("presenceRegistrationEligible = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveFaceResidentEnvOverConfig(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name   string
		envVal string
		envSet bool
		config *bool
		want   bool
	}{
		{"unset+nil config is the default off (ephemeral)", "", false, nil, false},
		{"unset honors config true", "", false, &yes, true},
		{"unset honors config false", "", false, &no, false},
		{"env 1 overrides config false", "1", true, &no, true},
		{"env 0 overrides config true", "0", true, &yes, false},
		{"bogus env falls through to config true", "resident", true, &yes, true},
		{"bogus env falls through to default off", "resident", true, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveFaceResident(tc.envVal, tc.envSet, tc.config, io.Discard)
			if got != tc.want {
				t.Errorf("resolveFaceResident = %v, want %v", got, tc.want)
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
