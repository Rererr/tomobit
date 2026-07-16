//go:build !darwin

package main

// silenceSystemStderr is a no-op off macOS: the focus-change stderr noise
// this suppresses is an AppKit behavior, and rerouting fd 2 elsewhere would
// cost panic visibility for nothing.
func silenceSystemStderr() error { return nil }
