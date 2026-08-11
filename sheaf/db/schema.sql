CREATE TABLE IF NOT EXISTS players (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  rating              INTEGER NOT NULL DEFAULT 400, -- TODO (later): skill vector
  created_at_unix_ms  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS problems (
  slug                TEXT PRIMARY KEY,
  author              TEXT,
  topic               TEXT    NOT NULL DEFAULT '',  -- TODO (later):
  rating              INTEGER NOT NULL DEFAULT 400, -- embedding vector and strength scalar
  version             INTEGER NOT NULL DEFAULT 1,
  created_at_unix_ms  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS games (
  id                     INTEGER PRIMARY KEY AUTOINCREMENT,
  player_id              INTEGER NOT NULL REFERENCES players(id),
  problem_slug           TEXT    NOT NULL REFERENCES problems(slug),
  problem_version        INTEGER NOT NULL,
  in_progress            INTEGER NOT NULL DEFAULT 1 CHECK (in_progress IN (0,1)),
  started_at_unix_ms     INTEGER NOT NULL,
  submissions            TEXT    NOT NULL DEFAULT '[]',
  result                 INTEGER CHECK (result IN (0,1)),
  player_rating_before   INTEGER,
  player_rating_after    INTEGER,
  problem_rating_before  INTEGER,
  problem_rating_after   INTEGER,
  finished_at_unix_ms    INTEGER,
  CHECK (
    (
      in_progress = 1
      AND result IS NULL
      AND player_rating_after IS NULL
      AND problem_rating_after IS NULL
      AND finished_at_unix_ms IS NULL
    )
    OR
    (
      in_progress = 0
      AND result IS NOT NULL
      AND player_rating_after IS NOT NULL
      AND problem_rating_after IS NOT NULL
      AND finished_at_unix_ms IS NOT NULL
    )
  )
);

CREATE TRIGGER IF NOT EXISTS games_finished_immutable
BEFORE UPDATE ON games
WHEN OLD.in_progress = 0
BEGIN
  SELECT RAISE(ABORT, 'finished games are immutable');
END;

CREATE TABLE IF NOT EXISTS searches (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  player_id           INTEGER REFERENCES players(id),
  query               TEXT    NOT NULL CHECK (length(query) <= 200),
  created_at_unix_ms  INTEGER NOT NULL
);

-- partial index: only in-progress rows, stays tiny even as the log grows
CREATE INDEX IF NOT EXISTS idx_games_active                 ON games(player_id) WHERE in_progress = 1;
CREATE INDEX IF NOT EXISTS idx_games_player                 ON games(player_id);
CREATE INDEX IF NOT EXISTS idx_games_problem                ON games(problem_slug);
CREATE INDEX IF NOT EXISTS idx_searches_created_at_unix_ms  ON searches(created_at_unix_ms);
CREATE INDEX IF NOT EXISTS idx_problems_topic               ON problems(topic);
