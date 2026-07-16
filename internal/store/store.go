// Package store implements SCHEMA.md v1.0 on SQLite: append-only truth
// (events, experiences), rebuildable projections (connections,
// surprise_ledger), and persistent working state (curiosity_queue).
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
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

type Store struct {
	DB *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
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

// PendingSessions returns finished sessions that have no experience at
// extractor_ver >= ver — the Deferred Perception queue, derived by query
// (SCHEMA.md: no table needed).
func (s *Store) PendingSessions(ver int) ([]string, error) {
	// ORDER BY id references a column outside the DISTINCT projection —
	// rejected by strict SQL, accepted by SQLite (fixed backend, ADR-0004).
	rows, err := s.DB.Query(`
		SELECT DISTINCT session_id FROM events e
		WHERE type IN ('task.finished','task.cancelled')
		  AND session_id NOT IN (
		    SELECT session_id FROM experiences WHERE extractor_ver >= ?
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
