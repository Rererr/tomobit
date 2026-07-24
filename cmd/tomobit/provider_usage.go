// Provider-usage summary: "どのエージェントをどのぐらい使ったか" — a real
// ledger measured 41 execution runs, all claude-code, with no way for the
// user to see that. This is the derivation that closes the gap, shared by
// `status --view json`'s providers field and the human status table so the
// two views can never drift (the same discipline statusCandidates already
// holds between showStatus and statusJSON).
package main

import (
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/Rererr/tomobit/internal/core"
)

// providerUsage is one Provider's row. No decay: the connections table right
// below it already carries the decayed judgment, so this counts the raw
// tally — "how much work actually happened", not "how much the harness still
// trusts it".
type providerUsage struct {
	Provider string  `json:"provider"`
	Runs     int     `json:"runs"` // production execution experiences
	FirstTS  int64   `json:"first_ts"`
	LastTS   int64   `json:"last_ts"`
	Success  float64 `json:"success"` // mean OutcomeWeight y over Scored experiences
	Scored   int     `json:"scored"`
}

// providerUsageSummary reduces the current-generation execution experiences
// to one row per Provider, human included (ADR-0018 Decision 2: human is a
// Provider like any other on this ledger).
//
// Provider == "" is skipped: it is the duel parent's no-signal execution row
// (tools/dogfood/README.md — Apply itself never folds it into a connection),
// not a fourth bucket to invent a label for.
//
// Success reuses core.OutcomeWeight rather than a second opinion on what
// "succeeded" means. Only source="production" rows count at all: no organ
// writes a source="learning" execution experience (a duel child runs as
// production — it did real work the user asked for, so it belongs in "how
// much did I use it"; whether it should carry source="learning" instead is
// an open ADR-0026 question), so a learning bucket here could only ever
// show 0.
func providerUsageSummary(exps []*core.Experience) []providerUsage {
	type acc struct {
		runs, scored    int
		successSum      float64
		firstTS, lastTS int64
	}
	accs := make(map[string]*acc)

	for _, e := range exps {
		if e.Kind != core.KindExecution || e.Provider == "" || e.Source != "production" {
			continue
		}
		a, ok := accs[e.Provider]
		if !ok {
			a = &acc{}
			accs[e.Provider] = a
		}
		a.runs++
		if a.firstTS == 0 || e.TS < a.firstTS {
			a.firstTS = e.TS
		}
		if e.TS > a.lastTS {
			a.lastTS = e.TS
		}
		if y, ok := core.OutcomeWeight(e); ok {
			a.successSum += y
			a.scored++
		}
	}

	rows := make([]providerUsage, 0, len(accs))
	for provider, a := range accs {
		var success float64
		if a.scored > 0 {
			success = a.successSum / float64(a.scored)
		}
		rows = append(rows, providerUsage{
			Provider: provider,
			Runs:     a.runs,
			FirstTS:  a.firstTS,
			LastTS:   a.lastTS,
			Success:  success,
			Scored:   a.scored,
		})
	}
	// runs desc, provider name asc — deterministic, since map iteration above is not.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Runs != rows[j].Runs {
			return rows[i].Runs > rows[j].Runs
		}
		return rows[i].Provider < rows[j].Provider
	})
	return rows
}

// relativeUsageTime names how long ago ts was, for the human table's 最終利用
// column. No prior helper in this codebase renders a duration this way
// (voice.Okaeri speaks the fact of an absence, not a "N日前" string), so this
// is the first one.
func relativeUsageTime(now, ts int64) string {
	if ts <= 0 {
		return "-"
	}
	d := time.Duration(now-ts) * time.Millisecond
	switch {
	case d < time.Minute:
		return "たった今"
	case d < time.Hour:
		return fmt.Sprintf("%d分前", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d時間前", int(d/time.Hour))
	default:
		return fmt.Sprintf("%d日前", int(d/(24*time.Hour)))
	}
}

// printProviderUsage renders the summary showStatus prints just above the
// connections table (same tabwriter discipline as printConnections).
func printProviderUsage(w io.Writer, rows []providerUsage, now int64) error {
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PROVIDER\t使った回数\t成功率\t最終利用")
	for _, r := range rows {
		success := "-"
		if r.Scored > 0 {
			success = fmt.Sprintf("%.0f%%", r.Success*100)
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", r.Provider, r.Runs, success, relativeUsageTime(now, r.LastTS))
	}
	return tw.Flush()
}
