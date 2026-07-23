// diag: for the rust task, print every capability connection each provider
// could read at the finest granularity, with the competing tie-break metrics.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/store"
)

func main() {
	s, err := store.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer s.Close()
	now := time.Now().UnixMilli()
	conns, _ := s.AllConnections()
	en := &core.Engine{Repo: s}
	tokens := []string{"cap=implement", "lang=rust"}
	sc := core.NewScope(tokens...)
	for _, prov := range []string{"claude-code", "codex", "human"} {
		fmt.Printf("== %s (rust task) ==\n", prov)
		for _, c := range conns {
			if c.Kind != "capability" || c.Target != prov {
				continue
			}
			if !c.Scope().SubsetOf(tokens) {
				continue
			}
			_ = sc
			ls, _ := en.LedgerSum(c, now)
			fmt.Printf("  scope=%-14s gran=%d quantile=%.3f evidence=%.2f mean=%.3f ledgerSum=%.3f\n",
				c.ScopeKey, len(c.Scope()), c.QuantileAt(now, 0.20), c.Evidence(now), c.Mean(now), ls)
		}
	}
}
