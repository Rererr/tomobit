//go:build darwin

package main

import (
	"os"
	"syscall"
)

// silenceSystemStderr points fd 2 at /dev/null and re-aims os.Stderr at a
// duplicate of the original, so macOS framework chatter (TSM/IMK lines on
// every focus change, written straight to fd 2 from inside AppKit) never
// lands in the launching terminal while tomobit-face's own messages still
// do. Cost: Go runtime panics also write to raw fd 2 and would go dark —
// --debug skips the rerouting when a crash needs investigating.
func silenceSystemStderr() error {
	saved, err := syscall.Dup(2)
	if err != nil {
		return err
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		syscall.Close(saved)
		return err
	}
	defer devnull.Close()
	if err := syscall.Dup2(int(devnull.Fd()), 2); err != nil {
		syscall.Close(saved)
		return err
	}
	os.Stderr = os.NewFile(uintptr(saved), "stderr")
	return nil
}
