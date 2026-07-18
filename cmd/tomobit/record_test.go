package main

import (
	"testing"

	"github.com/Rererr/tomobit/internal/executor"
)

// A tool_result is view-only (ADR-0030): after StripViewOnly its payload is
// empty, and recordEvent must not leave an empty provider.output row — that
// row carries no information yet still counts against the perception digest
// budget (ollama.eventsSection serialises every event), the cost Decision 1
// refused.
func TestRecordEventSkipsViewOnlyToolResult(t *testing.T) {
	s := openTestStore(t)
	ev := executor.Event{Type: executor.EventProviderOutput,
		Payload: map[string]any{executor.PayloadToolResult: "\x1b[31mRED\x1b[0m"}}
	if err := recordEvent(s, "sess", ev, 1); err != nil {
		t.Fatal(err)
	}
	evs, err := s.EventsBySession("sess")
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Errorf("a view-only tool_result must leave no ledger row, got %d: %v", len(evs), evs)
	}
}

// A tool_use still carries its name after the strip, so it is recorded — only
// the detail (view-only) is dropped, not the event. This is the asymmetry that
// makes the skip safe: tool_use survives, tool_result does not.
func TestRecordEventKeepsToolUseWithoutDetail(t *testing.T) {
	s := openTestStore(t)
	ev := executor.Event{Type: executor.EventProviderOutput,
		Payload: map[string]any{"tool": "Bash", executor.PayloadDetail: "echo hi"}}
	if err := recordEvent(s, "sess", ev, 1); err != nil {
		t.Fatal(err)
	}
	evs, err := s.EventsBySession("sess")
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("tool_use must be recorded, got %d", len(evs))
	}
	if _, ok := evs[0].Payload[executor.PayloadDetail]; ok {
		t.Errorf("detail must be stripped from the recorded event: %v", evs[0].Payload)
	}
	if evs[0].Payload["tool"] != "Bash" {
		t.Errorf("tool name must survive: %v", evs[0].Payload)
	}
}
