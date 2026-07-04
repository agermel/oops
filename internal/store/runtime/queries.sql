-- name: CountNodelets :one
SELECT COUNT(*) FROM nodelets;

-- name: ListNodelets :many
SELECT id, name, address, token FROM nodelets ORDER BY rowid;

-- name: DeleteAllNodelets :exec
DELETE FROM nodelets;

-- name: InsertNodelet :exec
INSERT INTO nodelets (id, name, address, token)
VALUES (?, ?, ?, ?);

-- name: CountMCPConnections :one
SELECT COUNT(*) FROM mcp_connections;

-- name: ListMCPConnections :many
SELECT
  id,
  name,
  type,
  transport,
  command,
  args_json,
  env_json,
  url,
  enabled,
  container_id,
  nodelet_id
FROM mcp_connections
ORDER BY rowid;

-- name: DeleteAllMCPConnections :exec
DELETE FROM mcp_connections;

-- name: InsertMCPConnection :exec
INSERT INTO mcp_connections (
  id,
  name,
  type,
  transport,
  command,
  args_json,
  env_json,
  url,
  enabled,
  container_id,
  nodelet_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: CountProjects :one
SELECT COUNT(*) FROM projects;

-- name: ListProjects :many
SELECT id, name, description, github_repo, created_at, updated_at
FROM projects
ORDER BY rowid;

-- name: ListProjectNodelets :many
SELECT project_id, nodelet_id, position
FROM project_nodelets
ORDER BY project_id, position;

-- name: ListProjectExclusions :many
SELECT project_id, ref
FROM project_excluded_containers
ORDER BY project_id, rowid;

-- name: DeleteAllProjectExclusions :exec
DELETE FROM project_excluded_containers;

-- name: DeleteAllProjectNodelets :exec
DELETE FROM project_nodelets;

-- name: DeleteAllProjects :exec
DELETE FROM projects;

-- name: InsertProject :exec
INSERT INTO projects (id, name, description, github_repo, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: InsertProjectNodelet :exec
INSERT INTO project_nodelets (project_id, nodelet_id, position)
VALUES (?, ?, ?);

-- name: InsertProjectExclusion :exec
INSERT INTO project_excluded_containers (project_id, ref)
VALUES (?, ?);

-- name: CountDSNEntries :one
SELECT COUNT(*) FROM container_dsn_entries;

-- name: ListDSNEntries :many
SELECT nodelet_id, container_id, key, value
FROM container_dsn_entries
ORDER BY nodelet_id, container_id, key;

-- name: DeleteAllDSNEntries :exec
DELETE FROM container_dsn_entries;

-- name: InsertDSNEntry :exec
INSERT INTO container_dsn_entries (nodelet_id, container_id, key, value)
VALUES (?, ?, ?, ?);

-- name: GetUser :one
SELECT username, name, password FROM users LIMIT 1;

-- name: UpsertUser :exec
INSERT INTO users (username, name, password) VALUES (?, ?, ?)
ON CONFLICT(username) DO UPDATE SET name = excluded.name, password = excluded.password;
