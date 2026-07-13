#!/usr/bin/env bash
#
# Install this repo's nginx vhost(s) from deploy/nginx/*.conf and reload nginx.
#
# The vhost is not decoration: it carries the SSE, WebSocket-upgrade and cache
# rules the app needs to function. Until this script existed those rules lived
# only as hand-edits in /etc/nginx, tracked by no repo — so rebuilding this box
# would restore the app but not the config that makes its event stream work.
#
# Layout: `sites-enabled/<host>` had drifted into a REAL FILE on this host while
# `sites-available/<host>` held a months-old fossil, so editing sites-available
# changed nothing that served. This script restores the Debian layout —
# sites-available holds the file, sites-enabled is a symlink to it — so the two
# directories cannot silently disagree again.
#
# Safe to run on its own (it touches nothing but nginx), and safe to re-run: a
# vhost already matching the repo is skipped without a reload.
#
# This file is IDENTICAL in every repo that ships a vhost (dash, llmux,
# kayushkin.com, argraphments, inber-party, multichat, forge). It is the one
# place the install and rollback rules live; copy it whole, do not fork it.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AVAILABLE_DIR=/etc/nginx/sites-available
ENABLED_DIR=/etc/nginx/sites-enabled
BACKUP_DIR="$HOME/.local/share/nginx-vhost-backup"
STAMP="$(date +%Y%m%dT%H%M%S)"

shopt -s nullglob
CONFS=("$SCRIPT_DIR"/*.conf)
shopt -u nullglob
if [ ${#CONFS[@]} -eq 0 ]; then
  echo "error: no *.conf next to $0 — nothing to install." >&2
  exit 1
fi

# The names a vhost answers for come from the CONFIG, never from the filename.
# dash's vhost is named for the single host it serves, while forge-dev.conf
# serves 0/1/2.dev.kayushkin.com; assuming filename == hostname works for the
# first and silently probes a hostname that does not exist for the second.
server_names_of() {
  grep -hoE '^[[:space:]]*server_name[[:space:]]+[^;]+;' "$1" \
    | sed -E 's/^[[:space:]]*server_name[[:space:]]+//; s/;[[:space:]]*$//' \
    | tr -s '[:space:]' '\n' | grep -v '^_\?$' | sort -u
}

# https if any listen directive in this vhost carries `ssl`, else http. A vhost
# with no TLS listener (forge's dev previews) is not reachable over 443 at all,
# so probing it there would report a failure that says nothing about this config.
scheme_of() {
  if grep -qE '^[[:space:]]*listen[[:space:]]+[^;]*\bssl\b' "$1"; then
    echo https
  else
    echo http
  fi
}

# HTTP status this box returns for <host>, or 000 if nginx did not answer at
# all. --resolve pins it to 127.0.0.1 so the check cannot depend on public DNS.
probe() {
  local scheme="$1" host="$2" port=80
  [ "$scheme" = https ] && port=443
  curl -sk -o /dev/null -w '%{http_code}' --max-time 10 \
    --resolve "$host:$port:127.0.0.1" "$scheme://$host/" 2>/dev/null || echo 000
}

# Workers currently ACCEPTING connections. nginx prints `nginx: worker process`
# for those and `nginx: worker process is shutting down` for ones that are
# draining — the latter have already closed their listening socket and can no
# longer answer anything new.
accepting_workers() {
  ps -eo pid=,args= \
    | awk '$2=="nginx:" && $3=="worker" && $4=="process" && NF==4 {print $1}' \
    | sort -n
}

# `systemctl reload` returns when the SIGHUP has been DELIVERED, not when nginx
# has re-read its config. Measured on this box: when it returns, every worker is
# still an old one, and workers from the new config appear ~200ms later. A probe
# fired in that window is answered by the OLD workers running the OLD config —
# so a vhost that breaks the site passes its own verification, and the good
# config that replaces it gets blamed for the broken one it just evicted. (Both
# happened here, one run apart, which is how this was found.)
#
# So wait until every ACCEPTING worker is one that did not exist before the
# reload. Deliberately NOT "wait for the old workers to exit": a worker draining
# a long-lived connection stays alive for as long as that connection does, and
# one on this box had been shutting down for 35 minutes holding an SSE stream.
reload_nginx() {
  local before pid all_new
  before=" $(accepting_workers | tr '\n' ' ') "
  sudo systemctl reload nginx
  for _ in $(seq 1 100); do
    all_new=1
    local -a now=()
    mapfile -t now < <(accepting_workers)
    if [ ${#now[@]} -eq 0 ]; then
      all_new=0
    else
      for pid in "${now[@]}"; do
        case "$before" in *" $pid "*) all_new=0; break ;; esac
      done
    fi
    [ "$all_new" = 1 ] && return 0
    sleep 0.2
  done
  return 1
}

# Preflight. nginx -t validates the WHOLE config, so if it is already failing
# for reasons that have nothing to do with us, our own post-install test would
# fail too and we would roll back a good vhost while blaming ourselves. Refuse
# to touch anything until the config we are about to modify is known-good.
if ! sudo nginx -t 2>/dev/null; then
  echo "error: 'nginx -t' fails BEFORE this script changed anything." >&2
  echo "       Someone else's config is broken; fix that first. Not touching nginx." >&2
  sudo nginx -t || true
  exit 1
fi

# Baseline, captured BEFORE anything changes. What this script promises is NO
# REGRESSION, not absolute health: forge's 1.dev/2.dev preview slots answer 502
# whenever no preview happens to be running, which is their normal steady state
# and no business of the vhost's. A flat "must be < 500" would fail that deploy
# every night, and a guard that cries wolf is a guard people switch off. So a
# host that served before must still serve after, while a host that was already
# failing is only required to still be ANSWERED by nginx.
declare -A BASELINE=()
for conf in "${CONFS[@]}"; do
  host="$(basename "$conf" .conf)"
  scheme="$(scheme_of "$conf")"
  # Only meaningful if this vhost was already installed. With no vhost, the
  # probe is answered by the DEFAULT server (404) — recording that as "it used
  # to work" would then demand < 500 from a preview slot with nothing behind it.
  if [ -e "$ENABLED_DIR/$host" ] || [ -e "$ENABLED_DIR/$host.conf" ]; then
    while read -r name; do
      [ -n "$name" ] || continue
      BASELINE["$name"]="$(probe "$scheme" "$name")"
    done < <(server_names_of "$conf")
  fi
done

changed=0
declare -a RESTORE_HOSTS=()

# Put <host> back exactly as it was — including whether sites-enabled/<host> was
# a regular file or a symlink, which differs per host on this box, and including
# a legacy `<host>.conf` entry if that is what had been serving.
restore_host() {
  local host="$1" bak="$BACKUP_DIR/$STAMP/$host"
  if [ -f "$bak/available" ]; then
    sudo install -m644 "$bak/available" "$AVAILABLE_DIR/$host"
  else
    sudo rm -f "$AVAILABLE_DIR/$host"
  fi
  sudo rm -f "$ENABLED_DIR/$host"
  if [ -f "$bak/enabled.symlink-target" ]; then
    sudo ln -sfn "$(cat "$bak/enabled.symlink-target")" "$ENABLED_DIR/$host"
  elif [ -f "$bak/enabled.file" ]; then
    sudo install -m644 "$bak/enabled.file" "$ENABLED_DIR/$host"
  fi
  if [ -f "$bak/enabled.legacy" ]; then
    sudo install -m644 "$bak/enabled.legacy" "$ENABLED_DIR/$host.conf"
  fi
  return 0
}

for conf in "${CONFS[@]}"; do
  host="$(basename "$conf" .conf)"

  # Already current? Then a reload would be pure risk for no change.
  if sudo cmp -s "$conf" "$AVAILABLE_DIR/$host" 2>/dev/null \
     && [ "$(readlink -f "$ENABLED_DIR/$host" 2>/dev/null)" = "$AVAILABLE_DIR/$host" ] \
     && [ ! -e "$ENABLED_DIR/$host.conf" ]; then
    echo "    $host: already current"
    continue
  fi

  echo "==> Installing vhost: $host"
  bak="$BACKUP_DIR/$STAMP/$host"
  mkdir -p "$bak"
  # Timestamped, never overwritten: a fixed-filename backup is not a rollback
  # point, because a second run would clobber it with the bad copy.
  [ -e "$AVAILABLE_DIR/$host" ] && sudo cp "$AVAILABLE_DIR/$host" "$bak/available"
  if [ -L "$ENABLED_DIR/$host" ]; then
    readlink "$ENABLED_DIR/$host" > "$bak/enabled.symlink-target"
  elif [ -f "$ENABLED_DIR/$host" ]; then
    sudo cp "$ENABLED_DIR/$host" "$bak/enabled.file"
  fi

  # `include sites-enabled/*` loads EVERY name in that directory, so a vhost
  # that used to be enabled as `<host>.conf` would keep serving alongside the
  # one we install as `<host>` — two server blocks for one server_name, and
  # which of them wins depends on sort order. Collapse the old name into the new
  # one, the same way the sites-available/sites-enabled split is collapsed above.
  if [ -f "$ENABLED_DIR/$host.conf" ] && [ ! -L "$ENABLED_DIR/$host.conf" ]; then
    echo "    superseding legacy entry: sites-enabled/$host.conf -> sites-enabled/$host"
    sudo cp "$ENABLED_DIR/$host.conf" "$bak/enabled.legacy"
    sudo rm -f "$ENABLED_DIR/$host.conf"
  fi
  sudo chown -R "$(id -u):$(id -g)" "$bak"

  sudo install -m644 "$conf" "$AVAILABLE_DIR/$host"
  sudo ln -sfn "$AVAILABLE_DIR/$host" "$ENABLED_DIR/$host"
  RESTORE_HOSTS+=("$host")
  changed=1
done

if [ "$changed" -eq 0 ]; then
  echo "==> nginx vhosts already up to date; no reload needed."
  exit 0
fi

rollback() {
  for host in "${RESTORE_HOSTS[@]}"; do restore_host "$host"; done
  if ! sudo nginx -t 2>/dev/null; then
    echo "    ✗ ROLLBACK DID NOT RESTORE A VALID CONFIG. Backups: $BACKUP_DIR/$STAMP" >&2
    exit 1
  fi
}

if ! sudo nginx -t 2>/dev/null; then
  echo "error: 'nginx -t' FAILED with the new vhost(s) — rolling back." >&2
  sudo nginx -t || true
  rollback
  echo "    rolled back; nginx config is valid again and was never reloaded." >&2
  exit 1
fi

echo "==> Reloading nginx (reload, not restart — drops no connections)..."
if ! reload_nginx; then
  echo "error: nginx never picked up the new config after reload. Backups: $BACKUP_DIR/$STAMP" >&2
  exit 1
fi
if ! systemctl is-active --quiet nginx; then
  echo "error: nginx is not active after reload. Backups: $BACKUP_DIR/$STAMP" >&2
  exit 1
fi

# A config that parses is not a config that serves. Verify, and roll the reload
# back if it does not — an invalid config never reaches nginx, but a VALID one
# that points at the wrong upstream reaches it just fine.
verify_failed=""
# Every server_name in the config nginx actually SERVES. Read with the same
# parser used on the repo's confs and compared as whole names: a substring match
# would accept `argraphments.com` on the strength of `www.argraphments.com`, and
# hand-rolling a second regex here is how the two got to disagree in the first
# place.
SERVED_DUMP="$(mktemp)"
trap 'rm -f "$SERVED_DUMP"' EXIT
sudo nginx -T 2>/dev/null > "$SERVED_DUMP"
SERVED_NAMES="$(server_names_of "$SERVED_DUMP")"

for conf in "${CONFS[@]}"; do
  host="$(basename "$conf" .conf)"
  scheme="$(scheme_of "$conf")"
  while read -r name; do
    [ -n "$name" ] || continue

    # Is the vhost actually in the config nginx SERVES? `nginx -T` dumps the
    # fully-resolved config, so this catches a file that landed in
    # sites-available but was never enabled — which curl alone cannot tell apart
    # from the default server cheerfully answering in its place.
    if ! grep -qxF "$name" <<<"$SERVED_NAMES"; then
      echo "error: $name is not in the config nginx serves — vhost not enabled?" >&2
      verify_failed=1
      continue
    fi

    code="$(probe "$scheme" "$name")"
    if [ "$code" = "000" ]; then
      echo "error: nginx does not answer for $name at all over $scheme" >&2
      verify_failed=1
      continue
    fi
    before="${BASELINE[$name]:-}"
    if [ -n "$before" ] && [ "$before" != "000" ] && [ "$before" -lt 500 ] && [ "$code" -ge 500 ]; then
      echo "error: $name REGRESSED — served HTTP $before before this change, HTTP $code now" >&2
      verify_failed=1
      continue
    fi
    if [ -n "$before" ] && [ "$before" != "$code" ]; then
      echo "    $name serves (HTTP $code; was $before — not a regression)"
    else
      echo "    $name serves (HTTP $code)"
    fi
  done < <(server_names_of "$conf")
done

if [ -n "$verify_failed" ]; then
  echo "error: vhost(s) do not serve after reload — rolling back and reloading." >&2
  rollback
  reload_nginx || echo "    ✗ nginx did not pick up the rolled-back config. Backups: $BACKUP_DIR/$STAMP" >&2
  echo "    rolled back to the previous vhost(s) and reloaded. Backups: $BACKUP_DIR/$STAMP" >&2
  exit 1
fi

echo "==> nginx vhost(s) installed. Backups: $BACKUP_DIR/$STAMP"
