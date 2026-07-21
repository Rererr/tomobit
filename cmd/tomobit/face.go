package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Rererr/tomobit/internal/facelock"
	"github.com/Rererr/tomobit/internal/presence"
)

// resolveFaceAutoLaunch is faceAutoLaunchEnabled's testable core: env
// TOMOBIT_FACE overrides the config choice, ADR-0021's env > config order. It
// now folds in the TTY gate ADR-0025 kept in maybeLaunchFace, so the whole
// decision lives in one pure place (ADR-0032 Decision 3):
//
//   - TOMOBIT_FACE=1 opts in explicitly and wins even off a terminal — the
//     caller declared a person is on the other side of the pipe (the GUI does).
//   - TOMOBIT_FACE=0 stops it, TTY or not.
//   - Silent env (unset, or a bogus value warned about and treated as unset)
//     keeps the machine-readable default: a non-TTY gets no window, and a TTY
//     honors config face_auto_launch (nil = the ADR-0025 default, on).
func resolveFaceAutoLaunch(envVal string, envSet bool, tty bool, configChoice *bool, warn io.Writer) bool {
	if envSet {
		switch envVal {
		case "0":
			return false
		case "1":
			return true
		default:
			fmt.Fprintf(warn, "warning: TOMOBIT_FACE=%q は 0 か 1 のみ — 未設定として扱う\n", envVal)
		}
	}
	// A pipe or redirect takes no side effect unless opted into above (ADR-0008):
	// config never crosses that line, so a script/CI/test never sprouts a window.
	if !tty {
		return false
	}
	if configChoice != nil {
		return *configChoice
	}
	return true
}

func faceAutoLaunchEnabled() bool {
	v, ok := os.LookupEnv("TOMOBIT_FACE")
	return resolveFaceAutoLaunch(v, ok, isTTY(os.Stdout), cfg.FaceAutoLaunch, os.Stderr)
}

// resolveFaceResident is faceResidentEnabled's testable core, a mirror of
// resolveFaceAutoLaunch but for ADR-0027's "keep the window" choice. Same
// env > config order (ADR-0021); the only difference is the default: a nil
// config choice is false = ephemeral, the new default where the window follows
// the conversation's life instead of lingering.
func resolveFaceResident(envVal string, envSet bool, configChoice *bool, warn io.Writer) bool {
	if envSet {
		switch envVal {
		case "0":
			return false
		case "1":
			return true
		default:
			fmt.Fprintf(warn, "warning: TOMOBIT_FACE_RESIDENT=%q は 0 か 1 のみ — 設定値にフォールバック\n", envVal)
		}
	}
	if configChoice != nil {
		return *configChoice
	}
	return false
}

func faceResidentEnabled() bool {
	v, ok := os.LookupEnv("TOMOBIT_FACE_RESIDENT")
	return resolveFaceResident(v, ok, cfg.FaceResident, os.Stderr)
}

// presenceRegistrationEligible reports whether this process should hold face
// presence (ADR-0032 Decision 3): a TTY always, or an explicit TOMOBIT_FACE=1
// even off a terminal. The env case is the pipe-borne window's lifeline — the
// GUI opts in with =1, and without a presence its window would sit at 0 and
// close on the grace timer (ADR-0027) the moment it opened, a dead window. A
// bogus, absent, or =0 env off a TTY registers nothing: there is no window to
// keep alive. Pure so the gate pins without a real terminal.
func presenceRegistrationEligible(tty bool, envVal string, envSet bool) bool {
	return tty || envSet && envVal == "1"
}

// registerPresence marks this CLI as a live conversation so the face window
// counts it and stays open while it runs (ADR-0027 Decision 3). Best-effort: a
// failure warns one honest line and returns a no-op release, never failing the
// command — presence governs the window's lifetime, not the work. Call the
// returned func (deferred) at the conversation's end.
//
// Registered on a TTY or an explicit TOMOBIT_FACE=1 (ADR-0032 Decision 3), the
// same reach the window's own gate has: presence exists to govern a window's
// lifetime, so a pipe with no window (and no opt-in) registers nothing rather
// than take a side effect on machine-readable output nobody watches (ADR-0008).
func registerPresence(warn io.Writer) func() {
	v, ok := os.LookupEnv("TOMOBIT_FACE")
	if !presenceRegistrationEligible(isTTY(os.Stdout), v, ok) {
		return func() {}
	}
	h, err := presence.Register()
	if err != nil {
		fmt.Fprintln(warn, "warning: 在席登録に失敗:", err, "— 顔窓の寿命管理を諦めて続行")
		return func() {}
	}
	return func() { h.Release() }
}

// maybeLaunchFace spawns tomobit-face for an interactive command (ADR-0025
// Decision 2), pointed at the DB the CLI resolved so both renderers read one
// truth (ADR-0020). Best-effort: a missing binary or a launch failure warns
// one honest line ("install it or turn it off") and never fails the command it
// decorates. The TTY gate now lives inside faceAutoLaunchEnabled (ADR-0032
// Decision 3): a non-TTY skips launch unless TOMOBIT_FACE=1 opts in.
func maybeLaunchFace(dbPath string) {
	if !faceAutoLaunchEnabled() {
		return
	}
	spawn := func() { spawnFace(dbPath, os.Stderr) }

	lockPath, err := facelock.DefaultPath()
	if err != nil {
		// No lock path (no home dir): spawn anyway — the face's own Acquire is
		// the final guard, so at worst the second one exits itself.
		spawn()
		return
	}
	probeThenSpawn(lockPath, spawn, os.Stderr)
}

// probeThenSpawn takes the machine-wide lock to check the desktop is free,
// hands it straight back, and only then runs spawn. If a face already holds it
// (ErrHeld) it skips spawn — no wasted second window; the probe releasing so
// the spawned face can take the lock is a CLI/CLI race the face side's own
// Acquire finally guards (ADR-0025 Decision 2). A genuine lock failure (broken
// ~/.tomobit, or a hand-back we could not complete) also skips, but says why.
//
// spawn is injected so a test can assert "held ⇒ spawn never runs" over a temp
// lock path with a stub, without ever launching a real window.
func probeThenSpawn(lockPath string, spawn func(), warn io.Writer) {
	l, err := facelock.Acquire(lockPath)
	if errors.Is(err, facelock.ErrHeld) {
		return
	}
	if err != nil {
		fmt.Fprintln(warn, "warning: face lock:", err, "— 顔窓の自動起動を見送る")
		return
	}
	if err := l.Release(); err != nil {
		// The face we are about to spawn would block on a lock we failed to
		// hand back, so say why rather than launch a doomed process.
		fmt.Fprintln(warn, "warning: face lock:", err, "— 顔窓の自動起動を見送る")
		return
	}
	spawn()
}

// spawnFace launches the detached face window pointed at dbPath — the
// side-effecting tail of maybeLaunchFace, split out so probeThenSpawn takes it
// as an injected action.
func spawnFace(dbPath string, warn io.Writer) {
	bin, err := faceBinary()
	if err != nil {
		fmt.Fprintln(warn, "warning: tomobit-face が見つからない — `go install ./cmd/tomobit-face` するか config の face_auto_launch: false で止める")
		return
	}

	// --resident is resolved here and passed to the child (ADR-0027 Decision 4):
	// config wiring lives on the CLI side, the same as --db. The face obeys the
	// flag rather than reading config itself, and the window's mode is fixed by
	// whichever process first opens it (later CLIs skip spawn on the lock).
	args := []string{"--db", dbPath}
	if faceResidentEnabled() {
		args = append(args, "--resident")
	}
	cmd := exec.Command(bin, args...)
	// Detach (platform-split): its own session/process group with no controlling
	// terminal, so the mascot outlives this CLI turn and never rides the
	// parent's Ctrl-C.
	cmd.SysProcAttr = detachSysProcAttr()
	cmd.Stdin = nil
	// stdout+stderr → ~/.tomobit/face.log (truncated each launch): the face is
	// detached with no terminal, so without a log a failure inside its own run
	// (font load, DB open, window creation) would be completely invisible. A
	// log we cannot open is not worth blocking the window — warn and start it
	// silent (nil = discard).
	if logF := openFaceLog(); logF != nil {
		cmd.Stdout, cmd.Stderr = logF, logF
		defer logF.Close() // the child keeps its own inherited fd after Start
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(warn, "warning: tomobit-face を起動できなかった:", err)
		return
	}
	// Reap it so a short-lived face (e.g. it lost the lock race) never lingers
	// as a zombie; we never wait on it synchronously — the CLI moves on.
	go cmd.Wait()
}

// openFaceLog opens ~/.tomobit/face.log truncated, for the detached face's
// stdout+stderr. Returns nil (the caller starts the face silent) if it cannot
// be opened, after one warning — a missing log never blocks the window.
func openFaceLog() *os.File {
	warn := func(err error) *os.File {
		fmt.Fprintln(os.Stderr, "warning: face.log:", err)
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return warn(err)
	}
	p := filepath.Join(home, ".tomobit", "face.log")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return warn(err)
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return warn(err)
	}
	return f
}

func faceBinary() (string, error) {
	dir := ""
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Dir(exe)
	}
	return findFaceBinary(dir, exec.LookPath)
}

// findFaceBinary looks for tomobit-face beside the running tomobit first (a
// `go install` puts the pair in the same bin dir), then on PATH via lookPath.
// Split from faceBinary so a test can point dir at a stub and stub lookPath
// without a real second binary — the search is exercised, no GUI is launched.
func findFaceBinary(dir string, lookPath func(string) (string, error)) (string, error) {
	if dir != "" {
		cand := filepath.Join(dir, "tomobit-face")
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand, nil
		}
	}
	return lookPath("tomobit-face")
}
