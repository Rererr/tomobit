// Samples decide.Choose over a ledger's current projection with many seeds —
// the statistical view of "which provider would auto pick right now".
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/Rererr/tomobit/internal/decide"
	"github.com/Rererr/tomobit/internal/store"
)

func main() {
	db := os.Args[1]
	m := 200
	if len(os.Args) > 2 {
		m, _ = strconv.Atoi(os.Args[2])
	}
	s, err := store.Open(db)
	if err != nil {
		panic(err)
	}
	defer s.Close()
	conns, err := s.AllConnections()
	if err != nil {
		panic(err)
	}
	now := time.Now().UnixMilli()
	tokens := []string{"cap=implement", "lang=go"}
	counts := map[string]int{}
	gated := map[string]int{}
	for i := 0; i < m; i++ {
		d := decide.Choose(conns, []string{"claude-code", "codex", "human"}, tokens, "", int64(1000+i), now)
		counts[d.Provider]++
		for _, c := range d.Candidates {
			if !c.Passed {
				gated[c.Provider]++
			}
		}
	}
	fmt.Printf("choices over %d seeds: %v  (gated-out counts: %v)\n", m, counts, gated)
}
