package main

import (
	"testing"

	"github.com/Rererr/tomobit/internal/core"
)

func execExp(provider, source string, ts int64, outcome core.Outcome) *core.Experience {
	return &core.Experience{
		Kind: core.KindExecution, Provider: provider, Source: source, TS: ts, Outcome: outcome,
	}
}

func TestProviderUsageSummaryExcludesNoSignalDuelParentRow(t *testing.T) {
	exps := []*core.Experience{
		execExp("", "production", 1000, core.Outcome{Cancelled: true}), // duel parent (tools/dogfood/README.md)
		execExp("claude-code", "production", 1000, core.Outcome{Verdict: "up"}),
	}
	rows := providerUsageSummary(exps)
	if len(rows) != 1 || rows[0].Provider != "claude-code" {
		t.Fatalf("rows = %+v, want exactly one row for claude-code (provider=\"\" excluded)", rows)
	}
}

func TestProviderUsageSummaryCountsHumanAsAProvider(t *testing.T) {
	exps := []*core.Experience{
		execExp("human", "production", 1000, core.Outcome{Verdict: "up"}),
	}
	rows := providerUsageSummary(exps)
	if len(rows) != 1 || rows[0].Provider != "human" || rows[0].Runs != 1 {
		t.Fatalf("rows = %+v, want one production run for human", rows)
	}
}

func TestProviderUsageSummaryIgnoresNonExecutionKinds(t *testing.T) {
	exps := []*core.Experience{
		{Kind: core.KindPreference, Provider: "claude-code", Source: "production", TS: 1000},
		{Kind: core.KindReflection, Provider: "claude-code", Source: "production", TS: 1000},
	}
	if rows := providerUsageSummary(exps); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none — only execution experiences count as usage", rows)
	}
}

// No organ writes a source="learning" execution experience today (duel
// children run as production), but should one ever appear it must not leak
// into a view whose every number claims to describe production usage.
func TestProviderUsageSummaryIgnoresNonProductionExecutionExperiences(t *testing.T) {
	exps := []*core.Experience{
		execExp("codex", "production", 1000, core.Outcome{Verdict: "up"}),
		execExp("codex", "learning", 2000, core.Outcome{Verdict: "up"}),
		execExp("codex", "learning", 3000, core.Outcome{Verdict: "down"}),
	}
	rows := providerUsageSummary(exps)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one codex row", rows)
	}
	if rows[0].Runs != 1 {
		t.Errorf("runs = %d, want 1 (a learning experiment is not production usage)", rows[0].Runs)
	}
	if rows[0].LastTS != 1000 {
		t.Errorf("last_ts = %d, want 1000 — learning rows must not touch any column of the row", rows[0].LastTS)
	}
}

func TestProviderUsageSummaryScoredCountsOnlyOutcomesOutcomeWeightCanResolve(t *testing.T) {
	exps := []*core.Experience{
		execExp("claude-code", "production", 1000, core.Outcome{Verdict: "up"}),   // y=1, scored
		execExp("claude-code", "production", 2000, core.Outcome{Cancelled: true}), // OutcomeWeight ok=false — not scored
	}
	rows := providerUsageSummary(exps)
	if rows[0].Scored != 1 {
		t.Fatalf("scored = %d, want 1 (a cancelled run carries no usable outcome)", rows[0].Scored)
	}
	if rows[0].Success != 1 {
		t.Errorf("success = %v, want 1 (the cancelled run must not drag the average down)", rows[0].Success)
	}
}

func TestProviderUsageSummarySuccessIsTheMeanOutcomeWeightOverProductionOnly(t *testing.T) {
	exps := []*core.Experience{
		execExp("claude-code", "production", 1000, core.Outcome{Adopted: "as-is"}),      // y=1.0
		execExp("claude-code", "production", 2000, core.Outcome{Adopted: "with-edits"}), // y=0.7
		execExp("claude-code", "learning", 3000, core.Outcome{Verdict: "down"}),         // y=0, but learning — excluded
	}
	rows := providerUsageSummary(exps)
	want := (1.0 + 0.7) / 2
	if rows[0].Scored != 2 {
		t.Fatalf("scored = %d, want 2 (learning experiences must not enter the production success average)", rows[0].Scored)
	}
	if got := rows[0].Success; got != want {
		t.Errorf("success = %v, want %v", got, want)
	}
}

func TestProviderUsageSummaryTracksFirstAndLastProductionTimestamp(t *testing.T) {
	exps := []*core.Experience{
		execExp("codex", "production", 500, core.Outcome{Verdict: "up"}),
		execExp("codex", "production", 3000, core.Outcome{Verdict: "up"}),
		execExp("codex", "production", 1500, core.Outcome{Verdict: "up"}),
	}
	rows := providerUsageSummary(exps)
	if rows[0].FirstTS != 500 {
		t.Errorf("first_ts = %d, want 500 (earliest production run)", rows[0].FirstTS)
	}
	if rows[0].LastTS != 3000 {
		t.Errorf("last_ts = %d, want 3000 (latest production run)", rows[0].LastTS)
	}
}

func TestProviderUsageSummaryOrdersByRunsDescendingThenProviderNameAscending(t *testing.T) {
	exps := []*core.Experience{
		execExp("codex", "production", 1000, core.Outcome{Verdict: "up"}),
		execExp("claude-code", "production", 1000, core.Outcome{Verdict: "up"}),
		execExp("claude-code", "production", 2000, core.Outcome{Verdict: "up"}),
		execExp("human", "production", 1000, core.Outcome{Verdict: "up"}),
		execExp("human", "production", 2000, core.Outcome{Verdict: "up"}),
	}
	rows := providerUsageSummary(exps)
	var order []string
	for _, r := range rows {
		order = append(order, r.Provider)
	}
	want := []string{"claude-code", "human", "codex"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order = %v, want %v (runs desc, ties broken by provider name asc)", order, want)
		}
	}
}
