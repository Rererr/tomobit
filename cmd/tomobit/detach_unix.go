//go:build unix

package main

import "syscall"

// detachSysProcAttr puts the face in its own session with no controlling
// terminal, so it outlives the CLI turn and never rides the parent's Ctrl-C.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
