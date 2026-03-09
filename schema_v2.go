package forge

// schemaV2SQL is the new environment-based schema
// Migration: environments contain multiple repos, changesets group PRs across repos
const schemaV2SQL = `
-- ============================================
-- PROJECTS (repos that can be in environments)
-- ============================================

CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,                    -- "inber", "bus", "si", "kayushkin"
    base_repo TEXT NOT NULL,                -- local git repo path (e.g., ~/life/repos/inber)
    repo_url TEXT,                          -- remote git URL
    build_cmd TEXT,                         -- "go build -o server ."
    serve_cmd TEXT,                         -- "./server -port {port}"
    is_primary INTEGER DEFAULT 0,           -- 1 if this is the primary project (inber)
    port_offset INTEGER DEFAULT 0,          -- relative to env base_port
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

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
-- DEPLOYS (deployment history)
-- ============================================

CREATE TABLE IF NOT EXISTS deploys (
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

CREATE INDEX IF NOT EXISTS idx_deploys_env ON deploys(environment_id, target);
`
