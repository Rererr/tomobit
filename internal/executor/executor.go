// Package executor runs a provider CLI as a child process and turns its
// stream into canonical events (ADR-0006). An Adapter knows only how to
// launch one CLI and how to translate its stream; this common Executor owns
// the child lifecycle (start, timeout, SIGINT forwarding), assigns ts, and
// observes exit code and wall-clock duration. Nothing here touches the DB,
// connections, or seq numbering — those live behind the Sink (SCHEMA.md).
package executor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Canonical event types an Adapter may emit (SCHEMA.md R4). The vocabulary
// lives here, not in the adapters, so the Executor can recognise the two
// events it treats specially: provider.finished, whose exit_code only the
// Executor observes (see Run), and provider.error, whose presence the
// Executor reports back so a caller doesn't record the same failure twice.
const (
	EventProviderSelected = "provider.selected"
	EventProviderOutput   = "provider.output"
	EventProviderFinished = "provider.finished"
	EventProviderError    = "provider.error"
)

// PayloadDetail is a view-only payload key (ADR-0024 Decision 6): a short,
// human-readable summary of a tool_use — the file it touched, the command it
// ran — that an Adapter may attach so a turn view can show "what, where", not
// just the tool name. It never reaches the ledger: the recorded event keeps
// the tool name only (SCHEMA.md R3), because a summary in the digest would
// pressure maxEventChars/maxSessionChars and force an extractor_ver bump.
const PayloadDetail = "detail"

// viewOnlyKeys are the payload keys that exist only for display. A Sink must
// drop them before recording (StripViewOnly); the vocabulary lives here, with
// the event types, so an adapter cannot invent a display key the store side
// silently persists.
var viewOnlyKeys = []string{PayloadDetail}

// Event is one canonical event: an R4 type plus its payload. Adapters produce
// these as pure data from a single stream line; the Executor assigns ts and
// the Sink assigns seq (store side).
type Event struct {
	Type    string
	Payload map[string]any
}

// StripViewOnly returns payload without its view-only keys (PayloadDetail),
// the form a Sink records. It never mutates the input and returns a copy only
// when there is something to strip: a chat's sink shows the event first and
// records it after, so the shown map must keep its detail regardless of call
// order, and events without a detail (the common case) allocate nothing.
func StripViewOnly(payload map[string]any) map[string]any {
	hasViewOnly := false
	for _, k := range viewOnlyKeys {
		if _, ok := payload[k]; ok {
			hasViewOnly = true
			break
		}
	}
	if !hasViewOnly {
		return payload
	}
	out := make(map[string]any, len(payload))
	for k, v := range payload {
		out[k] = v
	}
	for _, k := range viewOnlyKeys {
		delete(out, k)
	}
	return out
}

// TruncateDetail trims a candidate view-only summary and caps it at 60 runes
// (not bytes, so a Japanese path is measured by characters), ending the cut
// side with an ellipsis. A path keeps its tail — the filename answers
// "where", and a deep absolute path cut from the right shows only the prefix
// every path shares (measured on a real turn: the view showed
// ~/.claude-personal/projects/… with the filename gone). Everything else
// keeps its head. A blank value returns "" so an adapter can fall through to
// its next candidate. The ruler lives here beside PayloadDetail: two adapters
// measuring the same channel differently would be a drift bug waiting.
func TruncateDetail(s string, keepTail bool) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= 60 {
		return s
	}
	if keepTail {
		return "…" + string(r[len(r)-59:])
	}
	return string(r[:59]) + "…"
}

// Request describes a single execution. The Adapter turns it into a launch
// command; the Executor enforces the timeout.
type Request struct {
	Prompt string
	// ResumeID continues a provider thread instead of starting one
	// (ADR-0022 Decision 2): the provider_session_id an earlier run of the
	// same session reported. Empty starts a fresh thread. How to continue —
	// or that the CLI cannot — is the Adapter's to know.
	ResumeID       string
	PermissionMode string
	Timeout        time.Duration // 0 = no limit
}

// Result is what the Executor observes about a run, independent of what the
// stream said (ADR-0006 Decision 2).
type Result struct {
	// Started reports whether the child process actually launched. When
	// false, ExitCode and Duration are meaningless — the caller should treat
	// this as "nothing ran", not as a failed run (e.g. skip any adoption
	// question, since there is nothing to judge).
	Started bool
	// ErrorReported reports whether the adapter's own stream already
	// produced a provider.error event (delivered to the Sink during Run).
	// A caller that also wants to record ExitCode/runErr as provider.error
	// should skip it when this is true, to avoid recording the same failure
	// twice.
	ErrorReported bool
	ExitCode      int
	Duration      time.Duration
}

// Adapter is a provider's entire surface (ADR-0006 Decision 2): it knows the
// CLI and its stream format, and nothing about the DB, connections, questions,
// or seq/ts.
type Adapter interface {
	// Name is the canonical provider name recorded as the connection target
	// (SCHEMA.md R3), e.g. "claude-code".
	Name() string
	// Command returns the executable, args, and any extra environment
	// entries ("KEY=value", appended to the parent's environment) to launch
	// for req. The launch environment is part of knowing the CLI: a provider
	// that selects a profile via an env var (e.g. CLAUDE_CONFIG_DIR) owns
	// that here, not in shell aliases around tomobit.
	Command(req Request) (name string, args []string, extraEnv []string)
	// Translate maps one stream line to zero or more canonical events. Pure:
	// no I/O and no state, so recorded fixtures can pin the mapping.
	Translate(line []byte) ([]Event, error)
}

// Sink records one canonical event in stream order. The Executor supplies ts
// (wall clock at emission); the implementation assigns seq and persists.
type Sink func(ev Event, ts int64) error

// Executor runs an Adapter's CLI and drives its events into a Sink.
type Executor struct {
	Adapter Adapter
	Stderr  io.Writer // child stderr passthrough; nil discards
	// Warn receives translate errors — stream lines the adapter could not
	// parse at all. Distinct from Debug: a malformed line is a real defect,
	// not the forward-compatible "unknown type, ignore" path, so callers
	// that want failures visible by default wire this to os.Stderr
	// unconditionally instead of gating it behind a debug flag. Nil discards.
	Warn io.Writer
	// Debug receives lower-signal diagnostics: lines the adapter recognised
	// and intentionally dropped (0 events, no error). Nil is silent.
	Debug io.Writer
}

// Run launches the adapter's command, streams stdout line by line through
// Translate, and emits each event via sink. It manages the child lifecycle
// and returns the observed exit code and duration.
//
// A non-zero exit is NOT an error — it is reported in Result.ExitCode so the
// caller can decide what to record. Run errors only on operational failures:
// the command not starting, the timeout firing, a failed emit, or an
// unexpected wait error. Cancelling ctx (SIGINT) forwards SIGINT to the child
// and returns without error; the caller detects the cancellation via its own
// context.
func (e *Executor) Run(ctx context.Context, req Request, emit Sink) (Result, error) {
	runCtx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	name, args, extraEnv := e.Adapter.Command(req)
	cmd := exec.CommandContext(runCtx, name, args...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	cmd.Stderr = e.Stderr
	// Stdin is deliberately left unset, which hands the child os.DevNull.
	// Never wire the parent's stdin in: a chat (ADR-0022) reads the next turn
	// from that same terminal while a provider is running, and codex for one
	// reads whatever stdin offers and appends it to the prompt — the child
	// would eat the user's next task and answer it as part of this one.
	// Forward SIGINT rather than the default SIGKILL, so the child CLI can
	// flush its final result line before exiting.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("%s: stdout pipe: %w", name, err)
	}
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("%s: start: %w", name, err)
	}
	start := time.Now()

	var stashedFinished *Event
	var emitErr error
	var errorReported bool
	reader := bufio.NewReader(stdout)
	for emitErr == nil {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var sawError bool
			stashedFinished, sawError, emitErr = e.emitLine(line, emit, stashedFinished)
			errorReported = errorReported || sawError
		}
		if readErr != nil {
			break
		}
	}

	if emitErr != nil {
		// The Sink can no longer record anything, so nothing is left to wait
		// for — and simply calling Wait() can hang forever: if the child has
		// more than a pipe buffer of stdout still queued, its next write(2)
		// blocks once nobody reads, and Wait() blocks on the child exiting
		// (reproduced: 5MB of child stdout + a Sink erroring on the 2nd
		// event). Kill outright instead of draining, since the data can't be
		// used anyway.
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cmd.Wait()
		return Result{Started: true, Duration: time.Since(start)}, emitErr
	}

	waitErr := cmd.Wait()
	result := Result{Started: true, Duration: time.Since(start), ErrorReported: errorReported}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}

	if stashedFinished != nil {
		stashedFinished.Payload["exit_code"] = result.ExitCode
		if err := emit(*stashedFinished, nowMs()); err != nil {
			return result, err
		}
	}
	if req.Timeout > 0 && runCtx.Err() == context.DeadlineExceeded {
		return result, fmt.Errorf("%s: timed out after %s", name, req.Timeout)
	}
	// A killed process (cancel/timeout) surfaces as an ExitError, already
	// captured in ExitCode; only a non-exit wait failure is worth reporting.
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) && runCtx.Err() == nil {
		return result, fmt.Errorf("%s: wait: %w", name, waitErr)
	}
	return result, nil
}

// emitLine translates one stream line and forwards its events to emit,
// stashing provider.finished (see Run) instead of emitting it immediately.
// It reports whether a provider.error event was seen, so Run can tell a
// caller not to record the same failure a second time.
func (e *Executor) emitLine(line []byte, emit Sink, stashed *Event) (*Event, bool, error) {
	evs, err := e.Adapter.Translate(line)
	if err != nil {
		e.warnf("translate error: %v (line: %q)", err, line)
		return stashed, false, nil
	}
	if len(evs) == 0 {
		e.debugf("dropped: %q", line)
		return stashed, false, nil
	}
	sawError := false
	for i := range evs {
		if evs[i].Type == EventProviderFinished {
			if stashed != nil {
				e.debugf("provider.finished received twice; discarding the earlier one: %+v", stashed.Payload)
			}
			f := evs[i]
			stashed = &f
			continue
		}
		if evs[i].Type == EventProviderError {
			sawError = true
		}
		if err := emit(evs[i], nowMs()); err != nil {
			return stashed, sawError, err
		}
	}
	return stashed, sawError, nil
}

func (e *Executor) warnf(format string, args ...any) {
	if e.Warn == nil {
		return
	}
	fmt.Fprintf(e.Warn, format+"\n", args...)
}

func (e *Executor) debugf(format string, args ...any) {
	if e.Debug == nil {
		return
	}
	fmt.Fprintf(e.Debug, format+"\n", args...)
}

func nowMs() int64 { return time.Now().UnixMilli() }
