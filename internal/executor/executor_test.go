package executor

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeAdapter launches a chosen command and maps its lines by a tiny grammar,
// so child-process management can be tested without a real provider CLI.
type fakeAdapter struct {
	cmd  string
	args []string
}

func (f *fakeAdapter) Name() string { return "fake" }

func (f *fakeAdapter) Command(Request) (string, []string, []string) { return f.cmd, f.args, nil }

func (f *fakeAdapter) Translate(line []byte) ([]Event, error) {
	switch s := strings.TrimSpace(string(line)); s {
	case "":
		return nil, nil
	case "DROP":
		return nil, nil
	case "BAD":
		return nil, fmt.Errorf("unparseable")
	case "FIN":
		return []Event{{Type: EventProviderFinished, Payload: map[string]any{"duration_ms": int64(5)}}}, nil
	case "ERR":
		return []Event{{Type: EventProviderError, Payload: map[string]any{"message": "boom"}}}, nil
	default:
		return []Event{{Type: EventProviderOutput, Payload: map[string]any{"text": s}}}, nil
	}
}

type recorded struct {
	ev Event
	ts int64
}

func collect(got *[]recorded) Sink {
	return func(ev Event, ts int64) error {
		*got = append(*got, recorded{ev, ts})
		return nil
	}
}

// printfCmd emits the given already-newline-terminated text on stdout and
// exits 0, using printf so no shell quoting is involved.
func printfCmd(lines string) *fakeAdapter {
	return &fakeAdapter{cmd: "printf", args: []string{lines}}
}

func TestRunEmitsTranslatedEventsInStreamOrder(t *testing.T) {
	var got []recorded
	ex := &Executor{Adapter: printfCmd("hello\nworld\n")}
	res, err := ex.Run(context.Background(), Request{}, collect(&got))
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit code: got %d, want 0", res.ExitCode)
	}
	if len(got) != 2 || got[0].ev.Payload["text"] != "hello" || got[1].ev.Payload["text"] != "world" {
		t.Fatalf("expected hello then world, got %v", got)
	}
	if got[0].ts == 0 {
		t.Errorf("Executor must assign ts")
	}
}

func TestRunInjectsObservedExitCodeIntoFinishedAndEmitsItLast(t *testing.T) {
	var got []recorded
	ex := &Executor{Adapter: printfCmd("FIN\nafter\n")}
	if _, err := ex.Run(context.Background(), Request{}, collect(&got)); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected output + finished, got %v", got)
	}
	last := got[len(got)-1].ev
	if last.Type != EventProviderFinished {
		t.Fatalf("finished must be emitted last (after exit is known), got %v", got)
	}
	if last.Payload["exit_code"] != 0 {
		t.Errorf("exit_code should be filled by the Executor: got %v", last.Payload["exit_code"])
	}
	if last.Payload["duration_ms"] != int64(5) {
		t.Errorf("adapter payload must be preserved: got %v", last.Payload)
	}
}

func TestRunObservesNonZeroExitAndInjectsIt(t *testing.T) {
	var got []recorded
	ex := &Executor{Adapter: &fakeAdapter{cmd: "sh", args: []string{"-c", "printf 'FIN\\n'; exit 7"}}}
	res, err := ex.Run(context.Background(), Request{}, collect(&got))
	if err != nil {
		t.Fatalf("a non-zero exit is not an executor error: %v", err)
	}
	if res.ExitCode != 7 {
		t.Errorf("exit code: got %d, want 7", res.ExitCode)
	}
	if len(got) != 1 || got[0].ev.Payload["exit_code"] != 7 {
		t.Errorf("non-zero exit should be injected into finished: got %v", got)
	}
}

func TestRunSkipsUnknownLinesWithoutError(t *testing.T) {
	var got []recorded
	ex := &Executor{Adapter: printfCmd("DROP\nkept\nBAD\n")}
	if _, err := ex.Run(context.Background(), Request{}, collect(&got)); err != nil {
		t.Fatalf("dropped and malformed lines must not fail the run: %v", err)
	}
	if len(got) != 1 || got[0].ev.Payload["text"] != "kept" {
		t.Errorf("only the translatable line should be emitted, got %v", got)
	}
}

func TestRunReturnsErrorWhenCommandCannotStart(t *testing.T) {
	ex := &Executor{Adapter: &fakeAdapter{cmd: "tomobit-no-such-binary", args: nil}}
	res, err := ex.Run(context.Background(), Request{}, collect(new([]recorded)))
	if err == nil {
		t.Error("a missing executable should return a start error")
	}
	if res.Started {
		t.Error("Started must be false when the process never launched — callers use this to skip anything that assumes a run happened")
	}
}

func TestRunReportsStartedOnASuccessfulLaunch(t *testing.T) {
	ex := &Executor{Adapter: printfCmd("hello\n")}
	res, err := ex.Run(context.Background(), Request{}, collect(new([]recorded)))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Started {
		t.Error("Started should be true once the process launched")
	}
}

func TestRunPropagatesSinkError(t *testing.T) {
	boom := fmt.Errorf("db is down")
	ex := &Executor{Adapter: printfCmd("hello\n")}
	_, err := ex.Run(context.Background(), Request{}, func(Event, int64) error { return boom })
	if err != boom {
		t.Errorf("sink error should propagate: got %v", err)
	}
}

func TestRunEnforcesTimeoutAndKillsChild(t *testing.T) {
	ex := &Executor{Adapter: &fakeAdapter{cmd: "sleep", args: []string{"30"}}}
	start := time.Now()
	_, err := ex.Run(context.Background(), Request{Timeout: 100 * time.Millisecond}, collect(new([]recorded)))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("timeout should surface as an error: got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("child should have been killed promptly, ran %s", elapsed)
	}
}

func TestRunForwardsCancellationWithoutError(t *testing.T) {
	ex := &Executor{Adapter: &fakeAdapter{cmd: "sleep", args: []string{"30"}}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	// A user cancel (SIGINT) is not an executor error; the caller detects it
	// via its own context.
	if _, err := ex.Run(ctx, Request{}, collect(new([]recorded))); err != nil {
		t.Errorf("cancellation should not be reported as an error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("cancelled child should have been killed promptly, ran %s", elapsed)
	}
}

// TestRunKillsChildWhenSinkFailsWithBufferedOutputPending is a regression
// test for a deadlock: the child pipes "yes" through "head -c 5000000",
// producing far more stdout than fits in the OS pipe buffer. The Sink fails
// on the 2nd event, well before that output is drained. Without killing the
// child, its blocked write(2) never returns once nobody reads its stdout, so
// cmd.Wait() hangs forever.
func TestRunKillsChildWhenSinkFailsWithBufferedOutputPending(t *testing.T) {
	ex := &Executor{Adapter: &fakeAdapter{cmd: "sh", args: []string{"-c", "yes | head -c 5000000"}}}
	n := 0
	boom := fmt.Errorf("sink boom")
	sink := func(Event, int64) error {
		n++
		if n == 2 {
			return boom
		}
		return nil
	}

	done := make(chan error, 1)
	go func() {
		_, err := ex.Run(context.Background(), Request{}, sink)
		done <- err
	}()

	select {
	case err := <-done:
		if err != boom {
			t.Errorf("expected the sink error to propagate, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run hung after a Sink failure — the child was left blocked on a full pipe buffer instead of being killed")
	}
}

func TestRunReportsErrorReportedWhenAdapterEmitsProviderError(t *testing.T) {
	var got []recorded
	ex := &Executor{Adapter: printfCmd("ERR\n")}
	res, err := ex.Run(context.Background(), Request{}, collect(&got))
	if err != nil {
		t.Fatal(err)
	}
	if !res.ErrorReported {
		t.Error("ErrorReported should be true once the adapter's stream emits provider.error, so callers avoid recording the same failure twice")
	}
	if len(got) != 1 || got[0].ev.Type != EventProviderError {
		t.Fatalf("the provider.error event should still reach the sink, got %v", got)
	}
}

func TestRunErrorReportedStaysFalseWithoutAnAdapterError(t *testing.T) {
	ex := &Executor{Adapter: printfCmd("hello\n")}
	res, err := ex.Run(context.Background(), Request{}, collect(new([]recorded)))
	if err != nil {
		t.Fatal(err)
	}
	if res.ErrorReported {
		t.Error("ErrorReported should stay false when the adapter never emits provider.error")
	}
}

func TestRunWarnsWhenAdapterEmitsFinishedTwice(t *testing.T) {
	var debug bytes.Buffer
	var got []recorded
	ex := &Executor{Adapter: printfCmd("FIN\nFIN\n"), Debug: &debug}
	if _, err := ex.Run(context.Background(), Request{}, collect(&got)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(debug.String(), "provider.finished") {
		t.Errorf("a second provider.finished should not be silently discarded, got debug output %q", debug.String())
	}
	if len(got) != 1 {
		t.Fatalf("only the last finished should reach the sink, got %v", got)
	}
}

func TestRunWritesTranslateErrorsToWarnEvenWithoutDebugSet(t *testing.T) {
	var warn bytes.Buffer
	ex := &Executor{Adapter: printfCmd("BAD\n"), Warn: &warn}
	if _, err := ex.Run(context.Background(), Request{}, collect(new([]recorded))); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn.String(), "translate error") {
		t.Errorf("a malformed line is a real defect and must warn regardless of Debug, got %q", warn.String())
	}
}

func TestRunNeverPanicsWithNoWarnOrDebugWriters(t *testing.T) {
	ex := &Executor{Adapter: printfCmd("BAD\nDROP\nFIN\nFIN\n")}
	if _, err := ex.Run(context.Background(), Request{}, collect(new([]recorded))); err != nil {
		t.Fatal(err)
	}
}
