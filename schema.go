package forge

const schemaSQL = `
-- ============================================
-- PROJECTS (what can be forged)
-- ============================================

CREATE TABLE IF NOT EXISTS projects (
    id TEXT PRIMARY KEY,                    -- "kayushkin", "inber", "si"
    base_repo TEXT NOT NULL,                -- local git repo path
    pool_dir TEXT NOT NULL,                 -- where worktrees live
    pool_size INTEGER NOT NULL DEFAULT 3,
    default_branch TEXT DEFAULT 'main',
    repo_url TEXT,                          -- remote git URL (for cloning on targets)
    build_cmd TEXT,                         -- "go build -o server ."
    serve_cmd TEXT,                         -- "./server -port {port} -build ./build"
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- ============================================
-- SLOTS (isolated workspaces)
-- ============================================

CREATE TABLE IF NOT EXISTS slots (
    id INTEGER NOT NULL,
    project TEXT NOT NULL,
    path TEXT NOT NULL,                     -- worktree path
    branch TEXT,                            -- current branch
    status TEXT NOT NULL DEFAULT 'ready',   -- ready, acquired, building, previewing, dirty

    -- Orchestration context
    agent_id TEXT,
    session_id TEXT,
    orchestrator TEXT,                      -- "inber", "openclaw"

    acquired_at INTEGER,
    released_at INTEGER,
    PRIMARY KEY (project, id),
    FOREIGN KEY (project) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_slots_status ON slots(project, status);
CREATE INDEX IF NOT EXISTS idx_slots_session ON slots(session_id);

-- ============================================
-- TARGETS (where previews can run)
-- ============================================

CREATE TABLE IF NOT EXISTS targets (
    id TEXT PRIMARY KEY,                    -- "kayushkin-dev", "local", "docker"
    kind TEXT NOT NULL,                     -- "ssh", "local", "docker"

    -- SSH targets
    host TEXT,
    user TEXT,
    deploy_dir TEXT,                        -- remote directory for deployments

    -- Preview routing
    base_port INTEGER DEFAULT 9000,
    url_template TEXT,                      -- "{slot}.dev.kayushkin.com" or "localhost:{port}"

    created_at INTEGER NOT NULL
);

-- ============================================
-- PREVIEWS (running preview instances)
-- ============================================

CREATE TABLE IF NOT EXISTS previews (
    id TEXT PRIMARY KEY,
    project TEXT NOT NULL,
    slot_id INTEGER NOT NULL,
    target_id TEXT NOT NULL,

    -- What's deployed
    branch TEXT,
    commit_hash TEXT,

    -- Where it's running
    host TEXT,
    port INTEGER,
    pid INTEGER,
    url TEXT,                               -- resolved preview URL

    -- Status
    status TEXT NOT NULL DEFAULT 'pending', -- pending, building, running, stopped, failed
    error TEXT,

    -- Orchestration context (who triggered this)
    agent_id TEXT,
    session_id TEXT,
    orchestrator TEXT,

    started_at INTEGER,
    stopped_at INTEGER,

    FOREIGN KEY (project, slot_id) REFERENCES slots(project, id) ON DELETE CASCADE,
    FOREIGN KEY (target_id) REFERENCES targets(id)
);

CREATE INDEX IF NOT EXISTS idx_previews_status ON previews(status);
CREATE INDEX IF NOT EXISTS idx_previews_project ON previews(project, slot_id);
CREATE INDEX IF NOT EXISTS idx_previews_session ON previews(session_id);

-- ============================================
-- BUILDS (build history per slot)
-- ============================================

CREATE TABLE IF NOT EXISTS builds (
    id TEXT PRIMARY KEY,
    project TEXT NOT NULL,
    slot_id INTEGER NOT NULL,

    -- Build info
    branch TEXT,
    commit_hash TEXT,
    build_cmd TEXT,
    status TEXT NOT NULL DEFAULT 'pending', -- pending, running, success, failed
    output TEXT,                            -- build stdout/stderr
    duration_ms INTEGER,

    -- Orchestration context
    agent_id TEXT,
    session_id TEXT,
    orchestrator TEXT,

    started_at INTEGER,
    finished_at INTEGER,

    FOREIGN KEY (project, slot_id) REFERENCES slots(project, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_builds_project ON builds(project, slot_id);
CREATE INDEX IF NOT EXISTS idx_builds_status ON builds(status);

-- ============================================
-- DEPLOYS (deployment history)
-- ============================================

CREATE TABLE IF NOT EXISTS deploys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project TEXT NOT NULL,
    target TEXT NOT NULL,                   -- "prod", "dev-0", "dev-1", "dev-2"
    commit_hash TEXT NOT NULL,
    commit_message TEXT,
    branch TEXT,
    status TEXT NOT NULL DEFAULT 'pending', -- pending, running, success, failed
    error TEXT,
    triggered_by TEXT,                      -- "slava", "brigid", etc.
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    reverted_at INTEGER,                    -- set when this deploy was reverted
    FOREIGN KEY (project) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_deploys_project ON deploys(project, target);
CREATE INDEX IF NOT EXISTS idx_deploys_status ON deploys(status);
`
