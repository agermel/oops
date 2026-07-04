CREATE TABLE IF NOT EXISTS nodelets (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  address TEXT NOT NULL DEFAULT '',
  token TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS mcp_connections (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL DEFAULT '',
  transport TEXT NOT NULL DEFAULT '',
  command TEXT NOT NULL DEFAULT '',
  args_json TEXT NOT NULL DEFAULT '[]',
  env_json TEXT NOT NULL DEFAULT '[]',
  url TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 0,
  container_id TEXT NOT NULL DEFAULT '',
  nodelet_id TEXT NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_connections_container_binding
ON mcp_connections(nodelet_id, container_id)
WHERE nodelet_id <> '' AND container_id <> '';

CREATE TABLE IF NOT EXISTS projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  github_repo TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_name
ON projects(name)
WHERE name <> '';

CREATE TABLE IF NOT EXISTS project_nodelets (
  project_id TEXT NOT NULL,
  nodelet_id TEXT NOT NULL,
  position INTEGER NOT NULL,
  PRIMARY KEY (project_id, nodelet_id),
  FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS project_excluded_containers (
  project_id TEXT NOT NULL,
  ref TEXT NOT NULL,
  PRIMARY KEY (project_id, ref),
  FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS container_dsn_entries (
  nodelet_id TEXT NOT NULL,
  container_id TEXT NOT NULL,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  PRIMARY KEY (nodelet_id, container_id, key)
);

CREATE TABLE IF NOT EXISTS users (
  username TEXT PRIMARY KEY,
  name TEXT NOT NULL DEFAULT '',
  password TEXT NOT NULL DEFAULT ''
);
