// Package facewin is the face window — a second, display-only renderer over
// the same SQLite truth (ADR-0020). It opens the DB read-only, polls the
// events tail, and re-derives stage, mood, and speech as Views; it holds no
// truth and writes nothing.
package facewin

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/Rererr/tomobit/internal/core"
	"github.com/Rererr/tomobit/internal/face"
	"github.com/Rererr/tomobit/internal/store"
	"github.com/Rererr/tomobit/internal/voice"
)

// Snapshot is one derived View of the DB — everything the window draws from.
// Conns and MaxExpTS ride along so the next poll can detect transitions by
// comparison (ADR-0020 Decision 2: ADR-0009のApply前後View比較の窓内版).
type Snapshot struct {
	Stage    int
	Marker   string // "" ふつう / "?" はてな / "z" ねむい (face.Mood)
	Conns    []*core.Connection
	MaxExpTS int64
	Thoughts []Thought // providers currently running (ADR-0026 Decision 5)
}

// Thought is one in-flight provider's latest visible thinking (ADR-0026
// Decision 5): the assistant text it last emitted, tagged with its name. The
// window draws one "考える" bubble per thought — a duel shows two at once,
// making "Tomo is comparing" visible. Still display-only: the text is the same
// provider.output already in the ledger (回答チャネルは端末), shown as a
// thinking fragment, never a spoken answer.
type Thought struct {
	Provider string // provider name; "" until the run reports provider.selected
	Text     string // latest assistant text fragment
}

// Update is one poll's result: the fresh snapshot plus the spoken lines the
// window should show as bubbles, oldest first.
type Update struct {
	Snapshot
	Lines []string
}

// Poller tails the shared SQLite read-only. Zero value + Path is ready; the
// DB may not exist yet (the CLI creates it) — polls before that return the
// empty view and keep retrying, so the mascot can outlive its database.
type Poller struct {
	Path string

	s           *store.Store
	lastEventID int64
	prev        *Snapshot
}

// openRO opens the DB file read-only (ADR-0020 Decision 2: mode=ro — the
// window can never become a second writer). store.Open is not used on
// purpose: it runs migrations, which a read-only renderer must not.
func openRO(path string) (*store.Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &store.Store{DB: db}, nil
}

// Poll derives the current View and any new speech. A missing or unreadable
// DB is not an error: the window shows the empty view (S0, silence) until
// the CLI creates the file.
func (p *Poller) Poll(nowMs int64) (Update, error) {
	if p.s == nil {
		s, err := openRO(p.Path)
		if err != nil {
			return Update{}, nil // not ready yet — keep the fluff ball asleep
		}
		p.s = s
	}

	snap, err := p.snapshot(nowMs)
	if err != nil {
		return Update{}, fmt.Errorf("facewin: snapshot: %w", err)
	}

	u := Update{Snapshot: snap}

	if p.prev == nil {
		// First successful poll: start the event tail at the current head so
		// history is never replayed, and greet only an empty network.
		if p.lastEventID, err = p.s.MaxEventID(); err != nil {
			return Update{}, fmt.Errorf("facewin: event head: %w", err)
		}
		if len(snap.Conns) == 0 {
			u.Lines = append(u.Lines, voice.FirstMeeting())
		}
		p.prev = &snap
		return u, nil
	}

	// Perceive-boundary speech: compare the previous snapshot with this one
	// (growth > insight > miss-reaction > murmur — the same priority the
	// terminal speaks in).
	exps, err := p.expsSince(p.prev.MaxExpTS)
	if err != nil {
		return Update{}, fmt.Errorf("facewin: experiences: %w", err)
	}
	if len(exps) > 0 || snap.Stage != p.prev.Stage {
		ids := make([]string, len(exps))
		for i, e := range exps {
			ids[i] = e.ID
		}
		maxExcess, err := p.s.MaxExcess(ids)
		if err != nil {
			maxExcess = 0
		}
		if text, ok := voice.Perceive(p.prev.Stage, snap.Stage,
			voice.NewSplits(p.prev.Conns, snap.Conns), exps, maxExcess); ok {
			u.Lines = append(u.Lines, text)
		}
	}

	// Event-tail speech: questions and reflections become bubbles; the answer
	// channel stays the terminal (ADR-0020 Decision 2).
	events, err := p.s.EventsSince(p.lastEventID)
	if err != nil {
		return Update{}, fmt.Errorf("facewin: events: %w", err)
	}
	for _, e := range events {
		p.lastEventID = e.ID
		if line, ok := eventLine(e); ok {
			u.Lines = append(u.Lines, line)
		}
	}

	p.prev = &snap
	return u, nil
}

// snapshot derives stage and mood exactly the way the terminal renderer does
// (face.Stage / face.Mood over the same connections) — ADR-0008 Decisions
// 1〜3 apply to the window unchanged.
func (p *Poller) snapshot(nowMs int64) (Snapshot, error) {
	conns, err := p.s.AllConnections()
	if err != nil {
		return Snapshot{}, err
	}
	en := &core.Engine{Repo: p.s}
	states := make([]string, len(conns))
	for i, c := range conns {
		sum, err := en.LedgerSum(c, nowMs)
		if err != nil {
			return Snapshot{}, err
		}
		states[i] = c.State(nowMs, sum)
	}
	_, marker := face.Mood(states)
	maxTS, err := p.maxExperienceTS()
	if err != nil {
		return Snapshot{}, err
	}
	stage, err := face.StageFrom(p.s, nowMs)
	if err != nil {
		return Snapshot{}, err
	}
	thoughts, err := p.activeThoughts()
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Stage:    stage,
		Marker:   marker,
		Conns:    conns,
		MaxExpTS: maxTS,
		Thoughts: thoughts,
	}, nil
}

// activeThoughts derives the providers running right now (ADR-0026 Decision 5):
// task sessions that started but have not finished or cancelled, each folded to
// its latest provider name and assistant text. A session with no assistant text
// yet contributes nothing (there is no thought to show). Ordered by session id
// so two duel siblings keep a stable left/right; the caller caps how many draw.
//
// A duel's parent session is task.started-but-unfinished too, but it carries no
// provider.output of its own (its children do), so it folds to no thought — the
// bubbles are the children's, never the parent's.
func (p *Poller) activeThoughts() ([]Thought, error) {
	rows, err := p.s.DB.Query(`
		SELECT session_id, type, payload FROM events
		WHERE session_id IN (
			SELECT session_id FROM events WHERE type = 'task.started'
			EXCEPT
			SELECT session_id FROM events WHERE type IN ('task.finished','task.cancelled')
		) AND type IN ('provider.selected','provider.output')
		ORDER BY session_id, seq`)
	if err != nil {
		return nil, fmt.Errorf("facewin: active thoughts: %w", err)
	}
	defer rows.Close()

	order := []string{}
	byID := map[string]*Thought{}
	get := func(sid string) *Thought {
		t, ok := byID[sid]
		if !ok {
			t = &Thought{}
			byID[sid] = t
			order = append(order, sid)
		}
		return t
	}
	for rows.Next() {
		var sid, typ, payload string
		if err := rows.Scan(&sid, &typ, &payload); err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			continue // a malformed row must not blank the whole view
		}
		switch typ {
		case "provider.selected":
			if v, ok := m["provider"].(string); ok && v != "" {
				get(sid).Provider = v
			}
		case "provider.output":
			if v, ok := m["text"].(string); ok && v != "" {
				get(sid).Text = v
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []Thought
	for _, sid := range order {
		if t := byID[sid]; t.Text != "" {
			out = append(out, *t)
		}
	}
	return out, nil
}

func (p *Poller) maxExperienceTS() (int64, error) {
	var ts int64
	err := p.s.DB.QueryRow(`SELECT COALESCE(MAX(ts),0) FROM experiences`).Scan(&ts)
	return ts, err
}

// expsSince returns current experiences newer than ts — the batch a
// perceive boundary just landed, for voice.Murmur.
func (p *Poller) expsSince(ts int64) ([]*core.Experience, error) {
	all, err := p.s.CurrentExperiences()
	if err != nil {
		return nil, err
	}
	var out []*core.Experience
	for _, e := range all {
		if e.TS > ts {
			out = append(out, e)
		}
	}
	return out, nil
}

// eventLine re-derives the spoken line for one tail event. Only speech-worthy
// event types produce a bubble; everything else is the CLI's business.
func eventLine(e *store.Event) (string, bool) {
	switch e.Type {
	case "tomo.asked":
		scope := core.NewScope(payloadStrings(e.Payload["scope"])...)
		pair := payloadStrings(e.Payload["pair"])
		if len(pair) != 2 {
			return "", false
		}
		return voice.Asked(scope, pair[0], pair[1]), true
	case "tomo.reflected":
		// ADR-0015: the reflection's line is deterministic from its payload,
		// so re-displaying the recorded text is the same derivation.
		if text, ok := e.Payload["text"].(string); ok && text != "" {
			return text, true
		}
	}
	return "", false
}

// payloadStrings converts a JSON-decoded []any into []string, dropping
// anything that isn't a string.
func payloadStrings(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
