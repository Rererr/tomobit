// tomobit — the minimal core loop: record → perceive → connect → rebuild.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/perceive"
	"github.com/Rererr/tomobit/internal/store"
)

const extractorVer = 1 // bump when the extraction prompt/schema changes

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "record":
		return cmdRecord(rest)
	case "perceive":
		return cmdPerceive(rest)
	case "rebuild":
		return cmdRebuild(rest)
	case "status":
		return cmdStatus(rest)
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func usage() {
	fmt.Println(`tomobit — a living harness that grows with you

usage:
  tomobit record  --session <id> --type <event.type> [--json '{...}']
  tomobit perceive [--model qwen3:8b] [--url http://localhost:11434]
  tomobit rebuild
  tomobit status

common flags:
  --db <path>   database file (default ~/.tomobit/tomobit.db, or $TOMOBIT_DB)`)
}

func dbFlag(fs *flag.FlagSet) *string {
	def := os.Getenv("TOMOBIT_DB")
	if def == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			def = filepath.Join(home, ".tomobit", "tomobit.db")
		} else {
			def = "tomobit.db"
		}
	}
	return fs.String("db", def, "database file")
}

func openStore(path string) (*store.Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return store.Open(path)
}

func cmdRecord(args []string) error {
	fs := flag.NewFlagSet("record", flag.ExitOnError)
	db := dbFlag(fs)
	session := fs.String("session", "", "session id (required)")
	typ := fs.String("type", "", "event type, e.g. task.started (required)")
	payload := fs.String("json", "{}", "event payload as JSON")
	fs.Parse(args)
	if *session == "" || *typ == "" {
		return fmt.Errorf("record: --session and --type are required")
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(*payload), &p); err != nil {
		return fmt.Errorf("record: --json is not valid JSON: %w", err)
	}
	s, err := openStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.AppendEvent(*session, *typ, time.Now().UnixMilli(), p)
}

func cmdPerceive(args []string) error {
	fs := flag.NewFlagSet("perceive", flag.ExitOnError)
	db := dbFlag(fs)
	model := fs.String("model", "qwen3:8b", "ollama model for context extraction")
	url := fs.String("url", "", "ollama base url (default http://localhost:11434)")
	fs.Parse(args)

	s, err := openStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()

	p := &perceive.Perceiver{
		Store:     s,
		Extractor: &perceive.Ollama{URL: *url, Model: *model},
		Ver:       extractorVer,
	}
	exps, err := p.Run()
	if len(exps) > 0 {
		// Apply in the same (ts, id) order the store replays on rebuild, so
		// the live projection matches the canonical rebuilt one.
		sort.Slice(exps, func(i, j int) bool {
			if exps[i].TS != exps[j].TS {
				return exps[i].TS < exps[j].TS
			}
			return exps[i].ID < exps[j].ID
		})
		en := &core.Engine{Repo: s}
		for _, e := range exps {
			if applyErr := en.Apply(e); applyErr != nil {
				return fmt.Errorf("apply %s: %w (experiences are saved; the projection is stale — run `tomobit rebuild` to repair)", e.ID, applyErr)
			}
			fmt.Printf("perceived %s: %s %s → %s\n",
				e.SessionID, e.Kind, core.NewScope(e.Tokens()...).Key(), e.Target())
		}
	}
	if err != nil {
		return err
	}
	if len(exps) == 0 {
		fmt.Println("nothing pending")
	}
	return nil
}

func cmdRebuild(args []string) error {
	fs := flag.NewFlagSet("rebuild", flag.ExitOnError)
	db := dbFlag(fs)
	fs.Parse(args)
	s, err := openStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()
	en := &core.Engine{Repo: s}
	if err := en.Rebuild(); err != nil {
		return err
	}
	conns, err := s.AllConnections()
	if err != nil {
		return err
	}
	fmt.Printf("rebuilt: %d connections\n", len(conns))
	return nil
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	db := dbFlag(fs)
	fs.Parse(args)
	s, err := openStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()

	conns, err := s.AllConnections()
	if err != nil {
		return err
	}
	if len(conns) == 0 {
		fmt.Println("no connections yet — record a session and run `tomobit perceive`")
		return nil
	}
	en := &core.Engine{Repo: s}
	now := time.Now().UnixMilli()
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "KIND\tSCOPE\tTARGET\tSTRENGTH\tCONF\tEVIDENCE\tSTATE")
	for _, c := range conns {
		sum, err := en.LedgerSum(c, now)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%.2f\t%.2f\t%.1f\t%s\n",
			c.Kind, c.ScopeKey, c.Target,
			c.Mean(now), c.Confidence(now), c.Evidence(now),
			c.State(now, sum))
	}
	return w.Flush()
}
