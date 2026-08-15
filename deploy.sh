#!/usr/bin/env bash
# Build, install and restart the live forge-api daemon, then prove it answers.
#
# The unit is installed FROM deploy/forge-api.service, and the two values this
# script needs (binary path, port) are read back OUT of that unit's ExecStart
# rather than restated here. There is deliberately no second copy of them to
# drift: if the unit and this script ever disagree, someone edited the unit, and
# this script follows.
#
# Before this existed, ~/bin/forge was hand-built and hand-copied. It sat at a
# 2026-03-13 build, 30 commits behind HEAD, for four months — which is what
# "there is no deploy path" costs.
#
# forge-api.service is a --user unit, so no sudo is involved.
#
# Usage: ./deploy.sh
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
UNIT_SRC="$REPO_DIR/deploy/forge-api.service"
UNIT_DIR="$HOME/.config/systemd/user"
UNIT_NAME="forge-api.service"
BACKUP_DIR="$HOME/.local/share/forge-deploy-backup"
DB_PATH="$HOME/.config/forge/forge.db"   # forge.DefaultPath(); no env var or flag overrides it

# systemctl --user needs these to reach the user manager. Without them it prints
# NOTHING and exits 0 — silence that reads like success. Set them if absent.
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=$XDG_RUNTIME_DIR/bus}"

step() { printf '\n==> %s\n' "$*"; }
fail() { echo "DEPLOY FAILED: $*" >&2; exit 1; }

for bin in go curl jq systemctl; do
  command -v "$bin" >/dev/null 2>&1 || fail "required tool '$bin' not on PATH"
done
[ -f "$UNIT_SRC" ] || fail "missing unit template: $UNIT_SRC"

step "read the unit template — it is the source of truth for these values"
# Resolve systemd's %h specifier ourselves; everything below has to agree with
# what systemd will actually exec.
EXEC_START="$(sed -n 's|^ExecStart=||p' "$UNIT_SRC" | tail -1 | sed "s|%h|$HOME|g")"
WORK_DIR="$(sed -n 's|^WorkingDirectory=||p' "$UNIT_SRC" | tail -1 | sed "s|%h|$HOME|g")"
[ -n "$EXEC_START" ] || fail "unit has no ExecStart"

BIN_PATH="${EXEC_START%% *}"
# The port is an ExecStart argument (`forge api --port N`), not an Environment=
# var, so parse it from the same string systemd will pass to the process.
PORT="$(sed -n 's|.*--port \([0-9]\{1,\}\).*|\1|p' <<<"$EXEC_START")"
[ -n "$PORT" ] || fail "unit's ExecStart declares no --port — refusing to guess which port to verify"
echo "    binary:   $BIN_PATH"
echo "    port:     $PORT"
echo "    database: $DB_PATH"
BASE="http://127.0.0.1:$PORT"

step "preflight"
# systemd reports a missing WorkingDirectory and a missing binary with the same
# nameless 'result: resources' failure, then crash-loops on it (Restart=always).
# Name which one is wrong instead.
[ -d "$WORK_DIR" ] || fail "WorkingDirectory does not exist: $WORK_DIR"
echo "    WorkingDirectory exists: $WORK_DIR"
[ -f "$DB_PATH" ] || echo "    note: $DB_PATH does not exist yet — it will be created empty"

step "build (CGO stays ON — forge's SQLite driver is cgo-only mattn/go-sqlite3)"
cd "$REPO_DIR"
go vet ./... || fail "go vet failed"
go test ./... || fail "go test failed"
STAGED="$REPO_DIR/bin/forge"
mkdir -p "$REPO_DIR/bin"
# Explicit -o: a bare `go build ./cmd/forge` drops the binary in the CWD.
go build -o "$STAGED" ./cmd/forge || fail "go build failed"
[ -x "$STAGED" ] || fail "go build produced no binary at $STAGED"

# Checked BEFORE the install, for the same reason the smokes are: an unidentifiable
# binary compiles perfectly and reads clean in the log, so installing first would put
# it in front of live sessions and only then tell us it cannot be traced to a commit.
echo "==> Checking provenance..."
buildinfo="$(go version -m "$STAGED")"
vcs_revision="$(printf '%s\n' "$buildinfo" | awk -F= '$1 ~ /[[:space:]]vcs\.revision$/ {{print $2}}')"
vcs_modified="$(printf '%s\n' "$buildinfo" | awk -F= '$1 ~ /[[:space:]]vcs\.modified$/ {{print $2}}')"
if [ -z "$vcs_revision" ]; then
    echo "    'go build' writes no VCS stamp when it cannot find a .git DIRECTORY, and it does" >&2
    echo "    not fail when that happens -- not even with -buildvcs=true. The usual cause is" >&2
    echo "    building from a git worktree, whose .git is a pointer file. Build from a real" >&2
    echo "    clone or checkout instead." >&2
    fail "refusing to install "$STAGED": no vcs.revision, so nothing ties it back to a commit"
fi
echo "    vcs.revision=$vcs_revision"
if [ "$vcs_modified" = "true" ]; then
    echo "    WARNING: built from a DIRTY tree (vcs.modified=true). $vcs_revision names the" >&2
    echo "    commit this binary was built NEAR, not the source it was built FROM, and that" >&2
    echo "    source is not recoverable from any commit. Commit first for a reproducible build." >&2
fi
echo "    built: $(ls -lh "$STAGED" | awk '{print $5}')"

step "boot-and-answer smoke on a throwaway HOME, before touching the live daemon"
# The binary has to prove it boots, migrates a database and serves every route
# BEFORE it gets installed. `go build` passing says nothing about any of that:
# a conflicting ServeMux pattern compiles and vets green, then panics at
# registration time and the process dies on its first breath.
./scripts/e2e-smoke.sh >/dev/null || fail "e2e smoke failed — not installing. Run ./scripts/e2e-smoke.sh to see why."
echo "    smoke passed"

step "install"
mkdir -p "$BACKUP_DIR" "$(dirname "$BIN_PATH")" "$UNIT_DIR"
# Backups are TIMESTAMPED, never a fixed filename. A verification step below can
# fail AFTER the new binary is installed — that is the whole point of it — and
# re-running this script would then back the NEW binary up over a fixed
# `forge.prev`, destroying the last known-good one. A rollback point that the
# next attempt overwrites is not a rollback point.
BACKUP=""
if [ -f "$BIN_PATH" ]; then
  BACKUP="$BACKUP_DIR/forge.$(date +%Y%m%d-%H%M%S)"
  cp -p "$BIN_PATH" "$BACKUP"
  echo "    previous binary backed up to $BACKUP"
fi
# cp over a running binary fails ETXTBSY. Stage alongside, then rename — which
# is atomic, so there is no window where $BIN_PATH is half-written.
cp "$STAGED" "$BIN_PATH.new"
chmod +x "$BIN_PATH.new"
mv -f "$BIN_PATH.new" "$BIN_PATH"
install -m 0644 "$UNIT_SRC" "$UNIT_DIR/$UNIT_NAME"
systemctl --user daemon-reload
echo "    installed $BIN_PATH and $UNIT_DIR/$UNIT_NAME"

step "restart $UNIT_NAME"
systemctl --user restart "$UNIT_NAME"

step "wait for it to answer on $BASE/api/forge/environments"
# Poll. forge opens SQLite and runs its schema migrations before it binds, and
# that is not a fixed cost — a sleep long enough today is a flake tomorrow.
READY=0
for _ in $(seq 1 60); do
  if ! systemctl --user is-active --quiet "$UNIT_NAME"; then
    systemctl --user status "$UNIT_NAME" --no-pager -l | tail -20 >&2
    fail "$UNIT_NAME is not active — it died on startup (see status above)"
  fi
  if curl -fsS -o /dev/null --max-time 2 "$BASE/api/forge/environments" 2>/dev/null; then READY=1; break; fi
  sleep 0.25
done
[ "$READY" = "1" ] || fail "$UNIT_NAME never answered on $BASE within ~15s"

step "verify the live service"
# `systemctl is-active` is to a deploy what `go build` is to a guard: it proves a
# process exists, not that the binary answers. Everything below asserts behaviour.

# 1. It opened the database we think it did. forge resolves its SQLite file from
#    $HOME with no override, so a wrong HOME under systemd would silently create
#    and serve a FRESH EMPTY database — which looks exactly like a healthy deploy.
MAINPID="$(systemctl --user show "$UNIT_NAME" -p MainPID --value)"
[ -n "$MAINPID" ] && [ "$MAINPID" != "0" ] || fail "unit reports no MainPID"
readlink -f /proc/"$MAINPID"/fd/* 2>/dev/null | grep -qxF "$DB_PATH" \
  || fail "pid $MAINPID does not have $DB_PATH open — it is serving some OTHER database"
echo "    pid $MAINPID has $DB_PATH open"

# 2. It is serving that database's real contents, not an empty schema.
ENVS="$(curl -fsS "$BASE/api/forge/environments")"
ENV_COUNT="$(jq 'length' <<<"$ENVS")"
[ "$ENV_COUNT" -gt 0 ] 2>/dev/null \
  || fail "/api/forge/environments returned $ENV_COUNT entries — the service is serving an EMPTY database"
echo "    /api/forge/environments: $ENV_COUNT slots"

# 3. The binary that answered is THIS tree's, not the one that was already
#    installed. A deploy that silently no-ops is the failure mode this whole
#    script exists to prevent, and it is invisible to every check above.
#    `declared` was added when /api/forge/routes stopped fabricating its values
#    (39e2f74); a binary without it is a stale one, and this fails loudly on it.
ROUTES="$(curl -fsS "$BASE/api/forge/routes")"
jq -e '.[0] | has("declared")' >/dev/null <<<"$ROUTES" \
  || fail "/api/forge/routes has no 'declared' field — a STALE binary is still serving on :$PORT"

# 4. And it is not inventing the values it reports. An undeclared prod var must
#    come back EMPTY; the whole point of 39e2f74 is that forge stops guessing.
#
#    Scoped to env=="prod" on purpose. `declared` is a systemd-unit concept: the
#    prod loop reads each service's unit and records whether the var was actually
#    in it. Staging (env-N) routes come from ~/forge/envs/env-N/docker-compose.yml
#    and are only emitted for vars found there, so they carry real values with
#    `declared` left at its zero value. Asserting this fleet-wide would flag all
#    26 of them as fabricated, which they are not.
PROD_COUNT="$(jq '[.[] | select(.env == "prod")] | length' <<<"$ROUTES")"
[ "$PROD_COUNT" -gt 0 ] 2>/dev/null || fail "/api/forge/routes reports no prod routes at all"
FABRICATED="$(jq '[.[] | select(.env == "prod" and .declared == false and .value != "")] | length' <<<"$ROUTES")"
[ "$FABRICATED" = "0" ] \
  || fail "/api/forge/routes reports a value for $FABRICATED prod route(s) it never read from a unit file — the fabrication bug is live again"
# A prod route must also name the unit it consulted. An empty Source means the
# read path stopped consulting unit files at all, which is how the fabrication
# bug looked from the outside in the first place.
NO_SOURCE="$(jq '[.[] | select(.env == "prod" and .source == "")] | length' <<<"$ROUTES")"
[ "$NO_SOURCE" = "0" ] || fail "$NO_SOURCE prod route(s) name no source unit file"
DECLARED="$(jq '[.[] | select(.env == "prod" and .declared)] | length' <<<"$ROUTES")"
echo "    /api/forge/routes: $(jq 'length' <<<"$ROUTES") routes ($PROD_COUNT prod, $DECLARED declared by a unit, 0 fabricated)"

# The vhost that publishes the preview slots forge hands out: N.dev.kayushkin.com
# -> 127.0.0.1:9N00, with the WebSocket upgrade a live-reloading preview needs.
# Slots with nothing running answer 502, which is their normal idle state and not
# a deploy failure — install.sh asserts no REGRESSION per server_name, so an idle
# slot does not fail this deploy while a slot that stops answering does.
echo "==> Installing nginx vhost..."
"$REPO_DIR/deploy/nginx/install.sh"

printf '\n==> DEPLOYED — forge %s is live on %s\n' "$(git -C "$REPO_DIR" rev-parse --short HEAD 2>/dev/null || echo '(no git)')" "$BASE"
# `[ -n "$BACKUP" ] && echo …` as the last line would exit 1 under `set -e` on a
# first-ever install, failing a deploy that fully succeeded.
if [ -n "$BACKUP" ]; then
  echo "    rollback: cp $BACKUP $BIN_PATH && systemctl --user restart $UNIT_NAME"
fi
