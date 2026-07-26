// Package store implements SCHEMA.md v1.0 on SQLite: append-only truth
// (events, experiences), rebuildable projections (connections,
// surprise_ledger), and persistent working state (curiosity_queue).
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/Rererr/tomobit/internal/core"
)

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;

CREATE TABLE IF NOT EXISTS events (
  id         INTEGER PRIMARY KEY,
  session_id TEXT    NOT NULL,
  seq        INTEGER NOT NULL,
  ts         INTEGER NOT NULL,
  v          INTEGER NOT NULL DEFAULT 1,
  type       TEXT    NOT NULL,
  payload    TEXT    NOT NULL CHECK (json_valid(payload)),
  UNIQUE (session_id, seq)
);
CREATE INDEX IF NOT EXISTS events_by_type_ts ON events (type, ts);
CREATE TRIGGER IF NOT EXISTS events_no_update BEFORE UPDATE ON events
  BEGIN SELECT RAISE(ABORT, 'events is append-only'); END;
CREATE TRIGGER IF NOT EXISTS events_no_delete BEFORE DELETE ON events
  BEGIN SELECT RAISE(ABORT, 'events is append-only'); END;

CREATE TABLE IF NOT EXISTS experiences (
  id              TEXT    PRIMARY KEY,
  session_id      TEXT    NOT NULL,
  ts              INTEGER NOT NULL,
  kind            TEXT    NOT NULL CHECK (kind IN ('execution','preference','reflection')),
  extractor_ver   INTEGER NOT NULL,
  extractor_model TEXT    NOT NULL,
  context         TEXT    NOT NULL CHECK (json_valid(context)),
  provider        TEXT,
  outcome         TEXT    NOT NULL CHECK (json_valid(outcome)),
  source          TEXT    NOT NULL DEFAULT 'production'
                  CHECK (source IN ('production','learning'))
);
CREATE INDEX IF NOT EXISTS experiences_by_session ON experiences (session_id);
CREATE TRIGGER IF NOT EXISTS experiences_no_update BEFORE UPDATE ON experiences
  BEGIN SELECT RAISE(ABORT, 'experiences is append-only'); END;
CREATE TRIGGER IF NOT EXISTS experiences_no_delete BEFORE DELETE ON experiences
  BEGIN SELECT RAISE(ABORT, 'experiences is append-only'); END;

CREATE VIEW IF NOT EXISTS experiences_current AS
  SELECT * FROM experiences e
  WHERE e.extractor_ver = (
    SELECT max(extractor_ver) FROM experiences
    WHERE session_id = e.session_id AND kind = e.kind
  );

CREATE TABLE IF NOT EXISTS connections (
  kind        TEXT    NOT NULL CHECK (kind IN ('capability','preference','plan')),
  scope_key   TEXT    NOT NULL,
  target      TEXT    NOT NULL,
  alpha       REAL    NOT NULL,
  beta        REAL    NOT NULL,
  last_update INTEGER NOT NULL,
  born_ts     INTEGER NOT NULL,
  parent_key  TEXT,
  prior_alpha REAL    NOT NULL DEFAULT 1,
  prior_beta  REAL    NOT NULL DEFAULT 1,
  PRIMARY KEY (kind, scope_key, target)
);

CREATE TABLE IF NOT EXISTS surprise_ledger (
  kind          TEXT    NOT NULL,
  scope_key     TEXT    NOT NULL,
  target        TEXT    NOT NULL,
  experience_id TEXT    NOT NULL,
  ts            INTEGER NOT NULL,
  p             REAL    NOT NULL,
  y             REAL    NOT NULL,
  s_excess      REAL    NOT NULL,
  PRIMARY KEY (kind, scope_key, target, experience_id)
);

CREATE TABLE IF NOT EXISTS curiosity_queue (
  id          TEXT    PRIMARY KEY,
  created_ts  INTEGER NOT NULL,
  signal      TEXT    NOT NULL,
  payload     TEXT    NOT NULL CHECK (json_valid(payload)),
  priority    REAL    NOT NULL,
  status      TEXT    NOT NULL DEFAULT 'pending'
              CHECK (status IN ('pending','done','dismissed','expired')),
  resolved_ts INTEGER
);
`

// The append-only DELETE guards held as named DDL, byte-identical to the
// copies in schema above (a drift-guard test pins them). The forget organ
// (ADR-0033 Decision 5) DROPs a guard, deletes, and recreates it inside one
// transaction — SCHEMA.md D3's "意図的な保守作業ではその時だけDROP" — so a
// mid-flight failure rolls the trigger back with the rows.
const (
	eventsNoDeleteTrigger = `CREATE TRIGGER IF NOT EXISTS events_no_delete BEFORE DELETE ON events
  BEGIN SELECT RAISE(ABORT, 'events is append-only'); END;`
	experiencesNoDeleteTrigger = `CREATE TRIGGER IF NOT EXISTS experiences_no_delete BEFORE DELETE ON experiences
  BEGIN SELECT RAISE(ABORT, 'experiences is append-only'); END;`
)

type Store struct {
	DB *sql.DB
}

func Open(path string) (*Store, error) {
	// _txlock=immediate (a modernc.org/sqlite DSN param) makes every
	// sql.Tx this Store opens start with BEGIN IMMEDIATE instead of SQLite's
	// default deferred BEGIN. ForgetExperiences / ForgetSession /
	// AmendExperience all SELECT inside the same transaction they later
	// write in; a deferred BEGIN only takes the write lock at that later
	// write, so a concurrent writer (another process — a second CLI
	// invocation, or the GUI's subprocess call, ADR-0033 Decision 6) that
	// commits in between invalidates the read snapshot and the write fails
	// with SQLITE_BUSY_SNAPSHOT — a condition busy_timeout's lock-wait retry
	// does not cover, since there is no lock to wait for.
	db, err := sql.Open("sqlite", path+"?_txlock=immediate")
	if err != nil {
		return nil, err
	}
	// modernc.org/sqlite serializes writes; a single connection avoids
	// SQLITE_BUSY between concurrent statements in one process.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	// Widen pre-ADR-0013 databases in place: connections is a projection, so
	// the DEFAULT 1 backfill is at worst stale until the next rebuild.
	for _, col := range []string{"prior_alpha", "prior_beta"} {
		if err := ensureColumn(db, "connections", col, "REAL NOT NULL DEFAULT 1"); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate %s: %w", col, err)
		}
	}
	if err := ensureReflectionKind(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate experiences kind: %w", err)
	}
	if err := ensureColumn(db, "experiences", "plan", "TEXT"); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate experiences plan: %w", err)
	}
	if err := ensurePlanKind(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate connections kind: %w", err)
	}
	// SQLite expands a view's SELECT * at creation time, so a view created
	// before a column widening would hide the new column forever. Recreate.
	if _, err := db.Exec(`DROP VIEW IF EXISTS experiences_current`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate view: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate view: %w", err)
	}
	return &Store{DB: db}, nil
}

// ensurePlanKind rebuilds a pre-ADR-0014 connections table whose CHECK
// rejects kind='plan'. connections is a projection, so this could just drop
// and rebuild — but copying keeps the live view warm until the next rebuild.
func ensurePlanKind(db *sql.DB) error {
	var ddl string
	err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type='table' AND name='connections'`).Scan(&ddl)
	if err != nil {
		return err
	}
	if strings.Contains(ddl, "'plan'") {
		return nil
	}
	_, err = db.Exec(`
		BEGIN IMMEDIATE;
		CREATE TABLE connections_new (
		  kind        TEXT    NOT NULL CHECK (kind IN ('capability','preference','plan')),
		  scope_key   TEXT    NOT NULL,
		  target      TEXT    NOT NULL,
		  alpha       REAL    NOT NULL,
		  beta        REAL    NOT NULL,
		  last_update INTEGER NOT NULL,
		  born_ts     INTEGER NOT NULL,
		  parent_key  TEXT,
		  prior_alpha REAL    NOT NULL DEFAULT 1,
		  prior_beta  REAL    NOT NULL DEFAULT 1,
		  PRIMARY KEY (kind, scope_key, target)
		);
		INSERT INTO connections_new SELECT * FROM connections;
		DROP TABLE connections;
		ALTER TABLE connections_new RENAME TO connections;
		COMMIT;`)
	return err
}

// ensureReflectionKind rebuilds a pre-ADR-0015 experiences table whose CHECK
// still rejects kind='reflection'. SQLite cannot alter a CHECK in place, so
// this is the copy-rename dance — run inside one transaction, with the
// append-only triggers and the experiences_current view recreated after.
// Truth rows are copied verbatim; nothing is reinterpreted.
func ensureReflectionKind(db *sql.DB) error {
	var ddl string
	err := db.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type='table' AND name='experiences'`).Scan(&ddl)
	if err != nil {
		return err
	}
	if strings.Contains(ddl, "'reflection'") {
		return nil
	}
	_, err = db.Exec(`
		BEGIN IMMEDIATE;
		DROP VIEW IF EXISTS experiences_current;
		CREATE TABLE experiences_new (
		  id              TEXT    PRIMARY KEY,
		  session_id      TEXT    NOT NULL,
		  ts              INTEGER NOT NULL,
		  kind            TEXT    NOT NULL CHECK (kind IN ('execution','preference','reflection')),
		  extractor_ver   INTEGER NOT NULL,
		  extractor_model TEXT    NOT NULL,
		  context         TEXT    NOT NULL CHECK (json_valid(context)),
		  provider        TEXT,
		  outcome         TEXT    NOT NULL CHECK (json_valid(outcome)),
		  source          TEXT    NOT NULL DEFAULT 'production'
		                  CHECK (source IN ('production','learning'))
		);
		INSERT INTO experiences_new SELECT * FROM experiences;
		DROP TABLE experiences;
		ALTER TABLE experiences_new RENAME TO experiences;
		COMMIT;`)
	if err != nil {
		return err
	}
	// The index, triggers, and view come back via the idempotent schema.
	_, err = db.Exec(schema)
	return err
}

// ensureColumn adds a column when the table predates it — the whole
// migration story for rebuildable projections.
func ensureColumn(db *sql.DB, table, col, decl string) error {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`,
		table, col).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err = db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, col, decl))
	return err
}

func (s *Store) Close() error { return s.DB.Close() }

// NewID returns a sortable unique id: hex ms timestamp + random suffix.
func NewID(tsMs int64) string {
	var r [4]byte
	rand.Read(r[:])
	return fmt.Sprintf("%013x-%x", tsMs, r)
}

// ---- truth: events ----

type Event struct {
	ID        int64
	SessionID string
	Seq       int64
	TS        int64
	V         int
	Type      string
	Payload   map[string]any
}

// AppendEvent assigns the next seq within the session atomically.
func (s *Store) AppendEvent(sessionID, typ string, tsMs int64, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`
		INSERT INTO events (session_id, seq, ts, type, payload)
		VALUES (?, (SELECT COALESCE(MAX(seq),0)+1 FROM events WHERE session_id = ?), ?, ?, ?)`,
		sessionID, sessionID, tsMs, typ, string(b))
	return err
}

// LastEventTS returns the timestamp of the most recent event of the given
// type. Derived from the append-only log, not a counter: the question budget
// is a View over events (ADR-0007 Decision 3).
func (s *Store) LastEventTS(eventType string) (tsMs int64, found bool, err error) {
	err = s.DB.QueryRow(`
		SELECT ts FROM events WHERE type = ? ORDER BY ts DESC LIMIT 1`,
		eventType).Scan(&tsMs)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return tsMs, true, nil
}

// RecentProviderCosts returns the cost_usd values of the most recent
// provider.finished events that carry one, newest first, up to limit. Only
// claude-code reports cost_usd (codex does not — implementation confirmed), so
// this is the sample the parallel gate's cost estimate draws its median from
// (ADR-0028 Decision 3). An empty result means no real cost has ever been
// measured: the gate says so honestly rather than inventing a number.
func (s *Store) RecentProviderCosts(limit int) ([]float64, error) {
	rows, err := s.DB.Query(`
		SELECT json_extract(payload, '$.cost_usd') AS cost
		FROM events
		WHERE type = 'provider.finished'
		  AND json_extract(payload, '$.cost_usd') IS NOT NULL
		ORDER BY ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []float64
	for rows.Next() {
		var cost float64
		if err := rows.Scan(&cost); err != nil {
			return nil, err
		}
		out = append(out, cost)
	}
	return out, rows.Err()
}

// LatestEventTS returns the newest event timestamp of any type (0 when
// empty) — the absence detector's anchor (ADR-0019 Decision 2:
// 不在はeventsの空白から知覚できる).
func (s *Store) LatestEventTS() (int64, error) {
	var ts int64
	err := s.DB.QueryRow(`SELECT COALESCE(MAX(ts),0) FROM events`).Scan(&ts)
	return ts, err
}

// MaxEventID returns the id of the newest event, 0 when empty — where the
// face window starts its tail poll so a fresh window never replays history
// (ADR-0020 Decision 2).
func (s *Store) MaxEventID() (int64, error) {
	var id int64
	err := s.DB.QueryRow(`SELECT COALESCE(MAX(id),0) FROM events`).Scan(&id)
	return id, err
}

// EventsSince returns events with id > after, oldest first — the face
// window's polling read (ADR-0020 Decision 2: events末尾の定期ポーリング).
func (s *Store) EventsSince(after int64) ([]*Event, error) {
	rows, err := s.DB.Query(`
		SELECT id, session_id, seq, ts, v, type, payload
		FROM events WHERE id > ? ORDER BY id`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func (s *Store) EventsBySession(sessionID string) ([]*Event, error) {
	rows, err := s.DB.Query(`
		SELECT id, session_id, seq, ts, v, type, payload
		FROM events WHERE session_id = ? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func scanEvents(rows *sql.Rows) ([]*Event, error) {
	var out []*Event
	for rows.Next() {
		e := &Event{}
		var payload string
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Seq, &e.TS, &e.V, &e.Type, &payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &e.Payload); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ChildSessions returns the session ids of tasks opened as subtasks of
// parentSID — their task.started names it as parent (ADR-0023 Decision 2) — in
// creation order (the events autoincrement id, which is the flat proposal order
// subtasks open in). The chat's split fold-back reads them to gather each
// subtask's output for the parent thread (ADR-0028 Decision 5); the flat order
// lets it tell a subtask that ran from one left 未着手 by a fail-stop.
func (s *Store) ChildSessions(parentSID string) ([]string, error) {
	rows, err := s.DB.Query(`
		SELECT session_id FROM events
		WHERE type = 'task.started' AND json_extract(payload, '$.parent') = ?
		ORDER BY id`, parentSID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// PendingSessions returns finished sessions that have no experience at
// extractor_ver >= ver — the Deferred Perception queue, derived by query
// (SCHEMA.md: no table needed). Sessions the owner has forgotten or amended
// are excluded permanently (ADR-0033 Decision 4): 人間の知覚は最終知覚であり、
// extractor 改版で機械が忘れた記憶を復活させ人の訂正を上書きしてはならない.
func (s *Store) PendingSessions(ver int) ([]string, error) {
	// ORDER BY id references a column outside the DISTINCT projection —
	// rejected by strict SQL, accepted by SQLite (fixed backend, ADR-0004).
	//
	// The last clause is ADR-0054 Decision 2: a session opened under a parent is
	// that parent task's breakdown, not a task of its own, so it produces no
	// experience — one task, one experience. Its events stay in the ledger
	// untouched (One Ledger: only the projection changes).
	//
	// The exception is written as an opt-in rather than "exclude split
	// children": a duel's two sides ARE independent commissions — the whole
	// point is comparing them (ADR-0026) — so they name themselves out by the
	// task.duel their parent recorded. Any future parent/child relationship
	// therefore defaults to "breakdown", which is the side the Decision falls
	// on; treating one as independent has to be an explicit choice.
	rows, err := s.DB.Query(`
		SELECT DISTINCT session_id FROM events e
		WHERE type IN ('task.finished','task.cancelled')
		  AND session_id NOT IN (
		    SELECT session_id FROM experiences WHERE extractor_ver >= ?
		  )
		  AND session_id NOT IN (
		    SELECT session_id FROM events WHERE type IN ('user.forgot','user.amended')
		  )
		  AND session_id NOT IN (
		    SELECT c.session_id FROM events c
		    WHERE c.type = 'task.started'
		      AND json_extract(c.payload, '$.parent') IS NOT NULL
		      AND NOT EXISTS (
		        SELECT 1 FROM events p
		        WHERE p.session_id = json_extract(c.payload, '$.parent')
		          AND p.type = 'task.duel'
		      )
		  )
		ORDER BY id`, ver)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---- truth: experiences ----

func (s *Store) InsertExperience(e *core.Experience) error {
	provider := sql.NullString{String: e.Provider, Valid: e.Provider != ""}
	plan := sql.NullString{String: e.Plan, Valid: e.Plan != ""}
	_, err := s.DB.Exec(`
		INSERT INTO experiences
		  (id, session_id, ts, kind, extractor_ver, extractor_model,
		   context, provider, outcome, source, plan)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.SessionID, e.TS, e.Kind, e.ExtractorVer, e.ExtractorModel,
		core.MarshalContext(e.Context), provider,
		core.MarshalOutcome(e.Outcome), e.Source, plan)
	return err
}

// InsertExperiences inserts a session's experiences atomically: any single
// failure rolls the whole batch back. A partial commit would drop the
// session out of PendingSessions (which is satisfied by even one experience
// at the version), stranding the uninserted preference experiences forever
// — truth is append-only, so no rebuild could recover them.
func (s *Store) InsertExperiences(exps []*core.Experience) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
		INSERT INTO experiences
		  (id, session_id, ts, kind, extractor_ver, extractor_model,
		   context, provider, outcome, source, plan)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()
	for i, e := range exps {
		provider := sql.NullString{String: e.Provider, Valid: e.Provider != ""}
		plan := sql.NullString{String: e.Plan, Valid: e.Plan != ""}
		if _, err := stmt.Exec(
			e.ID, e.SessionID, e.TS, e.Kind, e.ExtractorVer, e.ExtractorModel,
			core.MarshalContext(e.Context), provider,
			core.MarshalOutcome(e.Outcome), e.Source, plan); err != nil {
			tx.Rollback()
			return fmt.Errorf("insert experience %d/%d (id=%s): %w", i+1, len(exps), e.ID, err)
		}
	}
	return tx.Commit()
}

func (s *Store) CurrentExperiences() ([]*core.Experience, error) {
	rows, err := s.DB.Query(`
		SELECT id, session_id, ts, kind, extractor_ver, extractor_model,
		       context, provider, outcome, source, plan
		FROM experiences_current ORDER BY ts, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExperiences(rows)
}

// CurrentExperiencesBySessionKind returns the current-generation rows of one
// (session, kind) — the sibling set amend copies forward together, since
// experiences_current picks the max extractor_ver per (session, kind) and
// bumping only the target would drop the siblings from the view (ADR-0033
// Decision 3).
func (s *Store) CurrentExperiencesBySessionKind(sessionID, kind string) ([]*core.Experience, error) {
	rows, err := s.DB.Query(`
		SELECT id, session_id, ts, kind, extractor_ver, extractor_model,
		       context, provider, outcome, source, plan
		FROM experiences_current WHERE session_id = ? AND kind = ? ORDER BY ts, id`,
		sessionID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExperiences(rows)
}

// currentExperiencesTx is CurrentExperiencesBySessionKind's read, scoped to
// an already-open transaction. AmendExperience's sibling read must land on
// the same connection its write later commits on (修正3: read-modify-write
// atomicity) — and Store pools a single connection (SetMaxOpenConns(1)), so
// a second s.DB query issued while a transaction holds that connection would
// block forever rather than see the transaction's own uncommitted state.
func currentExperiencesTx(tx *sql.Tx, sessionID, kind string) ([]*core.Experience, error) {
	rows, err := tx.Query(`
		SELECT id, session_id, ts, kind, extractor_ver, extractor_model,
		       context, provider, outcome, source, plan
		FROM experiences_current WHERE session_id = ? AND kind = ? ORDER BY ts, id`,
		sessionID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExperiences(rows)
}

func scanExperiences(rows *sql.Rows) ([]*core.Experience, error) {
	var out []*core.Experience
	for rows.Next() {
		e := &core.Experience{}
		var ctx, outcome string
		var provider, plan sql.NullString
		if err := rows.Scan(&e.ID, &e.SessionID, &e.TS, &e.Kind,
			&e.ExtractorVer, &e.ExtractorModel, &ctx, &provider, &outcome, &e.Source, &plan); err != nil {
			return nil, err
		}
		e.Provider = provider.String
		e.Plan = plan.String
		if err := json.Unmarshal([]byte(ctx), &e.Context); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(outcome), &e.Outcome); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// KnownValues returns the distinct values already recorded for a context
// key — the vocabulary handed to the extraction prompt (SCHEMA.md D5).
// Reads experiences_current, not experiences: vocabulary from superseded
// extractor versions would otherwise be re-suggested to the model forever,
// resurrecting exactly the misextractions a version bump was meant to fix.
func (s *Store) KnownValues(key string) ([]string, error) {
	// WHERE/ORDER BY reference the SELECT alias v — a SQLite extension over
	// strict SQL, safe on the fixed SQLite backend (ADR-0004).
	rows, err := s.DB.Query(`
		SELECT DISTINCT json_extract(context, '$.' || ?) AS v
		FROM experiences_current WHERE v IS NOT NULL AND v != '' ORDER BY v`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// MaxExcess returns the largest excess surprisal recorded for any of the
// given experience ids — the batch's sharpest miss, which grades Tomo's
// reaction line (ADR-0019 Decision 1: 驚き＝ADR-0002の導出値の翻訳).
func (s *Store) MaxExcess(expIDs []string) (float64, error) {
	if len(expIDs) == 0 {
		return 0, nil
	}
	args := make([]any, len(expIDs))
	marks := make([]string, len(expIDs))
	for i, id := range expIDs {
		args[i] = id
		marks[i] = "?"
	}
	var max float64
	err := s.DB.QueryRow(`
		SELECT COALESCE(MAX(s_excess), 0) FROM surprise_ledger
		WHERE experience_id IN (`+strings.Join(marks, ",")+`)`, args...).Scan(&max)
	return max, err
}

// ---- projections ----

func (s *Store) GetConnection(kind, scopeKey, target string) (*core.Connection, error) {
	row := s.DB.QueryRow(`
		SELECT kind, scope_key, target, alpha, beta, last_update, born_ts,
		       COALESCE(parent_key,''), prior_alpha, prior_beta
		FROM connections WHERE kind=? AND scope_key=? AND target=?`,
		kind, scopeKey, target)
	c := &core.Connection{}
	err := row.Scan(&c.Kind, &c.ScopeKey, &c.Target, &c.Alpha, &c.Beta,
		&c.LastUpdate, &c.BornTS, &c.ParentKey, &c.PriorA, &c.PriorB)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Store) UpsertConnection(c *core.Connection) error {
	parent := sql.NullString{String: c.ParentKey, Valid: c.ParentKey != ""}
	pa, pb := c.Prior()
	// The prior is set at birth and immutable afterwards (ADR-0013): the
	// conflict branch deliberately leaves prior_alpha/prior_beta untouched.
	_, err := s.DB.Exec(`
		INSERT INTO connections
		  (kind, scope_key, target, alpha, beta, last_update, born_ts, parent_key,
		   prior_alpha, prior_beta)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (kind, scope_key, target) DO UPDATE SET
		  alpha=excluded.alpha, beta=excluded.beta,
		  last_update=excluded.last_update`,
		c.Kind, c.ScopeKey, c.Target, c.Alpha, c.Beta, c.LastUpdate, c.BornTS, parent,
		pa, pb)
	return err
}

func (s *Store) DeleteConnection(kind, scopeKey, target string) error {
	_, err := s.DB.Exec(`DELETE FROM connections WHERE kind=? AND scope_key=? AND target=?`,
		kind, scopeKey, target)
	return err
}

func (s *Store) ConnectionsFor(kind, target string) ([]*core.Connection, error) {
	return s.queryConnections(`
		SELECT kind, scope_key, target, alpha, beta, last_update, born_ts,
		       COALESCE(parent_key,''), prior_alpha, prior_beta
		FROM connections WHERE kind=? AND target=? ORDER BY scope_key`, kind, target)
}

func (s *Store) AllConnections() ([]*core.Connection, error) {
	return s.queryConnections(`
		SELECT kind, scope_key, target, alpha, beta, last_update, born_ts,
		       COALESCE(parent_key,''), prior_alpha, prior_beta
		FROM connections ORDER BY kind, scope_key, target`)
}

func (s *Store) queryConnections(q string, args ...any) ([]*core.Connection, error) {
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*core.Connection
	for rows.Next() {
		c := &core.Connection{}
		if err := rows.Scan(&c.Kind, &c.ScopeKey, &c.Target, &c.Alpha, &c.Beta,
			&c.LastUpdate, &c.BornTS, &c.ParentKey, &c.PriorA, &c.PriorB); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) InsertLedger(e *core.LedgerEntry) error {
	_, err := s.DB.Exec(`
		INSERT OR REPLACE INTO surprise_ledger
		  (kind, scope_key, target, experience_id, ts, p, y, s_excess)
		VALUES (?,?,?,?,?,?,?,?)`,
		e.Kind, e.ScopeKey, e.Target, e.ExperienceID, e.TS, e.P, e.Y, e.SExcess)
	return err
}

func (s *Store) LedgerFor(kind, scopeKey, target string) ([]*core.LedgerEntry, error) {
	rows, err := s.DB.Query(`
		SELECT kind, scope_key, target, experience_id, ts, p, y, s_excess
		FROM surprise_ledger WHERE kind=? AND scope_key=? AND target=?
		ORDER BY ts`, kind, scopeKey, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*core.LedgerEntry
	for rows.Next() {
		e := &core.LedgerEntry{}
		if err := rows.Scan(&e.Kind, &e.ScopeKey, &e.Target, &e.ExperienceID,
			&e.TS, &e.P, &e.Y, &e.SExcess); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) DeleteLedgerFor(kind, scopeKey, target string) error {
	_, err := s.DB.Exec(`
		DELETE FROM surprise_ledger WHERE kind=? AND scope_key=? AND target=?`,
		kind, scopeKey, target)
	return err
}

// ClearProjections wipes everything rebuildable (SCHEMA.md D10).
func (s *Store) ClearProjections() error {
	if _, err := s.DB.Exec(`DELETE FROM connections`); err != nil {
		return err
	}
	_, err := s.DB.Exec(`DELETE FROM surprise_ledger`)
	return err
}

// ---- forgetting: the organ of forgetting (ADR-0033) ----

// dedupStrings returns in's distinct elements in first-seen order.
func dedupStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// inClause builds a "?,?,..." placeholder list and its args for an IN filter.
func inClause(ids []string) (marks string, args []any) {
	m := make([]string, len(ids))
	args = make([]any, len(ids))
	for i, id := range ids {
		m[i] = "?"
		args[i] = id
	}
	return strings.Join(m, ","), args
}

// ForgetExperiences physically deletes the named experiences and, per
// ADR-0034 Decision 1, every row in the same (session_id, kind) whose
// extractor_ver is lower than the named row's. Deleting only the named row
// would let experiences_current's max(extractor_ver) selection for that
// group fall through to an older generation — resurrecting a perception the
// owner had already superseded (by amend or by a re-perception) the moment
// they forgot its replacement, a world where forgetting could bring a
// machine perception back over a human one. Sweeping every lower generation
// closes that off structurally rather than by chasing individual re-surfacing
// cases.
//
// Only current-generation ids are accepted (ADR-0034 Decision 2 — the same
// discipline ADR-0033 Decision 3 already puts on amend): naming a superseded
// id would "forget" a row whose content still lives on in the current
// generation's copy-forward, which would make the deletion a lie.
//
// It runs in one transaction: every id must exist and be current-generation
// first — a typo or a superseded id that silently no-ops would forge a
// "忘れたつもり", the worst failure mode (ADR-0033 Decision 5) — then the
// append-only guard is dropped, the rows deleted, and the guard recreated,
// so a crash rolls the trigger back with the rows. The user.forgot marker
// records only the ids given, never the swept-along superseded ones
// (ADR-0034 Consequences: the marker is what the owner said to forget, not
// a deletion log). Returns the count of named rows deleted and the count of
// superseded rows swept along with them.
func (s *Store) ForgetExperiences(tsMs int64, ids []string) (named, superseded int, err error) {
	if len(ids) == 0 {
		return 0, 0, fmt.Errorf("forget: no experience ids given")
	}
	// Deduplicate up front so `--id e1 --id e1` neither double-lists e1 in the
	// marker payload nor inflates the reported count above the rows deleted.
	ids = dedupStrings(ids)
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	type location struct {
		sessionID string
		kind      string
		ver       int
	}
	marks, args := inClause(ids)
	rows, err := tx.Query(`SELECT id, session_id, kind, extractor_ver FROM experiences WHERE id IN (`+marks+`)`, args...)
	if err != nil {
		return 0, 0, err
	}
	found := map[string]location{}
	for rows.Next() {
		var id string
		var loc location
		if err := rows.Scan(&id, &loc.sessionID, &loc.kind, &loc.ver); err != nil {
			rows.Close()
			return 0, 0, err
		}
		found[id] = loc
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	rows.Close()

	// Input order, so both error reports and the marker's id list are
	// deterministic regardless of the SELECT's row order.
	var missing []string
	for _, id := range ids {
		if _, ok := found[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return 0, 0, fmt.Errorf("forget: no such experience: %s", strings.Join(missing, ", "))
	}

	curRows, err := tx.Query(`SELECT id FROM experiences_current WHERE id IN (`+marks+`)`, args...)
	if err != nil {
		return 0, 0, err
	}
	isCurrent := map[string]bool{}
	for curRows.Next() {
		var id string
		if err := curRows.Scan(&id); err != nil {
			curRows.Close()
			return 0, 0, err
		}
		isCurrent[id] = true
	}
	if err := curRows.Err(); err != nil {
		curRows.Close()
		return 0, 0, err
	}
	curRows.Close()

	var supersededIDs []string
	for _, id := range ids {
		if !isCurrent[id] {
			supersededIDs = append(supersededIDs, id)
		}
	}
	if len(supersededIDs) > 0 {
		return 0, 0, fmt.Errorf(
			"forget: %s is superseded — past perceptions cannot be forgotten (ADR-0034 Decision 2)",
			strings.Join(supersededIDs, ", "))
	}

	// Every named id is current, so its own extractor_ver is already the
	// (session, kind) group's max — sweeping strictly-lower versions can
	// never reach a current sibling that was not named.
	type group struct{ sessionID, kind string }
	sweepBelow := map[group]int{}
	for _, id := range ids {
		loc := found[id]
		sweepBelow[group{loc.sessionID, loc.kind}] = loc.ver
	}
	where := []string{"id IN (" + marks + ")"}
	delArgs := append([]any{}, args...)
	for g, ver := range sweepBelow {
		where = append(where, "(session_id = ? AND kind = ? AND extractor_ver < ?)")
		delArgs = append(delArgs, g.sessionID, g.kind, ver)
	}

	forgotBySession := map[string][]string{}
	var sessions []string
	for _, id := range ids {
		sid := found[id].sessionID
		if _, seen := forgotBySession[sid]; !seen {
			sessions = append(sessions, sid)
		}
		forgotBySession[sid] = append(forgotBySession[sid], id)
	}

	if _, err := tx.Exec(`DROP TRIGGER experiences_no_delete`); err != nil {
		return 0, 0, err
	}
	res, err := tx.Exec(`DELETE FROM experiences WHERE `+strings.Join(where, " OR "), delArgs...)
	if err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(experiencesNoDeleteTrigger); err != nil {
		return 0, 0, err
	}

	sort.Strings(sessions)
	for _, sid := range sessions {
		payload, err := json.Marshal(map[string]any{"ids": forgotBySession[sid]})
		if err != nil {
			return 0, 0, err
		}
		if _, err := tx.Exec(`
			INSERT INTO events (session_id, seq, ts, type, payload)
			VALUES (?, (SELECT COALESCE(MAX(seq),0)+1 FROM events WHERE session_id = ?), ?, 'user.forgot', ?)`,
			sid, sid, tsMs, string(payload)); err != nil {
			return 0, 0, err
		}
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return len(ids), int(n) - len(ids), nil
}

// ForgetSession physically deletes a whole session — events and experiences
// both — the only way to erase raw-log content (ADR-0033 Decision 2). No marker
// is written: the session vanishes from the events the queue derives from, so
// there is nothing left to exclude (Decision 4). Both append-only guards drop
// and come back inside the one transaction. Returns the counts deleted.
func (s *Store) ForgetSession(sid string) (events, exps int, err error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	if err = tx.QueryRow(`SELECT count(*) FROM events WHERE session_id = ?`, sid).Scan(&events); err != nil {
		return 0, 0, err
	}
	if err = tx.QueryRow(`SELECT count(*) FROM experiences WHERE session_id = ?`, sid).Scan(&exps); err != nil {
		return 0, 0, err
	}
	if events == 0 && exps == 0 {
		return 0, 0, fmt.Errorf("forget: unknown session %q", sid)
	}

	if _, err = tx.Exec(`DROP TRIGGER events_no_delete`); err != nil {
		return 0, 0, err
	}
	if _, err = tx.Exec(`DROP TRIGGER experiences_no_delete`); err != nil {
		return 0, 0, err
	}
	if _, err = tx.Exec(`DELETE FROM events WHERE session_id = ?`, sid); err != nil {
		return 0, 0, err
	}
	if _, err = tx.Exec(`DELETE FROM experiences WHERE session_id = ?`, sid); err != nil {
		return 0, 0, err
	}
	if _, err = tx.Exec(eventsNoDeleteTrigger); err != nil {
		return 0, 0, err
	}
	if _, err = tx.Exec(experiencesNoDeleteTrigger); err != nil {
		return 0, 0, err
	}

	if err = tx.Commit(); err != nil {
		return 0, 0, err
	}
	return events, exps, nil
}

// AmendExperience is the corrective verb's whole read-modify-write, made
// atomic (修正3): the target's (session, kind) lookup, the current-generation
// read, the superseded check, the newVer computation, the new-generation
// write, and the user.amended marker all happen inside one transaction.
// Split across separate autocommit reads with only the final write in a
// transaction — the pre-fix shape — two concurrent amends of the same
// (session, kind) could both read the same max(extractor_ver), both compute
// the same newVer, and both commit a "current" generation at it; rebuild
// would then double-count whichever generation's evidence survived the write
// race, since experiences_current's max-ver selection cannot tell the two
// apart.
//
// apply receives the target row — already loaded with its current-generation
// content — to edit in place; AmendExperience marks it extractor_model
// "human" afterwards regardless of which fields apply touched, since being
// amended at all is what makes a row a human re-perception, not which fields
// moved. An apply error aborts the transaction with nothing written.
//
// Untouched siblings ride the new generation forward under their own
// extractor_model — the origin does not lie (ADR-0033 Decision 3) — because
// experiences_current selects the max extractor_ver per (session, kind):
// bumping only the target would drop the siblings from the view entirely.
// Returns the new generation's extractor_ver.
func (s *Store) AmendExperience(id string, tsMs int64, apply func(target *core.Experience) error) (newVer int, err error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var sessionID, kind string
	err = tx.QueryRow(`SELECT session_id, kind FROM experiences WHERE id = ?`, id).Scan(&sessionID, &kind)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("amend: no such experience %q", id)
	}
	if err != nil {
		return 0, err
	}

	current, err := currentExperiencesTx(tx, sessionID, kind)
	if err != nil {
		return 0, err
	}
	var target *core.Experience
	for _, e := range current {
		if e.ID == id {
			target = e
			break
		}
	}
	if target == nil {
		return 0, fmt.Errorf("amend: %s is superseded — past perceptions cannot be amended (ADR-0033 Decision 3)", id)
	}
	if err := apply(target); err != nil {
		return 0, err
	}
	target.ExtractorModel = "human"

	for _, e := range current {
		if e.ExtractorVer > newVer {
			newVer = e.ExtractorVer
		}
	}
	newVer++

	stmt, err := tx.Prepare(`
		INSERT INTO experiences
		  (id, session_id, ts, kind, extractor_ver, extractor_model,
		   context, provider, outcome, source, plan)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	for i, e := range current {
		provider := sql.NullString{String: e.Provider, Valid: e.Provider != ""}
		plan := sql.NullString{String: e.Plan, Valid: e.Plan != ""}
		if _, err := stmt.Exec(
			NewID(tsMs), e.SessionID, e.TS, e.Kind, newVer, e.ExtractorModel,
			core.MarshalContext(e.Context), provider,
			core.MarshalOutcome(e.Outcome), e.Source, plan); err != nil {
			return 0, fmt.Errorf("insert amended experience %d/%d (id=%s): %w", i+1, len(current), e.ID, err)
		}
	}

	payload, err := json.Marshal(map[string]any{"id": id, "ver": newVer})
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`
		INSERT INTO events (session_id, seq, ts, type, payload)
		VALUES (?, (SELECT COALESCE(MAX(seq),0)+1 FROM events WHERE session_id = ?), ?, 'user.amended', ?)`,
		sessionID, sessionID, tsMs, string(payload)); err != nil {
		return 0, err
	}

	return newVer, tx.Commit()
}

// CarryVerdictForward makes an already-perceived session's execution experience
// show the human's layer-2 judgment now, by copying the current row forward at
// extractor_ver+1 with only Outcome.Verdict replaced (ADR-0055 Decision 2).
//
// **The event is the truth; this copy only makes it effective today.** The
// verdict lives in the ledger as user.verdict, and every later re-perception
// reads it back through parseDeterministic — so the judgment is imperishable
// and what this carries is immediacy, not permanence. Without it a 👍 would
// change nothing until the extractor happened to be revised, which is how
// test.result stayed at zero for six years.
//
// Exactly one row moves. Amend copies the whole current generation forward
// because experiences_current picks the max ver per (session, kind) and lifting
// one sibling would drop the others out of the view — but a session holds
// exactly one execution row (perceiveSession writes one), and preference
// siblings sit under a different kind, outside this view's grouping. So the
// full copy-forward amend needs is not needed here.
//
// Nothing else about the row changes. extractor_model keeps its original value
// rather than becoming "human" (as amend's does): in an experience row the
// model's own claim is the *context*, while the outcome has always been
// parseDeterministic's — deterministic code reading events. Rewriting the
// provenance of a row whose provenance did not change would be the lie
// (ADR-0033: 出自は嘘をつかない). No user.amended marker is written either, so
// the session stays perceivable — freezing it would tax the human for using
// their veto (Decision 3).
//
// carried reports whether a row was found to move. false is ordinary, not an
// error: a session whose boundary perception has not run yet has nothing to
// carry, and the pending queue will read the event when it gets there.
func (s *Store) CarryVerdictForward(sessionID, verdict string, tsMs int64) (newVer int, carried bool, err error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()

	current, err := currentExperiencesTx(tx, sessionID, core.KindExecution)
	if err != nil {
		return 0, false, err
	}
	if len(current) == 0 {
		return 0, false, nil
	}
	if len(current) > 1 {
		// One execution row per session is an invariant of perceiveSession, and
		// this method's single-row copy depends on it. Say so rather than
		// silently carrying the first and stranding the rest.
		return 0, false, fmt.Errorf(
			"verdict: session %s has %d current execution experiences, expected 1", sessionID, len(current))
	}

	e := current[0]
	newVer = e.ExtractorVer + 1
	e.Outcome.Verdict = verdict

	provider := sql.NullString{String: e.Provider, Valid: e.Provider != ""}
	plan := sql.NullString{String: e.Plan, Valid: e.Plan != ""}
	if _, err := tx.Exec(`
		INSERT INTO experiences
		  (id, session_id, ts, kind, extractor_ver, extractor_model,
		   context, provider, outcome, source, plan)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		NewID(tsMs), e.SessionID, e.TS, e.Kind, newVer, e.ExtractorModel,
		core.MarshalContext(e.Context), provider,
		core.MarshalOutcome(e.Outcome), e.Source, plan); err != nil {
		return 0, false, fmt.Errorf("verdict: carrying %s forward: %w", e.ID, err)
	}
	return newVer, true, tx.Commit()
}

// Vacuum reclaims the space freed by forget so a "消した" is not the
// sovereign lie of leaving the content in WAL frames or free pages
// (ADR-0033 Decision 5).
//
// VACUUM runs first, checkpoint second — the reverse of the two commands'
// natural reading order, and worth stating explicitly: in WAL mode, VACUUM
// writes its rewritten (compacted, freed-page-free) copy of the database
// into new WAL frames, not into the main db file. Checkpointing first would
// only flush whatever the WAL held *before* the compaction — the forgotten
// row's now-unused but not-yet-overwritten page bytes stay in the main file
// regardless, and VACUUM's own output, still sitting in the WAL when Vacuum
// returns, is what a later, unrelated checkpoint would silently clean up
// (or not, if none ever runs). Running VACUUM first means the WAL holds the
// only up-to-date, post-delete pages; TRUNCATE-checkpointing them into the
// main file second is the step that actually overwrites the old bytes on
// disk. Both run outside any transaction — VACUUM cannot run inside one.
//
// The checkpoint runs once, not in a retry loop. Measured (modernc/sqlite,
// WAL, busy_timeout=5000): an idle read-only connection, and one that has
// finished a SELECT, both leave TRUNCATE free to run — it returns busy=0 in
// under a millisecond. Only a connection holding an *open* read transaction
// pins the snapshot, and against that one the PRAGMA already waits out the
// full busy_timeout before reporting busy=1. busy_timeout is the wait; a
// loop around it only multiplies the same 5 seconds by the attempt count
// (measured: 25s for five tries) and buys nothing, because each attempt has
// already waited as long as the first. The face window's poller (ADR-0020,
// mode=ro) queries and returns, so it is the first case, not the third.
//
// A busy report is non-error output from the PRAGMA, so it has to be turned
// into an error here or it becomes silence. It must not: the logical delete
// and rebuild are already committed by the time Vacuum runs (cmdForget calls
// it last), and there is no path back — a second forget of the same id fails
// as "unknown experience". An honest error is the only chance to say that
// physical erasure did not finish (Decision 5's 逆向きの嘘もつかない).
func (s *Store) Vacuum() error {
	if _, err := s.DB.Exec(`VACUUM`); err != nil {
		return err
	}
	var busy, log, checkpointed int
	if err := s.DB.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &checkpointed); err != nil {
		return err
	}
	if busy == 0 && log == 0 {
		return nil
	}
	return fmt.Errorf(
		"vacuum: wal checkpoint incomplete (busy=%d log=%d checkpointed=%d) — a reader holding an open transaction is blocking TRUNCATE",
		busy, log, checkpointed)
}
