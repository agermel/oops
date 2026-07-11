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
ORDER BY created_at, id;

-- name: GetProject :one
SELECT id, name, description, github_repo, created_at, updated_at
FROM projects
WHERE id = ?;

-- name: ListProjectNodelets :many
SELECT project_id, nodelet_id, position
FROM project_nodelets
ORDER BY project_id, position, nodelet_id;

-- name: ListProjectNodeletsForProject :many
SELECT project_id, nodelet_id, position
FROM project_nodelets
WHERE project_id = ?
ORDER BY position, nodelet_id;

-- name: ListProjectExclusions :many
SELECT project_id, ref
FROM project_excluded_containers
ORDER BY project_id, ref;

-- name: ListProjectExclusionsForProject :many
SELECT project_id, ref
FROM project_excluded_containers
WHERE project_id = ?
ORDER BY ref;

-- name: UpdateProject :execrows
UPDATE projects
SET name = ?, description = ?, github_repo = ?, updated_at = ?
WHERE id = ?;

-- name: DeleteProject :execrows
DELETE FROM projects
WHERE id = ?;

-- name: DeleteProjectNodelets :exec
DELETE FROM project_nodelets
WHERE project_id = ?;

-- name: DeleteProjectExclusions :exec
DELETE FROM project_excluded_containers
WHERE project_id = ?;

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

-- name: ListDSNEntriesForContainer :many
SELECT nodelet_id, container_id, key, value
FROM container_dsn_entries
WHERE nodelet_id = ? AND container_id = ?
ORDER BY key;

-- name: DeleteDSNEntriesForContainer :exec
DELETE FROM container_dsn_entries
WHERE nodelet_id = ? AND container_id = ?;

-- name: DeleteDSNEntry :exec
DELETE FROM container_dsn_entries
WHERE nodelet_id = ? AND container_id = ? AND key = ?;

-- name: InsertDSNEntry :exec
INSERT INTO container_dsn_entries (nodelet_id, container_id, key, value)
VALUES (?, ?, ?, ?);

-- name: UpsertDSNEntry :exec
INSERT INTO container_dsn_entries (nodelet_id, container_id, key, value)
VALUES (?, ?, ?, ?)
ON CONFLICT(nodelet_id, container_id, key) DO UPDATE SET value = excluded.value;

-- name: GetUser :one
SELECT username, name, password FROM users LIMIT 1;

-- name: UpsertUser :exec
INSERT INTO users (username, name, password) VALUES (?, ?, ?)
ON CONFLICT(username) DO UPDATE SET name = excluded.name, password = excluded.password;

-- name: GetAgentSettings :one
SELECT id, max_turns, updated_at FROM agent_settings WHERE id = 'default';

-- name: UpsertAgentSettings :exec
INSERT INTO agent_settings (id, max_turns, updated_at)
VALUES ('default', ?, ?)
ON CONFLICT(id) DO UPDATE SET
  max_turns = excluded.max_turns,
  updated_at = excluded.updated_at;
