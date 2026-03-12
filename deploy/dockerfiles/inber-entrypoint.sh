#!/bin/bash
set -e

DB="${BUS_AGENT_DB:-/data/agents.db}"

# Write API key for inber
mkdir -p /root/.config/inber
echo "ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY:-}" > /root/.config/inber/.env

# Copy agent-store DB if bundled (from build)
if [ -f /etc/inber/agents.db ] && [ ! -f /root/.config/agent-store/agents.db ]; then
    mkdir -p /root/.config/agent-store
    cp /etc/inber/agents.db /root/.config/agent-store/agents.db
    echo "[inber-entrypoint] Seeded agent-store from bundled DB"
fi

# Seed inber backend in registry DB
sqlite3 "$DB" <<'SQL'
CREATE TABLE IF NOT EXISTS backends (
    name TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    config TEXT DEFAULT '{}',
    priority INTEGER DEFAULT 0,
    enabled INTEGER DEFAULT 1
);
INSERT OR IGNORE INTO backends (name, type, config, priority, enabled)
VALUES (
    'inber', 'cli',
    '{"cmd":["inber","run","-a","{agent}"],"dir":"/etc/inber","stdin":"json","features":["meta","spawns","inject"]}',
    0, 1
);
SQL

# Add project column if missing (migration)
sqlite3 "$DB" "ALTER TABLE agents ADD COLUMN project TEXT DEFAULT '';" 2>/dev/null || true

echo "[inber-entrypoint] Starting bus-agent..."
exec bus-agent \
    -bus "${BUS_URL:-http://bus:8100}" \
    -token "${BUS_TOKEN:-}" \
    -consumer "${BUS_CONSUMER:-bus-agent-staging}" \
    -db "$DB" \
    -sync \
    "$@"
