package executor

import (
	"reflect"
	"testing"
)

func TestStripViewOnlyRemovesDetailForTheLedger(t *testing.T) {
	in := map[string]any{"tool": "Edit", PayloadDetail: "/etc/hosts"}
	got := StripViewOnly(in)
	if _, ok := got[PayloadDetail]; ok {
		t.Errorf("detail must not reach the ledger: %v", got)
	}
	if got["tool"] != "Edit" {
		t.Errorf("non-view keys must survive: %v", got)
	}
}

// A tool's output is display-only too (ADR-0030): it must never reach the
// ledger, or the perception digest would carry what R3 deliberately drops.
func TestStripViewOnlyRemovesToolResultForTheLedger(t *testing.T) {
	in := map[string]any{"tool": "Bash", PayloadToolResult: "\x1b[31mRED\x1b[0m"}
	got := StripViewOnly(in)
	if _, ok := got[PayloadToolResult]; ok {
		t.Errorf("tool output must not reach the ledger: %v", got)
	}
	if got["tool"] != "Bash" {
		t.Errorf("non-view keys must survive: %v", got)
	}
}

// TestStripViewOnlyDoesNotMutateInput guards the chat's show-then-record order:
// the event is displayed with its detail before the sink strips it, so the
// argument map must still carry the detail after the call.
func TestStripViewOnlyDoesNotMutateInput(t *testing.T) {
	in := map[string]any{"tool": "Edit", PayloadDetail: "/etc/hosts"}
	StripViewOnly(in)
	if in[PayloadDetail] != "/etc/hosts" {
		t.Errorf("input map was mutated: detail lost from %v", in)
	}
}

// TestStripViewOnlyReturnsSameMapWhenNothingToStrip pins the allocation-saving
// path: an event with no view-only key is returned as-is, not copied.
func TestStripViewOnlyReturnsSameMapWhenNothingToStrip(t *testing.T) {
	in := map[string]any{"tool": "Edit"}
	got := StripViewOnly(in)
	if reflect.ValueOf(got).Pointer() != reflect.ValueOf(in).Pointer() {
		t.Errorf("payload without a view-only key should not be copied")
	}
}

func TestStripViewOnlyHandlesNil(t *testing.T) {
	if got := StripViewOnly(nil); got != nil {
		t.Errorf("nil payload should stay nil, got %v", got)
	}
}
