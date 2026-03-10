package forge

// schemaV3SQL is the container-based schema
// Slots are now Docker containers, not worktrees
const schemaV3SQL = `
-- ============================================
-- PROJECTS (what can be deployed)
-- ============================================

CREATE TABLE IF NOT EXISTS projects_v3 (
    id TEXT PRIMARY KEY,                    -- "inber-stack", "my-app"
    name TEXT NOT NULL,                     -- display name
    description TEXT,

    -- Container build info
    dockerfile TEXT,                        -- Dockerfile content (or template name)
    dockerfile_template TEXT,               -- OR reference: "golang-node", "node", "python"

    -- Commands (run inside container)
    build_cmd TEXT,                         -- "just build" or "npm run build"
    test_cmd TEXT,                          -- "just test" or "npm test"
    start_cmd TEXT,                         -- "just dev" or "npm start"

    -- Slot config
    slot_count INTEGER DEFAULT 3,           -- how many dev slots to allocate
    base_port INTEGER NOT NULL,             -- starting port (9000, 10000, etc.)
    port_count INTEGER DEFAULT 10,          -- how many ports per slot

    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- ============================================
-- PROJECT_REPOS (which repos a project needs)
-- ============================================

CREATE TABLE IF NOT EXISTS project_repos (
    project_id TEXT NOT NULL,
    repo_id TEXT NOT NULL,                  -- "inber", "bus", "si"
    repo_url TEXT,                          -- git URL (optional if local)
    repo_path TEXT NOT NULL,                -- path inside container: /repos/inber
    branch TEXT DEFAULT 'main',
    PRIMARY KEY (project_id, repo_id),
    FOREIGN KEY (project_id) REFERENCES projects_v3(id) ON DELETE CASCADE
);

-- ============================================
-- REPOS (known git repos)
-- ============================================

CREATE TABLE IF NOT EXISTS repos (
    id TEXT PRIMARY KEY,                    -- "inber", "bus", "kayushkin"
    name TEXT NOT NULL,
    url TEXT,                               -- git remote URL
    local_path TEXT,                        -- local clone path (for faster access)
    default_branch TEXT DEFAULT 'main',
    created_at INTEGER NOT NULL
);

-- ============================================
-- SLOTS (container instances)
-- ============================================

CREATE TABLE IF NOT EXISTS slots_v3 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id TEXT NOT NULL,
    slot_num INTEGER NOT NULL,              -- 0, 1, 2 within project
    container_name TEXT NOT NULL UNIQUE,    -- "inber-stack-0", "my-app-1"

    -- Container state
    status TEXT NOT NULL DEFAULT 'idle',    -- idle, building, running, stopped, error
    container_id TEXT,                      -- docker container ID
    image_id TEXT,                          -- docker image ID

    -- Port mapping (base + slot_num * port_count + offset)
    base_port INTEGER NOT NULL,

    -- Acquisition
    agent_id TEXT,
    session_id TEXT,
    orchestrator TEXT,
    acquired_at INTEGER,

    -- Git state (last known)
    branch TEXT,
    commit_hash TEXT,
    dirty INTEGER DEFAULT 0,

    created_at INTEGER NOT NULL,

    FOREIGN KEY (project_id) REFERENCES projects_v3(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_slots_v3_project_slot ON slots_v3(project_id, slot_num);
CREATE INDEX IF NOT EXISTS idx_slots_v3_status ON slots_v3(status);
CREATE INDEX IF NOT EXISTS idx_slots_v3_session ON slots_v3(session_id);

-- ============================================
-- DEPLOYS (deployment history)
-- ============================================

CREATE TABLE IF NOT EXISTS deploys_v3 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    slot_id INTEGER NOT NULL,
    target TEXT NOT NULL,                   -- "dev", "prod"
    commit_hash TEXT NOT NULL,
    commit_message TEXT,
    branch TEXT,
    status TEXT NOT NULL DEFAULT 'pending', -- pending, running, success, failed
    error TEXT,
    triggered_by TEXT,
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    FOREIGN KEY (slot_id) REFERENCES slots_v3(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_deploys_v3_slot ON deploys_v3(slot_id, target);

-- ============================================
-- DOCKERFILE_TEMPLATES (reusable base images)
-- ============================================

CREATE TABLE IF NOT EXISTS dockerfile_templates (
    id TEXT PRIMARY KEY,                    -- "golang-node", "node", "python"
    dockerfile TEXT NOT NULL,               -- Dockerfile content
    description TEXT,
    created_at INTEGER NOT NULL
);
`

// defaultTemplatesSQL inserts default Dockerfile templates
const defaultTemplatesSQL = `
INSERT OR IGNORE INTO dockerfile_templates (id, dockerfile, description, created_at) VALUES
('golang-node', 'FROM golang:1.22-bookworm

RUN apt-get update && apt-get install -y nodejs npm git curl && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://just.systems/install.sh | bash -s -- --to /usr/local/bin

WORKDIR /repos
CMD ["just", "dev"]
', 'Go + Node.js + Just', strftime('%s', 'now')),

('node', 'FROM node:20-bookworm

RUN apt-get update && apt-get install -y git curl && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://just.systems/install.sh | bash -s -- --to /usr/local/bin

WORKDIR /repos
CMD ["just", "dev"]
', 'Node.js + Just', strftime('%s', 'now')),

('golang', 'FROM golang:1.22-bookworm

RUN apt-get update && apt-get install -y git curl && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://just.systems/install.sh | bash -s -- --to /usr/local/bin

WORKDIR /repos
CMD ["just", "dev"]
', 'Go + Just', strftime('%s', 'now'));
`
