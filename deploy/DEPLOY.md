# Forge Deploy System

## Overview

Two deployment targets:
- **Prod** (kayushkin.com) — native binaries, deployed via `forge-deploy-prod`
- **Staging** (N.dev.kayushkin.com) — Docker containers, 3 environments (env-0, env-1, env-2)

## Production Deployment

**⚠️ ONLY Claxon (main agent) runs prod deploys. Individual agents should NOT deploy to prod directly.**

When your code is ready for prod:
1. Push to main branch on GitHub
2. Ask Claxon to deploy, or Claxon will deploy during routine checks

### Script: `forge-deploy-prod`

Location: `~/repos/forge/deploy/forge-deploy-prod`

```bash
# Deploy a single service
forge-deploy-prod kayushkin
forge-deploy-prod bus

# Deploy everything (dependency order: bus → si → logstack → kayushkin → bookstack → downloadstack → videostack)
forge-deploy-prod all
```

**What it does per service:**
1. `git pull` on the server
2. `go build`
3. Stop service + kill stragglers
4. Back up old binary
5. Swap in new binary
6. Start service
7. Health check (3 retries) → auto-rollback on failure

### Services

| Service | Repo | Service Type | Health Check |
|---------|------|-------------|--------------|
| kayushkin | kayushkin.com | system | localhost:8080/api/health |
| bus | bus | user | localhost:8100/stats |
| si | si | user | — |
| logstack | logstack | user | localhost:8088/api/v1/stats |
| bookstack | bookstack | system | — |
| downloadstack | downloadstack | system | — |
| videostack | videostack | system | — |

## Staging Environments

Three Docker Compose environments for testing changes across multiple projects.

| Env | URL | Ports |
|-----|-----|-------|
| env-0 | http://0.dev.kayushkin.com | web=9000, bus=9010, si=9020 |
| env-1 | http://1.dev.kayushkin.com | web=9100, bus=9110, si=9120 |
| env-2 | http://2.dev.kayushkin.com | web=9200, bus=9210, si=9220 |

### Managing Environments

Script: `~/repos/forge/deploy/forge-env` (run on server or via SSH)

```bash
# Set env vars first
export REPOS_DIR=$HOME/repos FORGE_DIR=$HOME/repos/forge ENVS_DIR=$HOME/forge/envs

forge-env start 0      # build & start env-0
forge-env stop 1       # stop env-1
forge-env restart 2    # rebuild & restart env-2
forge-env status       # show all environments
forge-env logs 0       # tail logs for env-0
forge-env ps 1         # show containers for env-1
```

### Updating Staging Code

Repos are at `~/repos/` on the server. To test a branch:
```bash
cd ~/repos/kayushkin.com && git checkout feature-branch
forge-env restart 0
```

## Server Layout

```
~/repos/              ← git clones (bus, si, kayushkin.com, forge, bookstack, downloadstack, videostack, logstack)
~/bin/                ← prod binaries (kayushkin-server, bus, si, logstack, tunnel-server)
~/bookstack/          ← prod binary + config
~/downloadstack/      ← prod binary + config
~/videostack/         ← prod binary + config
~/forge/envs/         ← staging docker compose files
```
