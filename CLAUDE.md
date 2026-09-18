# About forge

## What it owns

Workspace and preview management for orchestrated agents: isolated git worktree slots, builds, and preview deployments tied to agent sessions. **Retired by the operator on 2026-09-18:** `forge-api.service` (`:8150`) is stopped and disabled, its five `forge-env-0-*` Docker containers are stopped (not removed; compose file `~/forge/envs/env-0/docker-compose.yml`), its nginx vhost `forge-dev` is disabled, and healthcheck no longer watches it. `bus` and `inber` still import the module. Do not build on it. ⚠️ What this cost to learn: healthcheck's check had `auto_restart: true` and restarted and re-enabled the unit three minutes after it was first stopped — **take a service out of `healthcheck/config.yaml` before you stop it.**

## Where this prompt lives

These sections are stored in agent-store as a project prompt collection and rendered, with identical text, to `AGENTS.md` and `CLAUDE.md` at the root of this repo, so that whichever file a harness reads it gets the same thing. Edit them on dash `/files`, or edit either rendered file: the 15-minute scan carries the edit back into the sections and out to the other file. The host prompt keeps one row for this repo with only what an agent elsewhere needs.
