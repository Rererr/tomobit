//go:build darwin

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
)

// TestSilenceDropsRawFd2ButKeepsGoStderr pins the split this exists for: a
// write to raw fd 2 (what AppKit does) vanishes, a write via the os.Stderr
// var (what tomobit-face and facewin do) still reaches the original stream.
func TestSilenceDropsRawFd2ButKeepsGoStderr(t *testing.T) {
	origStderr := os.Stderr
	savedFd2, err := syscall.Dup(2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		syscall.Dup2(savedFd2, 2)
		syscall.Close(savedFd2)
		os.Stderr = origStderr
	}()

	// Stand a pipe in for the launching terminal.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Dup2(int(w.Fd()), 2); err != nil {
		t.Fatal(err)
	}
	w.Close()

	if err := silenceSystemStderr(); err != nil {
		t.Fatal(err)
	}

	fmt.Fprintln(os.Stderr, "go-side message")
	syscall.Write(2, []byte("raw fd2 noise\n"))

	os.Stderr.Close() // last writer on the pipe — reader sees EOF
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "go-side message") {
		t.Errorf("os.Stderr writes must survive the rerouting, got %q", got)
	}
	if strings.Contains(string(got), "noise") {
		t.Errorf("raw fd 2 writes must be dropped, got %q", got)
	}
}
