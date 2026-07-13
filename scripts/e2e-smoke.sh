#!/usr/bin/env bash
# Boot-and-answer smoke test for forge.
#
# Builds cmd/forge from THIS source tree, drives the CLI against a throwaway
# HOME, boots the `forge api` daemon on a temp port, and asserts that the state
# the CLI wrote actually comes back out of the HTTP API.
#
# Why this exists: `go build` proves the tree compiles, not that the binary is
# alive. Go 1.22+ http.ServeMux panics on a conflicting route pattern at
# REGISTRATION time — the compiler never sees it, `go vet` never sees it, and
# the process dies on its first breath. Tier 1 of the repo guard calls such a
# tree green. This is tier 2: it boots the thing and makes it answer.
#
# WHAT MAKES THIS HERMETIC:
#   * HOME is redirected to a temp dir. This is load-bearing, not hygiene:
#     forge.Open("") resolves its SQLite file from DefaultPath() =
#     $HOME/.config/forge/forge.db (forge.go:16-24) and there is NO env var or
#     flag to override it. Unsandboxed, this script would open, migrate and
#     write the LIVE forge database that the :8150 daemon serves.
#   * --port moves the daemon off the live :8150 (api.go:17).
#   * No NATS, no LLM, no network: forge's whole dependency set is
#     mattn/go-sqlite3 + google/uuid, and boot does nothing but open SQLite and
#     run its schema migrations.
#
# WHAT THIS DELIBERATELY DOES NOT TOUCH:
#   * POST /api/forge/routes — it REWRITES ~/.config/systemd/user/*.service and
#     runs `systemctl --user restart` (api.go:494-556). A smoke must never call
#     it. GET on the same path is read-only and safe, and with a temp HOME it
#     reads an empty directory.
#   * GET /api/forge/topology — shells out to `ss` and `docker compose ps`
#     (api.go:193, 785). Its result depends on what happens to be running on the
#     box, which is a coin flip, not a guard.
#
# Exits 0 on success, non-zero on the FIRST failing assertion, dumping the
# server log to stderr.
#
# Env:
#   E2E_PORT   — forge api port  (default 19133)
#   E2E_KEEP=1 — leave $TMP_DIR in place after the run

set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PORT="${E2E_PORT:-19133}"
BASE="http://127.0.0.1:$PORT"

for bin in go curl jq; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "ERROR: required tool '$bin' not found on PATH" >&2
    exit 2
  fi
done

TMP_DIR="$(mktemp -d -t forge-e2e.XXXXXX)"
BIN_DIR="$TMP_DIR/bin"
FAKE_HOME="$TMP_DIR/home"
DB_PATH="$FAKE_HOME/.config/forge/forge.db"
SERVER_LOG="$TMP_DIR/server.log"
mkdir -p "$BIN_DIR" "$FAKE_HOME"

SERVER_PID=""
cleanup() {
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  if [ "${E2E_KEEP:-}" = "1" ]; then
    echo "[e2e] keeping $TMP_DIR"
  else
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT INT TERM

step() { printf '\n==> %s\n' "$*"; }
dump_logs() {
  echo "----- server.log -----" >&2
  cat "$SERVER_LOG" >&2 2>/dev/null || true
}
fail() { echo "FAIL: $*" >&2; dump_logs; exit 1; }

# Unique per run, so an assertion can only pass on state THIS run created.
CHANGE="e2e-change-$$"
AGENT="e2e-agent-$$"

step "build cmd/forge from $REPO_DIR"
cd "$REPO_DIR"
# Explicit -o: a bare `go build ./cmd/forge` drops the binary into the CWD,
# which in a clean clone is the repo root.
# CGO stays ENABLED on purpose — forge's SQLite driver is mattn/go-sqlite3,
# which is cgo-only. Forcing CGO_ENABLED=0 would fail the build here rather
# than ship a binary that dies opening its own database.
go build -o "$BIN_DIR/forge" ./cmd/forge
echo "    forge: $(ls -lh "$BIN_DIR/forge" | awk '{print $5}')"

step "forge init — create the project and its slots in the throwaway DB"
HOME="$FAKE_HOME" "$BIN_DIR/forge" init >"$TMP_DIR/init.log" 2>&1 \
  || { cat "$TMP_DIR/init.log" >&2; fail "forge init exited non-zero"; }

# HOME honoured? If forge resolved DefaultPath() against the real HOME instead,
# this file would be absent — and the live DB would have been the one migrated.
# Assert this BEFORE anything else touches the DB, while the damage would still
# be nil.
[ -f "$DB_PATH" ] || fail "no db at $DB_PATH — HOME was not honoured, forge may have opened the LIVE ~/.config/forge/forge.db"
echo "    db: $DB_PATH ($(ls -lh "$DB_PATH" | awk '{print $5}'))"

step "forge slot open $CHANGE $AGENT — claim a slot (DB-only, no git, no docker)"
SLOT=$(HOME="$FAKE_HOME" "$BIN_DIR/forge" slot open "$CHANGE" "$AGENT" 2>"$TMP_DIR/open.err" | tail -1)
case "$SLOT" in
  ''|*[!0-9]*) cat "$TMP_DIR/open.err" >&2; fail "slot open did not print a slot number, got: '$SLOT'" ;;
esac
echo "    claimed slot $SLOT"

step "launch forge api on :$PORT"
HOME="$FAKE_HOME" "$BIN_DIR/forge" api --port "$PORT" >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!
echo "    pid: $SERVER_PID"

# Poll — never sleep-and-hope. forge has no /health, so /api/forge/deploys is
# the readiness probe: it is pure SQLite (no shell-outs) and returns a non-nil
# slice, so a fresh DB answers `[]` rather than `null`.
#
# Abort the instant the pid dies. That is how a route-registration panic
# surfaces as a named failure instead of a ten-second mystery timeout.
deploys=""
for _ in $(seq 1 50); do
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    fail "forge api exited during startup (route registration panic?)"
  fi
  if deploys=$(curl -fsS --max-time 2 "$BASE/api/forge/deploys" 2>/dev/null); then break; fi
  deploys=""
  sleep 0.2
done
[ -n "$deploys" ] || fail "forge api did not answer $BASE/api/forge/deploys within 10s"
echo "    up"

step "GET /api/forge/environments — the slot the CLI opened must come back"
ENVS=$(curl -fsS --max-time 10 "$BASE/api/forge/environments")
# `null` here is a real, distinct failure from `[]`: the handler declares
# `var envs []envResponse` (api.go:144), so a nil slice serialises as `null`
# and means the project rows are missing — i.e. the daemon is reading a
# DIFFERENT database than the CLI wrote. jq 'length' would report 0 for both.
[ "$(jq -r 'type' <<<"$ENVS")" = "array" ] \
  || fail "/api/forge/environments returned $(jq -r 'type' <<<"$ENVS"), not an array — the api is reading a different DB than the CLI wrote: $ENVS"

MINE=$(jq -c --argjson slot "$SLOT" '.[] | select(.id == $slot)' <<<"$ENVS")
[ -n "$MINE" ] || fail "slot $SLOT absent from /api/forge/environments: $ENVS"

# Assert the parsed CONTENT, not a 200. The round trip is: the CLI's SQLite
# write -> the daemon's own read -> the JSON shape dash consumes.
[ "$(jq -r '.status' <<<"$MINE")" = "active" ] || fail "slot $SLOT is not active: $MINE"
[ "$(jq -r '.change' <<<"$MINE")" = "$CHANGE" ] || fail "slot $SLOT change != $CHANGE: $MINE"
[ "$(jq -r '.name'   <<<"$MINE")" = "env-$SLOT" ] || fail "slot $SLOT name != env-$SLOT: $MINE"
[ "$(jq -r '.agents | index("'"$AGENT"'") // "no"' <<<"$MINE")" != "no" ] \
  || fail "slot $SLOT does not list agent $AGENT: $MINE"
echo "    slot $SLOT: active, change=$CHANGE, agents=[$AGENT]"

step "GET /api/forge/deploys — the slot_log row the CLI wrote must come back"
DEPLOYS=$(curl -fsS --max-time 10 "$BASE/api/forge/deploys")
[ "$(jq -r 'type' <<<"$DEPLOYS")" = "array" ] || fail "/api/forge/deploys is not an array: $DEPLOYS"
ENTRY=$(jq -c --arg a "$AGENT" '.[] | select(.agent == $a and .action == "open")' <<<"$DEPLOYS")
[ -n "$ENTRY" ] || fail "no open-action log entry for $AGENT in /api/forge/deploys: $DEPLOYS"
[ "$(jq -r '.detail' <<<"$ENTRY")" = "change=$CHANGE" ] || fail "log entry detail != change=$CHANGE: $ENTRY"
[ "$(jq -r '.slot' <<<"$ENTRY")" = "$SLOT" ] || fail "log entry slot != $SLOT: $ENTRY"
# timestamp is unix seconds (api.go:847-865); 0 would mean the column never got written.
TS=$(jq -r '.timestamp' <<<"$ENTRY")
[ "$TS" -gt 0 ] 2>/dev/null || fail "log entry has a zero/absent unix timestamp: $ENTRY"
echo "    deploy log: slot=$SLOT agent=$AGENT action=open detail=change=$CHANGE ts=$TS"

step "GET /api/forge/routes — PINS A KNOWN BUG: the values are fabricated, not read"
# The read side of the route this smoke must never POST to.
#
# This assertion pins current behaviour, and that behaviour is WRONG. It is
# asserted-as-is on purpose so that fixing the bug trips this step deliberately
# rather than silently.
#
# getRoutes() (api.go:380) reads each service's systemd unit for its routable
# Environment= vars. parseSystemdEnv() (api.go:475-480) returns an EMPTY MAP on
# a missing file — the read error is swallowed — and then getRoutes() fills every
# var it did not find from a hardcoded fallback table (api.go:406-419).
#
# So a var that no unit declares is INDISTINGUISHABLE from one pinned to the
# fallback value, and the API reports the guess as if it were the configuration.
# On the live box the real units declare exactly ONE of the six routable vars
# (si.service's LOGSTACK_URL) — every other value this endpoint serves is
# invented. It is a rerouting UI: it tells an operator where prod currently
# points, and five sixths of that answer is a hardcoded literal.
#
# Here, with HOME sandboxed, NO unit file exists at all — so a correct
# implementation would return empty values, and this returns the full fallback
# table. That is what we assert. Filed as its own todo; when it is fixed, this
# step fails and sends whoever fixed it here to read this comment.
ROUTES=$(curl -fsS --max-time 10 "$BASE/api/forge/routes")
[ "$(jq -r 'type' <<<"$ROUTES")" = "array" ] || fail "/api/forge/routes is not an array: $ROUTES"
FORGE_URL=$(jq -r '.[] | select(.service == "kayushkin" and .env_var == "FORGE_API_URL") | .value' <<<"$ROUTES")
[ "$FORGE_URL" = "http://127.0.0.1:8150" ] || fail "the fabricated-fallback pin has changed (expected the hardcoded http://127.0.0.1:8150, got '$FORGE_URL'). If you just made getRoutes() stop inventing values for undeclared vars: GOOD — that is the fix. Update this assertion to expect an empty value."
echo "    routes: $(jq -r 'length' <<<"$ROUTES") entries, values FABRICATED from the hardcoded table (no unit file exists in the sandbox) — known bug, pinned"

step "process still alive after serving every route"
kill -0 "$SERVER_PID" 2>/dev/null || fail "forge api died while serving"

printf '\nsmoke test OK (forge boots, migrates, and answers on :%s)\n' "$PORT"
