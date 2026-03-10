package forge

// schemaV2SQL is the new environment-based schema
// Migration: environments contain multiple repos, changesets group PRs across repos
const schemaV2SQL = `
-- ============================================
-- PROJECTS (repos that can be in environments)
-- ============================================

-- The v1 projects table already exists, we just need to add new columns
-- SQLite doesn't support IF NOT EXISTS for columns, so we handle this in Go code

-- ============================================
-- ENVIRONMENTS (deployment slots)
-- ============================================

CREATE TABLE IF NOT EXISTS environments (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,              -- "env-0", "env-1"
    base_port INTEGER NOT NULL,             -- 9000, 9100, 9200...
    status TEXT NOT NULL DEFAULT 'idle',    -- idle, active, deploying
    agent_id TEXT,
    session_id TEXT,
    orchestrator TEXT,
    acquired_at INTEGER,
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_environments_status ON environments(status);
CREATE INDEX IF NOT EXISTS idx_environments_session ON environments(session_id);

-- ============================================
-- ENVIRONMENT_REPOS (repos checked out per environment)
-- ============================================

CREATE TABLE IF NOT EXISTS environment_repos (
    environment_id INTEGER NOT NULL,
    project_id TEXT NOT NULL,
    worktree_path TEXT NOT NULL,            -- ~/.envs/env-0/inber
    branch TEXT,
    commit_hash TEXT,
    dirty INTEGER DEFAULT 0,
    PRIMARY KEY (environment_id, project_id),
    FOREIGN KEY (environment_id) REFERENCES environments(id) ON DELETE CASCADE,
    FOREIGN KEY (project_id) REFERENCES projects(id)
);

-- ============================================
-- TARGETS (where environments can deploy)
-- ============================================

CREATE TABLE IF NOT EXISTS targets (
    id TEXT PRIMARY KEY,                    -- "dev", "prod"
    kind TEXT NOT NULL,                     -- "local", "ssh"
    host TEXT,
    user TEXT,
    url_template TEXT,                      -- "{env}.dev.kayushkin.com"
    created_at INTEGER NOT NULL
);

-- ============================================
-- CHANGESETS (grouped PRs across repos)
-- ============================================

CREATE TABLE IF NOT EXISTS changesets (
    id TEXT PRIMARY KEY,                    -- UUID
    environment_id INTEGER NOT NULL,
    title TEXT,
    description TEXT,
    status TEXT NOT NULL DEFAULT 'open',    -- open, merged, closed
    created_at INTEGER NOT NULL,
    merged_at INTEGER,
    FOREIGN KEY (environment_id) REFERENCES environments(id)
);

-- ============================================
-- PULL_REQUESTS (individual PRs per repo)
-- ============================================

CREATE TABLE IF NOT EXISTS pull_requests (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    changeset_id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    pr_url TEXT,
    pr_number INTEGER,
    status TEXT NOT NULL DEFAULT 'open',    -- open, merged, closed
    commit_hash TEXT,
    FOREIGN KEY (changeset_id) REFERENCES changesets(id),
    FOREIGN KEY (project_id) REFERENCES projects(id)
);

-- ============================================
-- DEPLOYS V2 (deployment history by environment)
-- ============================================

-- Note: v1 deploys table already exists with (project, target) keys.
-- We keep both for backward compatibility during migration.
-- New deploys use environment_id, old ones use project.

CREATE TABLE IF NOT EXISTS deploys_v2 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    environment_id INTEGER NOT NULL,
    target TEXT NOT NULL,                   -- "prod", "dev"
    changeset_id TEXT,
    status TEXT NOT NULL DEFAULT 'pending', -- pending, running, success, failed
    error TEXT,
    triggered_by TEXT,
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    FOREIGN KEY (environment_id) REFERENCES environments(id),
    FOREIGN KEY (changeset_id) REFERENCES changesets(id)
);

CREATE INDEX IF NOT EXISTS idx_deploys_v2_env ON deploys_v2(environment_id, target);
`

// MigrateV2 adds v2 columns to existing tables
func (f *Forge) MigrateV2() error {
	// Add new columns to projects if they don't exist
	if _, err := f.db.Exec(`ALTER TABLE projects ADD COLUMN is_primary INTEGER DEFAULT 0`); err != nil {
		// Column might already exist, ignore error
	}
	if _, err := f.db.Exec(`ALTER TABLE projects ADD COLUMN port_offset INTEGER DEFAULT 0`); err != nil {
		// Column might already exist, ignore error
	}
	return nil
}

// MigrateV3 creates v3 tables (no data migration needed, v3 is opt-in)
func (f *Forge) MigrateV3() error {
	// Tables are created in Open(), this is for any column additions
	return nil
}
