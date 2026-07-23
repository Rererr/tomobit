// Plan-learning probe (ADR-0014): prints the live menu plan.Live derives for
// a capability, each plan connection's derived state (so retirement is
// observable, not inferred), and the full plan.generated history. Read-only.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/plan"
	"github.com/Rererr/tomobit/internal/store"
)

func main() {
	db := os.Args[1]
	capability := "implement"
	if len(os.Args) > 2 {
		capability = os.Args[2]
	}
	s, err := store.Open(db)
	if err != nil {
		panic(err)
	}
	defer s.Close()
	now := time.Now().UnixMilli()

	menu, err := plan.Live(s, capability, now)
	if err != nil {
		panic(err)
	}
	fmt.Println("menu:")
	for _, m := range menu {
		fmt.Printf("  %s\n", m)
	}

	rows, err := s.DB.Query(`SELECT ts, json_extract(payload,'$.plan'),
		json_extract(payload,'$.parent'), json_extract(payload,'$.op')
		FROM events WHERE type='plan.generated' ORDER BY id`)
	if err != nil {
		panic(err)
	}
	fmt.Println("plan.generated history:")
	for rows.Next() {
		var ts int64
		var name, parent, op string
		if err := rows.Scan(&ts, &name, &parent, &op); err != nil {
			panic(err)
		}
		fmt.Printf("  %s (op=%s parent=%s, %.1f days ago)\n",
			name, op, parent, float64(now-ts)/86400000)
	}
	rows.Close()

	conns, err := s.AllConnections()
	if err != nil {
		panic(err)
	}
	en := &core.Engine{Repo: s}
	fmt.Println("plan connections:")
	for _, c := range conns {
		if c.Kind != core.ConnPlan {
			continue
		}
		sum, err := en.LedgerSum(c, now)
		if err != nil {
			panic(err)
		}
		fmt.Printf("  %-28s %-32s a=%.3f b=%.3f mean=%.3f ev=%.2f state=%s\n",
			c.ScopeKey, c.Target, c.Alpha, c.Beta, c.Mean(now), c.Evidence(now),
			c.State(now, sum))
	}
}
