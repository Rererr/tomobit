// expprobe: the ADR-0042 comparison harness. Given a ledger db, reports the
// decide.Choose distribution over 200 fixed seeds for the rust task, the go
// task, a no-tie invariant task (cap=implement only), and an empty-token task
// (uniform prior), plus the capability scope each provider reads for the rust
// task (audit determinism). decide.go is swapped by the driver; recompiled
// per variant.
package main

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/decide"
	"github.com/Rererr/tomobit/internal/store"
)

var providers = []string{"claude-code", "codex", "human"}

func dist(conns []*core.Connection, tokens []string, now int64) map[string]int {
	counts := map[string]int{}
	for i := 0; i < 200; i++ {
		d := decide.Choose(conns, providers, tokens, "", int64(2000+i), now)
		counts[d.Provider]++
	}
	return counts
}

func fmtCounts(c map[string]int) string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for _, k := range keys {
		out += fmt.Sprintf("%s=%d ", k, c[k])
	}
	return out
}

func main() {
	s, err := store.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer s.Close()
	now := time.Now().UnixMilli()
	conns, _ := s.AllConnections()

	rust := []string{"cap=implement", "lang=rust"}
	goT := []string{"cap=implement", "lang=go"}
	capOnly := []string{"cap=implement"} // each provider matches <=1 conn: no tie
	empty := []string{}                  // blank slate: uniform prior

	fmt.Printf("rust  : %s\n", fmtCounts(dist(conns, rust, now)))
	fmt.Printf("go    : %s\n", fmtCounts(dist(conns, goT, now)))
	fmt.Printf("capOnly(no-tie): %s\n", fmtCounts(dist(conns, capOnly, now)))
	fmt.Printf("empty(uniform) : %s\n", fmtCounts(dist(conns, empty, now)))

	// Audit determinism: which capability scope does each provider read for the
	// rust task, and is that audit row stable across all 200 seeds?
	fmt.Println("rust-task capability audit (scope read counts / passed counts):")
	for _, p := range providers {
		scopes := map[string]int{}
		passed := map[bool]int{}
		for i := 0; i < 200; i++ {
			d := decide.Choose(conns, providers, rust, "", int64(2000+i), now)
			for _, c := range d.Candidates {
				if c.Provider == p {
					scopes[c.ScopeKey]++
					passed[c.Passed]++
				}
			}
		}
		fmt.Printf("  %-12s scopes=%v passed=%v\n", p, scopes, passed)
	}
}
