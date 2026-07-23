// Gap probe (ADR-0007/0016/0026): prints curiosity.Gaps for a ledger —
// the duel offer's only trigger — so the offer precondition is measured,
// not assumed, before running `tomobit do` under a PTY.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/Rererr/tomobit/internal/curiosity"
	"github.com/Rererr/tomobit/internal/store"
)

func main() {
	s, err := store.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer s.Close()
	gaps, err := curiosity.Gaps(s, time.Now().UnixMilli())
	if err != nil {
		panic(err)
	}
	if len(gaps) == 0 {
		fmt.Println("no open gaps")
		return
	}
	for _, g := range gaps {
		fmt.Printf("gap scope=%s pair=%s~%s lnBF=%.3f freq=%.2f wobble=%.3f voi=%.3f\n",
			g.ScopeKey, g.A, g.B, g.LnBF, g.Freq, g.Wobble, g.VoI)
	}
}
