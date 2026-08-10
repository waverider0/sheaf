package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, no cgo
)

// db is the app's single database handle, initialized once in initDB.
var db *sql.DB

// schemaTables is the full v1 table set. Every statement is IF NOT EXISTS,
// so it is safe to run on every startup. Changes that alter existing tables
// live in the migrate step below, not here. Indexes are separate
// (schemaIndexes) because they must run after migrations.
const schemaTables = `
CREATE TABLE IF NOT EXISTS players (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  display_name        TEXT    NOT NULL DEFAULT 'New Player',
  role                INTEGER NOT NULL DEFAULT 0, -- 0 = player, 1 = problem author
  rating              INTEGER NOT NULL DEFAULT 400,
  created_at_unix_ms  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS problems (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  author              INTEGER REFERENCES players(id),
  text                TEXT    NOT NULL,
  answers             TEXT    NOT NULL,
  solutions           TEXT    NOT NULL,
  topic               TEXT    NOT NULL DEFAULT '',  -- TODO (later):
  rating              INTEGER NOT NULL DEFAULT 400, -- embedding vector and strength scalar
  version             INTEGER NOT NULL DEFAULT 1,
  created_at_unix_ms  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS games (
  id                        INTEGER PRIMARY KEY AUTOINCREMENT,
  player_id                 INTEGER NOT NULL REFERENCES players(id),
  problem_id                INTEGER NOT NULL REFERENCES problems(id),
  problem_version           INTEGER NOT NULL,
  status                    TEXT    NOT NULL DEFAULT 'in_progress' CHECK (status IN ('in_progress', 'finished')),
  started_at_unix_ms        INTEGER NOT NULL,
  last_activity_at_unix_ms  INTEGER NOT NULL,
  submissions               TEXT    NOT NULL DEFAULT '[]', -- JSON
  points                    INTEGER NOT NULL DEFAULT 0,
  result                    INTEGER CHECK (result IN (0, 1)),
  player_rating_before      INTEGER,
  player_rating_after       INTEGER,
  problem_rating_before     INTEGER,
  problem_rating_after      INTEGER,
  finished_at_unix_ms       INTEGER,
  CHECK (
    (
		  status = 'in_progress'
		  AND result IS NULL
			AND player_rating_after IS NULL
			AND problem_rating_after IS NULL
			AND finished_at_unix_ms IS NULL
		)
    OR
    (
		  status = 'finished'
			AND result IS NOT NULL
			AND player_rating_after IS NOT NULL
			AND problem_rating_after IS NOT NULL
			AND finished_at_unix_ms IS NOT NULL
		)
  )
);

-- finished rows are a permanent log: no updates, ever.
CREATE TRIGGER IF NOT EXISTS games_finished_immutable
BEFORE UPDATE ON games
WHEN OLD.status = 'finished'
BEGIN
  SELECT RAISE(ABORT, 'finished games are immutable');
END;

CREATE TABLE IF NOT EXISTS searches (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  player_id           INTEGER REFERENCES players(id),
  query               TEXT    NOT NULL CHECK (length(query) <= 200),
  created_at_unix_ms  INTEGER NOT NULL
);

`

// schemaIndexes run after migrations so they can reference migrated columns.
const schemaIndexes = `
-- partial index: only in-progress rows, stays tiny even as the log grows
CREATE INDEX IF NOT EXISTS idx_games_active         ON games(player_id) WHERE status = 'in_progress';
CREATE INDEX IF NOT EXISTS idx_games_player         ON games(player_id);
CREATE INDEX IF NOT EXISTS idx_games_problem        ON games(problem_id);
CREATE INDEX IF NOT EXISTS idx_searches_created_at_unix_ms  ON searches(created_at_unix_ms);
CREATE INDEX IF NOT EXISTS idx_problems_topic       ON problems(topic);
`

// initDB opens (creating if needed) the SQLite database at path, applies the
// schema, migrates pre-v1 tables, and leaves the package-global db ready.
//
// The _pragma DSN params apply per-connection, which is what busy_timeout
// and foreign_keys need; journal_mode also persists in the DB file itself
// once WAL is active.
func initDB(path string) {
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)"
	var err error
	db, err = sql.Open("sqlite", dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	// SQLite allows one writer; a single connection avoids SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schemaTables); err != nil {
		log.Fatalf("init schema: %v", err)
	}
	if err := migrate(); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if _, err := db.Exec(schemaIndexes); err != nil {
		log.Fatalf("init indexes: %v", err)
	}
}

// migrate upgrades pre-v1 databases: the old searches table (id, query)
// gains the v1 columns, and the pre-release active_games/finished_games
// split is dropped in favor of the merged games table. All steps are
// no-ops on a fresh database.
func migrate() error {
	// the two-table games split predates any real data; nothing writes to
	// these tables anymore, so they are dropped outright.
	for _, t := range []string{"active_games", "finished_games"} {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + t); err != nil {
			return fmt.Errorf("drop %s: %w", t, err)
		}
	}
	// v1 indexes searches by created_at_unix_ms; drop the legacy index that
	// pointed at the pre-v1 created_at column (schemaIndexes recreates the
	// correct one afterwards).
	if _, err := db.Exec("DROP INDEX IF EXISTS idx_searches_created_at"); err != nil {
		return fmt.Errorf("drop legacy searches index: %w", err)
	}
	return migrateSearches()
}

// migrateSearches brings searches to its v1 shape. Two legacy shapes exist:
// pre-v1 (id, query[, created_at[, player_id]]) and the intermediate shape
// that gained a stray created_at column alongside v1's created_at_unix_ms.
// Both are rebuilt without the dead column; pre-v1 timestamps (UnixMilli,
// same units) are carried over.
func migrateSearches() error {
	hasUnixMs, err := hasColumn("searches", "created_at_unix_ms")
	if err != nil {
		return err
	}
	hasCreatedAt, err := hasColumn("searches", "created_at")
	if err != nil {
		return err
	}
	hasPlayerID, err := hasColumn("searches", "player_id")
	if err != nil {
		return err
	}
	if hasUnixMs && !hasCreatedAt {
		return nil // already v1
	}

	tsCol := "0"
	if hasUnixMs {
		tsCol = "created_at_unix_ms"
	} else if hasCreatedAt {
		tsCol = "created_at"
	}
	selCols, insCols := "id, query", "id, query"
	if hasPlayerID {
		selCols += ", player_id"
		insCols += ", player_id"
	}
	selCols += ", " + tsCol
	insCols += ", created_at_unix_ms"

	// Single-connection DB: holding the tx reserves the only conn, so all
	// statements here must go through tx.
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin searches migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DROP TABLE IF EXISTS searches_new"); err != nil {
		return fmt.Errorf("drop searches_new: %w", err)
	}
	if _, err := tx.Exec(`CREATE TABLE searches_new (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  player_id           INTEGER REFERENCES players(id),
  query               TEXT    NOT NULL CHECK (length(query) <= 200),
  created_at_unix_ms  INTEGER NOT NULL
)`); err != nil {
		return fmt.Errorf("create searches_new: %w", err)
	}
	if _, err := tx.Exec(fmt.Sprintf(
		`INSERT INTO searches_new (%s) SELECT %s FROM searches`,
		insCols, selCols,
	)); err != nil {
		return fmt.Errorf("copy searches: %w", err)
	}
	if _, err := tx.Exec("DROP TABLE searches"); err != nil {
		return fmt.Errorf("drop searches: %w", err)
	}
	if _, err := tx.Exec("ALTER TABLE searches_new RENAME TO searches"); err != nil {
		return fmt.Errorf("rename searches: %w", err)
	}
	return tx.Commit()
}

// hasColumn reports whether table has a column named col.
func hasColumn(table, col string) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`,
		table, col,
	).Scan(&n)
	return n > 0, err
}

// dbPath returns the SQLite file location, overridable via SHEAF_DB.
func dbPath() string {
	if p := os.Getenv("SHEAF_DB"); p != "" {
		return p
	}
	return "sheaf.db"
}
