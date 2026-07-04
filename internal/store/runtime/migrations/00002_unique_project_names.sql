-- +goose Up
CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_name
ON projects(name)
WHERE name <> '';

-- +goose Down
DROP INDEX IF EXISTS idx_projects_name;
