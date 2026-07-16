//go:build unix

package lineedit

import "syscall"

// suspendSupported is true on unix, where Ctrl-Z is a job-control stop.
const suspendSupported = true

// raiseSIGTSTP stops this process group the way the shell expects, so `fg`
// resumes the whole job. pid 0 targets the caller's process group; the call
// returns once SIGCONT arrives, which is where reclaiming raw mode belongs.
func raiseSIGTSTP() { syscall.Kill(0, syscall.SIGTSTP) }
