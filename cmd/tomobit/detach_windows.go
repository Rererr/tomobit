//go:build windows

package main

import "syscall"

// Process-creation flags (stdlib syscall does not export DETACHED_PROCESS).
// Detach from the parent console and start a new process group, the Windows
// analogue of unix Setsid: the face keeps running with no console of its own
// and Ctrl-C in the CLI's console never reaches it.
const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
)

func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
}
