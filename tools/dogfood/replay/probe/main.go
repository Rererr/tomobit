// Probe: for a rust task, what does the Decision Engine actually read, and
// what would the split judgment have said had it ever been summoned?
//
//  1. finestMatch via decide.Choose over tokens [cap=implement lang=rust]
//  2. LedgerSum (decayed excess surprisal) of cap=implement/claude-code —
//     the split trigger statistic
//  3. counterfactual ln BF of "lang=rust partitions" within scope
//     cap=implement, target claude-code (core.LnBF over decayed counts)
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/decide"
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

	// 1. decide over a rust task, many seeds
	tokens := []string{"cap=implement", "lang=rust"}
	counts := map[string]int{}
	var sampleScope string
	for i := 0; i < 200; i++ {
		d := decide.Choose(conns, []string{"claude-code", "codex", "human"}, tokens, "", int64(2000+i), now)
		counts[d.Provider]++
		for _, c := range d.Candidates {
			if c.Provider == "claude-code" {
				sampleScope = fmt.Sprintf("scope=%q quantile=%.3f passed=%v", c.ScopeKey, c.Quantile, c.Passed)
			}
		}
	}
	fmt.Println("rust-task choices over 200 seeds:", counts)
	fmt.Println("claude-code candidate audit:", sampleScope)

	// 2. split trigger statistic on the parent
	en := &core.Engine{Repo: s}
	for _, c := range conns {
		if c.Kind == "capability" && c.Target == "claude-code" {
			sum, err := en.LedgerSum(c, now)
			if err != nil {
				panic(err)
			}
			fmt.Printf("LedgerSum(%s/%s) = %.3f (ThetaTrigger=%.1f)\n", c.ScopeKey, c.Target, sum, core.ThetaTrigger)
		}
	}

	// 3. counterfactual token BF: does lang=rust partition cap=implement/claude-code?
	exps, _ := s.CurrentExperiences()
	scope := core.NewScope("cap=implement")
	token := core.NewScope("lang=rust")
	var kW, nW, kO, nO float64
	for _, e := range exps {
		if e.ConnKind() != "capability" || e.Target() != "claude-code" {
			continue
		}
		if !scope.SubsetOf(e.Tokens()) {
			continue
		}
		y, ok := core.OutcomeWeight(e)
		if !ok {
			continue
		}
		w := core.DecayFactor(e.TS, now)
		if token.SubsetOf(e.Tokens()) {
			kW += w * y
			nW += w
		} else {
			kO += w * y
			nO += w
		}
	}
	bf := core.LnBF(kW, nW, kO, nO)
	fmt.Printf("counterfactual lnBF(lang=rust | cap=implement/claude-code) = %.2f (ThetaSplit=%.1f; with=%.1f/%.1f without=%.1f/%.1f)\n",
		bf, core.ThetaSplit, kW, nW, kO, nO)
}
