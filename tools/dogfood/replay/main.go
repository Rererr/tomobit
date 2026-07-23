// Replay check (ADR-0012 Decision 5): same ledger + same seed must reproduce
// the same decision. Reads the most recent tomo.decided events whose ledger
// state is still current (no weighted experience applied since), re-runs
// decide.Choose with the recorded seed/tokens/size at the recorded ts, and
// compares provider + candidate audit.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/Rererr/tomobit/internal/decide"
	"github.com/Rererr/tomobit/internal/store"
)

func main() {
	db, n := os.Args[1], 1
	if len(os.Args) > 2 {
		n, _ = strconv.Atoi(os.Args[2])
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
	rows, err := s.DB.Query(`SELECT ts, payload FROM events WHERE type='tomo.decided' ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	bad := 0
	for rows.Next() {
		var ts int64
		var payload string
		if err := rows.Scan(&ts, &payload); err != nil {
			panic(err)
		}
		var p struct {
			Provider   string   `json:"provider"`
			Seed       string   `json:"seed"`
			Size       string   `json:"size"`
			Tokens     []string `json:"tokens"`
			Candidates []struct {
				Provider string  `json:"provider"`
				Quantile float64 `json:"quantile"`
				Wins     int     `json:"wins"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			panic(err)
		}
		seed, _ := strconv.ParseInt(p.Seed, 10, 64)
		dec := decide.Choose(conns, []string{"claude-code", "codex", "human"}, p.Tokens, p.Size, seed, ts)
		match := dec.Provider == p.Provider
		fmt.Printf("recorded=%s replayed=%s match=%v\n", p.Provider, dec.Provider, match)
		for i, c := range dec.Candidates {
			r := p.Candidates[i]
			if c.Provider != r.Provider || c.Quantile != r.Quantile || c.Wins != r.Wins {
				fmt.Printf("  cand mismatch: recorded=%+v replayed={%s %g %d}\n", r, c.Provider, c.Quantile, c.Wins)
				bad++
			}
		}
		if !match {
			bad++
		}
	}
	if bad > 0 {
		fmt.Println("REPLAY MISMATCH:", bad)
		os.Exit(1)
	}
	fmt.Println("replay OK")
}
