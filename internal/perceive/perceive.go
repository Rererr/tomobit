// Package perceive turns Sessions (event streams) into Experiences.
// Deterministic facts are parsed from events directly; only semantic
// context attributes go to the LLM (ADR-0004: 決定的にパースできるものは
// LLMに聞かない). Perception is deferred and replayable (ADR-0004/0005).
package perceive

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/store"
)

// LLM-extracted context keys (SCHEMA.md R2). cap/model/provider are
// deterministic and never asked of the model.
var SemanticKeys = []string{"lang", "framework", "topic", "size"}

// vocabLimit bounds how many known values each semantic key contributes to
// the extraction prompt (SCHEMA.md D5). Unlike the rest of the prompt —
// eventsSection (ollama.go) already bounds the session digest to
// maxSessionChars=12000 — the vocabulary had no cap, so it grows with the
// ledger forever.
//
// 20 is measured, not guessed: against the dogfood ledger
// (/Users/example/.tomobit/tomobit.db, 2026-07-21, 28 experiences), known values
// average 6-10 characters, so a saturated key's line stays under ~200
// characters at this limit; four such keys are still a small, fixed
// fraction of the 12000-character session digest budget they share the
// prompt with, however large the ledger grows. The same ledger's most
// diverse key (topic, free text) had only 11 distinct values, so the cap
// changes nothing yet — it only starts trimming once growth actually
// reaches it, which is the failure mode being closed here.
const vocabLimit = 20

// capVocab picks, per semantic key, the vocabLimit values most worth
// showing the extraction prompt. SCHEMA.md D5 hands vocabulary to the
// prompt so the model converges spelling variants ("axum"/"Axum"/
// "axum-web") onto one form instead of inventing a new one each time.
//
// A value is worth protecting once it has recurred: a single occurrence
// cannot yet have drifted into variants, so the value most in need of
// convergence is the one reused across the most distinct sessions —
// frequency is therefore the primary ranking signal. Recency breaks ties
// in favor of the spelling actually in current use, so a value dormant for
// a long time does not permanently hold a slot over one the user is
// actively naming today. The value string itself is the final tiebreaker,
// making the ranking independent of Go's randomized map order (ADR-0011:
// 判断は数学 — the derivation must be deterministic).
//
// Frequency counts distinct SESSIONS, not experience rows: a session's
// preference experiences (ADR-0003) copy the execution experience's
// Context verbatim (perceiveSession below), so counting rows would let one
// session with several preferences outrank several sessions that each used
// a value once — the opposite of "recurred across real occurrences".
func capVocab(exps []*core.Experience, limit int) map[string][]string {
	type stat struct {
		sessions map[string]bool
		lastTS   int64
	}
	byKey := make(map[string]map[string]*stat, len(SemanticKeys))
	for _, k := range SemanticKeys {
		byKey[k] = map[string]*stat{}
	}
	for _, e := range exps {
		for _, k := range SemanticKeys {
			v := e.Context[k]
			if v == "" {
				continue
			}
			st, ok := byKey[k][v]
			if !ok {
				st = &stat{sessions: map[string]bool{}}
				byKey[k][v] = st
			}
			st.sessions[e.SessionID] = true
			if e.TS > st.lastTS {
				st.lastTS = e.TS
			}
		}
	}

	out := make(map[string][]string, len(SemanticKeys))
	for _, k := range SemanticKeys {
		type ranked struct {
			value  string
			count  int
			lastTS int64
		}
		entries := make([]ranked, 0, len(byKey[k]))
		for v, st := range byKey[k] {
			entries = append(entries, ranked{v, len(st.sessions), st.lastTS})
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].count != entries[j].count {
				return entries[i].count > entries[j].count
			}
			if entries[i].lastTS != entries[j].lastTS {
				return entries[i].lastTS > entries[j].lastTS
			}
			return entries[i].value < entries[j].value
		})
		if len(entries) > limit {
			entries = entries[:limit]
		}
		values := make([]string, len(entries))
		for i, e := range entries {
			values[i] = e.value
		}
		sort.Strings(values) // prompt order stays alphabetical, matching Store.KnownValues before this change
		out[k] = values
	}
	return out
}

// Extractor extracts semantic context attributes from a session's events.
// vocab maps each semantic key to values already known to Tomobit,
// handed to the prompt to prevent vocabulary drift (SCHEMA.md D5).
type Extractor interface {
	ExtractContext(events []*store.Event, vocab map[string][]string) (map[string]string, error)
	Name() string // extractor_model, e.g. "qwen3:8b"
}

// Perceiver runs deferred perception over pending sessions.
type Perceiver struct {
	Store     *store.Store
	Extractor Extractor
	Ver       int // extractor_ver: bump when prompt/schema changes
}

// Run perceives every pending session and returns the new experiences.
// A failing session aborts the run; already-perceived sessions are
// untouched, the rest stay pending (deferred perception — nothing lost).
func (p *Perceiver) Run() ([]*core.Experience, error) {
	sessions, err := p.Store.PendingSessions(p.Ver)
	if err != nil {
		return nil, err
	}
	var out []*core.Experience
	for _, sid := range sessions {
		exps, err := p.perceiveSession(sid)
		if err != nil {
			return out, fmt.Errorf("session %s: %w", sid, err)
		}
		out = append(out, exps...)
	}
	return out, nil
}

func (p *Perceiver) perceiveSession(sessionID string) ([]*core.Experience, error) {
	events, err := p.Store.EventsBySession(sessionID)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, nil
	}

	det := parseDeterministic(events)

	semantic := map[string]string{}
	if p.Extractor != nil {
		var known []*core.Experience
		known, err = p.Store.CurrentExperiences()
		if err != nil {
			return nil, err
		}
		semantic, err = p.Extractor.ExtractContext(events, capVocab(known, vocabLimit))
		if err != nil {
			return nil, err
		}
	}

	ctx := map[string]string{}
	for _, k := range SemanticKeys {
		if v := strings.TrimSpace(strings.ToLower(semantic[k])); v != "" {
			ctx[k] = v
		}
	}
	// qwen3:8b echoes the language into framework despite the prompt rule
	// (measured: "go worker pool" → framework=go survived two prompt
	// iterations). A language is never a framework, and the equality is
	// decidable — so decide it here, not in the prompt (ADR-0004).
	if ctx["framework"] == ctx["lang"] {
		delete(ctx, "framework")
	}
	if det.capability != "" {
		ctx["cap"] = det.capability
	}
	if det.model != "" {
		ctx["model"] = det.model
	}

	ts := events[len(events)-1].TS
	out := []*core.Experience{{
		ID: store.NewID(ts), SessionID: sessionID, TS: ts,
		Kind: core.KindExecution, ExtractorVer: p.Ver,
		ExtractorModel: p.extractorName(),
		Context:        ctx, Provider: det.provider, Plan: det.plan,
		Outcome: det.outcome, Source: det.source,
	}}

	// A user.preference answer in the session becomes its own experience
	// (ADR-0003: 回答 = Learning Reality → Experience).
	for i, pref := range det.preferences {
		ctxCopy := map[string]string{}
		for k, v := range ctx {
			ctxCopy[k] = v
		}
		out = append(out, &core.Experience{
			ID: store.NewID(ts + int64(i) + 1), SessionID: sessionID, TS: ts,
			Kind: core.KindPreference, ExtractorVer: p.Ver,
			ExtractorModel: p.extractorName(),
			Context:        ctxCopy,
			Outcome:        pref, Source: "learning",
		})
	}

	// One transaction for the whole session: a partial insert would strand
	// the preference experiences forever (see Store.InsertExperiences).
	if err := p.Store.InsertExperiences(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *Perceiver) extractorName() string {
	if p.Extractor == nil {
		return "none"
	}
	return p.Extractor.Name()
}

type deterministic struct {
	capability  string
	provider    string
	model       string
	plan        string
	outcome     core.Outcome
	source      string
	preferences []core.Outcome
}

func parseDeterministic(events []*store.Event) deterministic {
	d := deterministic{source: "production"}
	str := func(m map[string]any, k string) string {
		if v, ok := m[k].(string); ok {
			return core.CanonValue(v)
		}
		return ""
	}
	for _, e := range events {
		switch e.Type {
		case "task.started":
			if str(e.Payload, "source") == "learning" {
				d.source = "learning"
			}
		case "capability.started":
			d.capability = str(e.Payload, "capability")
		case "provider.selected":
			d.provider = str(e.Payload, "provider")
			d.model = str(e.Payload, "model")
		case "plan.selected":
			// The plan is a machine attribute the harness itself recorded
			// (ADR-0014) — deterministic, never asked of the model.
			d.plan = str(e.Payload, "plan")
		case "test.result":
			if passed, ok := e.Payload["passed"].(bool); ok {
				p := passed
				d.outcome.TestsPassed = &p
			}
		case "user.verdict":
			d.outcome.Verdict = str(e.Payload, "verdict")
		case "user.preference":
			d.preferences = append(d.preferences, core.Outcome{
				Preferred: str(e.Payload, "preferred"),
				Over:      str(e.Payload, "over"),
			})
		case "task.finished":
			d.outcome.Adopted = str(e.Payload, "adopted")
			if reverted, ok := e.Payload["reverted"].(bool); ok {
				d.outcome.Reverted = reverted
			}
		case "provider.error":
			// The objective failure signal (exit≠0 / executor error, recorded
			// via providerErrorPayload). It is a split subtask's and a duel
			// child's only outcome — their task.finished is empty (ADR-0028
			// Decision 5) — so without this branch a failed child would be
			// indistinguishable from a successful one.
			d.outcome.Failed = true
		case "task.cancelled":
			d.outcome.Cancelled = true
		}
	}
	return d
}
