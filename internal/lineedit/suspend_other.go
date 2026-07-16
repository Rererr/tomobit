//go:build !unix

package lineedit

// suspendSupported is false off unix: Windows has no SIGTSTP job control, so
// Ctrl-Z is left alone rather than half-restoring the terminal for no stop.
const suspendSupported = false

func raiseSIGTSTP() {}
