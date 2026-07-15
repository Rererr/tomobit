// Package perceive turns Sessions (event streams) into Experiences.
// Deterministic facts are parsed from events directly; only semantic
// context attributes go to the LLM (ADR-0004: 決定的にパースできるものは
// LLMに聞かない). Perception is deferred and replayable (ADR-0004/0005).
package perceive

import (
	"fmt"
	"strings"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/store"
)

// LLM-extracted context keys (SCHEMA.md R2). cap/model/provider are
// deterministic and never asked of the model.
var SemanticKeys = []string{"lang", "framework", "topic", "size"}

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
		vocab := map[string][]string{}
		for _, k := range SemanticKeys {
			vals, err := p.Store.KnownValues(k)
			if err != nil {
				return nil, err
			}
			vocab[k] = vals
		}
		semantic, err = p.Extractor.ExtractContext(events, vocab)
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
		Context:        ctx, Provider: det.provider,
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
		case "task.cancelled":
			d.outcome.Cancelled = true
		}
	}
	return d
}
