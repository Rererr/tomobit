package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/perceive"
)

// idList collects a repeatable --id flag into a slice (ADR-0033 Decision 2:
// forget --id can name several experiences at once).
type idList []string

func (l *idList) String() string { return strings.Join(*l, ",") }
func (l *idList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// contextKeys is the closed key set amend accepts (ADR-0033 Decision 3): the
// perception's own semantic keys plus the two deterministic ones (cap / model)
// the extractor writes without asking the model. Derived from
// perceive.SemanticKeys, not a hand-copied list, so the human's amend vocabulary
// can never drift from what perception records. A human may re-canonicalize
// values but never invent a key: adding one is a schema change (an
// extractor_ver bump), not a per-amend act.
var contextKeys = buildContextKeys()

func buildContextKeys() map[string]bool {
	keys := map[string]bool{"cap": true, "model": true}
	for _, k := range perceive.SemanticKeys {
		keys[k] = true
	}
	return keys
}

// cmdForget is the destructive verb (ADR-0033 Decision 2): physically delete
// experiences (--id) or a whole session (--session), then rebuild and vacuum in
// the same command so the projections never lag the truth and the freed pages
// carry no residue.
func cmdForget(args []string) error {
	fs := flag.NewFlagSet("forget", flag.ExitOnError)
	db := dbFlag(fs)
	var ids idList
	fs.Var(&ids, "id", "experience id to forget (repeatable)")
	session := fs.String("session", "", "session id to forget entirely (events + experiences)")
	yes := fs.Bool("yes", false, "skip the confirmation prompt (required when non-interactive)")
	fs.Parse(args)

	// --id and --session are exclusive; one is required.
	if (len(ids) == 0) == (*session == "") {
		return fmt.Errorf("forget: give either --id (repeatable) or --session, not both")
	}

	s, err := openStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()

	// A session forget spares its children (ADR-0033 Decision 2): name them so
	// an orphaned subtask is a choice, not a surprise. Tree deletion is a
	// re-run over the listed ids. The notice goes to stderr — stdout is reserved
	// for the Decision 6 one-line summary contract (a GUI parses it).
	if *session != "" {
		children, err := s.ChildSessions(*session)
		if err != nil {
			return err
		}
		if len(children) > 0 {
			fmt.Fprintf(os.Stderr, "子セッションは残る（消すのは指名分のみ）: %s\n", strings.Join(children, ", "))
		}
	}

	proceed, err := forgetConfirmed(bufio.NewReader(os.Stdin), os.Stdout, isTTY(os.Stdin) && isTTY(os.Stdout), *yes, ids, *session)
	if err != nil {
		return err
	}
	if !proceed {
		fmt.Println("aborted")
		return nil
	}

	now := time.Now().UnixMilli()
	var events, exps, named, superseded int
	if *session != "" {
		events, exps, err = s.ForgetSession(*session)
	} else {
		named, superseded, err = s.ForgetExperiences(now, ids)
	}
	if err != nil {
		return err
	}

	// Rebuild before Vacuum: the logical delete and the projection are both
	// committed here, so the summary can be printed as fact. Vacuum runs after
	// and is the only step whose failure leaves work "done but not physically
	// erased" (ADR-0033 Decision 5) — reported apart from the summary.
	en := &core.Engine{Repo: s}
	if err := en.Rebuild(); err != nil {
		return err
	}
	conns, err := s.AllConnections()
	if err != nil {
		return err
	}
	switch {
	case *session != "":
		fmt.Printf("forgot: session %s (%d events, %d experiences; rebuilt: %d connections)\n",
			*session, events, exps, len(conns))
	case superseded > 0:
		// ADR-0034 Decision 3: the sweep of superseded generations (Decision 1)
		// is a named cost, not a hidden one — it shows up in the one-line summary
		// a GUI parses (ADR-0033 Decision 6), same as the session-forget child
		// notice shows up on stderr instead of being silent.
		fmt.Printf("forgot: %d experiences (+%d superseded rows) (rebuilt: %d connections)\n",
			named, superseded, len(conns))
	default:
		fmt.Printf("forgot: %d experiences (rebuilt: %d connections)\n", named, len(conns))
	}

	if err := s.Vacuum(); err != nil {
		return fmt.Errorf("vacuum failed — 台帳からは削除済みだが、ディスク上の痕跡の物理消去は未完: %w", err)
	}
	return nil
}

// forgetConfirmed resolves the irreversible-delete gate (ADR-0033 Decision 2):
// --yes proceeds unasked; without it a terminal is asked y/N (default no), and a
// non-interactive run is refused outright — automation must never delete on a
// stray default. Interactivity is injected so the branch can be exercised
// without a real terminal (the askWithIO / ensureClaudeProfileIO split).
func forgetConfirmed(in *bufio.Reader, out io.Writer, interactive, yes bool, ids idList, session string) (bool, error) {
	if yes {
		return true, nil
	}
	if !interactive {
		return false, fmt.Errorf("forget: 不可逆な操作のため、非対話では --yes が必要")
	}
	return confirmForget(in, out, ids, session), nil
}

// confirmForget reads a y/N answer, defaulting to no (Enter/EOF) — the same
// gate the duel offer uses. Only an explicit y/yes proceeds.
func confirmForget(in *bufio.Reader, out io.Writer, ids idList, session string) bool {
	if session != "" {
		fmt.Fprintf(out, "session %s とその全event・全experienceを物理削除する（取り消せない）。続ける? [y/N] ", session)
	} else {
		fmt.Fprintf(out, "%d件の経験を物理削除する（取り消せない）。続ける? [y/N] ", len(ids))
	}
	line, _ := in.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// cmdAmend is the corrective verb (ADR-0033 Decision 3): not a delete but a
// human re-perception. It appends a new generation of the target's
// (session, kind) sibling set with the target's context/outcome/provider
// replaced, then rebuilds so the projections read the corrected truth.
func cmdAmend(args []string) error {
	fs := flag.NewFlagSet("amend", flag.ExitOnError)
	db := dbFlag(fs)
	id := fs.String("id", "", "experience id to amend (required)")
	ctxJSON := fs.String("context", "", "replacement context as a JSON object (full replace)")
	outcomeJSON := fs.String("outcome", "", "replacement outcome as JSON (full replace)")
	providerName := fs.String("provider", "", "replacement provider (execution experiences only)")
	fs.Parse(args)

	if *id == "" {
		return fmt.Errorf("amend: --id is required")
	}
	var setContext, setOutcome, setProvider bool
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "context":
			setContext = true
		case "outcome":
			setOutcome = true
		case "provider":
			setProvider = true
		}
	})
	if !setContext && !setOutcome && !setProvider {
		return fmt.Errorf("amend: give at least one of --context, --outcome, --provider")
	}

	// Parsed ahead of the store call: context/outcome/provider validity does
	// not depend on which row *id resolves to, so it need not share the
	// atomic read-modify-write transaction below (修正3) — only the
	// --provider/kind mismatch does, since kind is only known once the
	// target row is read.
	var newContext map[string]string
	var newOutcome core.Outcome
	var err error
	if setContext {
		if newContext, err = parseAmendContext(*ctxJSON); err != nil {
			return err
		}
	}
	if setOutcome {
		if newOutcome, err = parseAmendOutcome(*outcomeJSON); err != nil {
			return err
		}
	}
	if setProvider && !validProvider(*providerName) {
		return fmt.Errorf("amend: unknown provider %q (available: %s, human)",
			*providerName, strings.Join(providerNames(), ", "))
	}

	s, err := openStore(*db)
	if err != nil {
		return err
	}
	defer s.Close()

	now := time.Now().UnixMilli()
	newVer, err := s.AmendExperience(*id, now, func(target *core.Experience) error {
		if setProvider {
			// A reflection row also carries a provider (reflection.go: which
			// Connection the insight was about — the mirror's own bookkeeping),
			// but that is not a capability bet target. Rewriting it would change
			// what the reflection was *about*, not correct an executor, so
			// --provider is execution-only; preference rows have no provider at
			// all.
			if target.Kind != core.KindExecution {
				return fmt.Errorf("amend: --provider applies to execution experiences only (this is %s)", target.Kind)
			}
			target.Provider = *providerName
		}
		if setContext {
			target.Context = newContext
		}
		if setOutcome {
			target.Outcome = newOutcome
		}
		return nil
	})
	if err != nil {
		return err
	}

	en := &core.Engine{Repo: s}
	if err := en.Rebuild(); err != nil {
		return err
	}
	conns, err := s.AllConnections()
	if err != nil {
		return err
	}
	fmt.Printf("amended: %s -> ver %d (rebuilt: %d connections)\n", *id, newVer, len(conns))
	return nil
}

// parseAmendContext decodes a JSON object into a canonical context map,
// enforcing the closed key set and CanonValue-nonempty values (ADR-0033
// Decision 3). context is a full replace: to drop a key, omit it from the JSON.
func parseAmendContext(js string) (map[string]string, error) {
	var raw map[string]string
	dec := json.NewDecoder(strings.NewReader(js))
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("amend: --context must be a JSON object of string values: %w", err)
	}
	// Decode reads one value; a second Token that is not EOF means trailing
	// garbage (e.g. `{...},{...}`) the caller likely did not intend to drop.
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("amend: --context must be a single JSON object (trailing data)")
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		key := core.CanonValue(k)
		if !contextKeys[key] {
			return nil, fmt.Errorf("amend: unknown context key %q (allowed: %s)", k, allowedKeys())
		}
		val := core.CanonValue(v)
		if val == "" {
			return nil, fmt.Errorf("amend: empty value for context key %q (omit the key to drop it)", key)
		}
		out[key] = val
	}
	return out, nil
}

// parseAmendOutcome strictly decodes a JSON outcome, rejecting unknown fields so
// a typo'd key is an error rather than a silently dropped correction (ADR-0033
// Decision 3). outcome is a full replace.
func parseAmendOutcome(js string) (core.Outcome, error) {
	var o core.Outcome
	dec := json.NewDecoder(strings.NewReader(js))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return core.Outcome{}, fmt.Errorf("amend: --outcome invalid: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return core.Outcome{}, fmt.Errorf("amend: --outcome must be a single JSON object (trailing data)")
	}
	return o, nil
}

// validProvider accepts a registered adapter name or human (SCHEMA.md R3: no
// free-typed provider names — ADR-0033 Decision 3).
func validProvider(name string) bool {
	if name == "human" {
		return true
	}
	for _, n := range providerNames() {
		if n == name {
			return true
		}
	}
	return false
}

func allowedKeys() string {
	keys := make([]string, 0, len(contextKeys))
	for k := range contextKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
