-- +goose Up
CREATE TABLE IF NOT EXISTS agent_settings (
  id TEXT PRIMARY KEY CHECK (id = 'default'),
  max_turns INTEGER NOT NULL DEFAULT 15 CHECK (max_turns BETWEEN 1 AND 100),
  updated_at INTEGER NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE IF EXISTS agent_settings;
