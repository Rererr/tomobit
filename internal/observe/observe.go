// Package observe is the first layer of Outcome (ADR-0003 Decision 1), finally
// given a writer (ADR-0052). It runs the command the user wired for a place and
// reads its exit code — nothing else.
//
// The distinction that justifies this package existing at all: ADR-0006
// Decision 3 deferred test.result because "子プロセス内のテスト実行を外から
// 決定的に識別できない" — you cannot tell, from someone else's stream, that a
// test just ran. That reason binds only when tomobit is watching. It does not
// bind when tomobit runs the command itself: an exit code from a process we
// started is an observation, not an identification.
//
// Nothing here knows a test runner. The command is one opaque string the user
// wrote in config, handed to `sh -c` — the same judgment GUI ADR-0007 made
// ("argv分割は安全を買わないのに使えるコマンドだけを減らす"). Providers never
// supply it: a self-reported pass would move Beta, and no Provider gets to
// write its own report card (ADR-0052 Decision 1).
package observe

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultTimeout bounds one observation. Five minutes is long enough for the
// unit suites this is meant to watch and short enough that a hung runner does
// not hold a task boundary open — a boundary the human is usually waiting at.
const DefaultTimeout = 5 * time.Minute

// Result is one completed observation. It exists only when the command ran to
// completion: a runner that failed to start or timed out produces no Result,
// because neither says anything about the deliverable (ADR-0052 Decision 4).
type Result struct {
	Passed     bool
	ExitCode   int
	DurationMs int64
	Command    string
}

// Payload is the test.result event body (SCHEMA.md R4). Only `passed` is read
// by perception; the rest is for audit — without Command, a later reader cannot
// say what `passed: true` was true *about*.
func (r Result) Payload() map[string]any {
	return map[string]any{
		"passed":      r.Passed,
		"exit_code":   r.ExitCode,
		"duration_ms": r.DurationMs,
		"command":     r.Command,
	}
}

// Lookup finds the command wired for workDir, plus the directory it should run
// in. The match is the longest configured path that contains workDir, so a
// chat that /cd'd into a subdirectory (ADR-0047) still finds the repository's
// entry; the command runs at that matched root, which is where "npm test" is
// expected to work from.
//
// An empty workDir means the process's own cwd — `do` never sets one, and a
// chat only does after /cd.
//
// This is a pure function of its inputs except for the cwd/home fallbacks, so
// the matching rule itself is pinnable without a filesystem.
func Lookup(commands map[string]string, workDir string) (command, dir string, ok bool) {
	if len(commands) == 0 {
		return "", "", false
	}
	target := absClean(workDir)
	if target == "" {
		return "", "", false
	}
	best := ""
	for key, cmd := range commands {
		if strings.TrimSpace(cmd) == "" {
			continue // a blank command is "wired to nothing", not "run nothing"
		}
		k := absClean(key)
		if k == "" || !contains(k, target) {
			continue
		}
		if len(k) > len(best) {
			best, command, dir = k, cmd, k
		}
	}
	return command, dir, best != ""
}

// contains reports whether dir is target itself or one of its ancestors. The
// separator check matters: "/a/bc" must not match under "/a/b".
func contains(dir, target string) bool {
	if dir == target {
		return true
	}
	if !strings.HasSuffix(dir, string(filepath.Separator)) {
		dir += string(filepath.Separator)
	}
	return strings.HasPrefix(target, dir)
}

// absClean expands a leading ~, resolves to an absolute path and cleans it.
// Config is hand-written JSON, so "~/dev/repo" is a shape people actually
// type; failing to expand it would silently never match.
func absClean(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		if wd, err := os.Getwd(); err == nil {
			return filepath.Clean(wd)
		}
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~"+string(filepath.Separator)) {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p[1:], string(filepath.Separator)))
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	return filepath.Clean(abs)
}

// Run executes command in dir and reports what the exit code says.
//
// The three outcomes are deliberately not three booleans:
//
//	(Result, true, nil)   the command ran; Passed is exit==0
//	(_, false, err)       it could not run, or ran out of time — record nothing
//
// A runner that never started, or never finished, observed nothing about the
// deliverable. Writing passed:false there would let a broken test setup be
// recorded as the Provider's failure, which is the ledger telling a lie
// (ADR-0010 Decision 2, applied in the direction it was written).
func Run(ctx context.Context, command, dir string, timeout time.Duration) (Result, bool, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	// Output is not kept: the ledger records the verdict, not the transcript
	// (ADR-0006「全文は記帳しない」). What a human needs to debug a red suite is
	// in their own terminal, one `sh -c` away.
	err := cmd.Run()
	elapsed := time.Since(started).Milliseconds()

	if ctx.Err() != nil {
		return Result{}, false, ctx.Err()
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return Result{Passed: true, ExitCode: 0, DurationMs: elapsed, Command: command}, true, nil
	case errors.As(err, &exitErr):
		// The command ran and disagreed with itself passing. That is the
		// observation this whole package exists to make.
		return Result{Passed: false, ExitCode: exitErr.ExitCode(), DurationMs: elapsed, Command: command}, true, nil
	default:
		// Never started: no shell, no such directory, permission denied.
		return Result{}, false, err
	}
}
