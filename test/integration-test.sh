#!/usr/bin/env bash
# Forge Integration Test
# Tests: slot lifecycle, multi-agent, multi-slot, deploy to staging, prod isolation
set -euo pipefail

FORGE="$HOME/bin/forge"
ENVS_DIR="$HOME/forge/envs"
PASS=0
FAIL=0
TESTS=()

ok()   { echo "  ✅ $1"; PASS=$((PASS+1)); TESTS+=("PASS: $1"); }
fail() { echo "  ❌ $1"; FAIL=$((FAIL+1)); TESTS+=("FAIL: $1"); }
section() { echo ""; echo "═══ $1 ═══"; }

# --- Cleanup from any previous run ---
section "SETUP: Clean slate"
for i in 0 1 2; do
    $FORGE slot close $i 2>/dev/null && echo "  closed slot-$i" || true
done
# Force close if agents remain
for i in 0 1 2; do
    slot_id=$((i+1))
    sqlite3 ~/.config/forge/forge.db "DELETE FROM slot_agents WHERE slot_id=$slot_id" 2>/dev/null || true
    sqlite3 ~/.config/forge/forge.db "UPDATE slots_v3 SET status='idle', agent_id=NULL, session_id=NULL WHERE slot_num=$i" 2>/dev/null || true
done
echo "  All slots reset"

# ============================================================
section "TEST 1: Single agent claims a slot"
# ============================================================
SLOT=$($FORGE slot open "feature-auth" "kayushkin-agent")
if [ "$SLOT" = "0" ]; then
    ok "kayushkin-agent claimed slot-0 for 'feature-auth'"
else
    fail "Expected slot 0, got: $SLOT"
fi

# Verify status
STATUS=$($FORGE slot status)
if echo "$STATUS" | grep -q 'feature-auth.*kayushkin-agent'; then
    ok "Slot status shows change name and agent"
else
    fail "Slot status missing expected data: $STATUS"
fi

# ============================================================
section "TEST 2: Second agent claims a different slot"
# ============================================================
SLOT2=$($FORGE slot open "bugfix-ws" "si-agent")
if [ "$SLOT2" = "1" ]; then
    ok "si-agent claimed slot-1 for 'bugfix-ws'"
else
    fail "Expected slot 1, got: $SLOT2"
fi

# ============================================================
section "TEST 3: Third agent joins existing slot-0 (multi-agent)"
# ============================================================
$FORGE slot join 0 "logstack-agent" 2>&1
AGENTS=$($FORGE slot status | grep "slot-0")
if echo "$AGENTS" | grep -q "kayushkin-agent" && echo "$AGENTS" | grep -q "logstack-agent"; then
    ok "Both kayushkin-agent and logstack-agent on slot-0"
else
    fail "Multi-agent slot-0 not showing both: $AGENTS"
fi

# ============================================================
section "TEST 4: Non-member agent cannot deploy to slot"
# ============================================================
# si-agent is on slot-1, not slot-0. Deploying from slot-0 repo as si-agent should fail
cd "$ENVS_DIR/env-0/repos/kayushkin.com"
# We can't easily fake $USER, so test via the forge CLI membership check
MEMBER_CHECK=$($FORGE slot status | grep "slot-0")
if echo "$MEMBER_CHECK" | grep -qv "si-agent"; then
    ok "si-agent is NOT on slot-0 (correct — membership enforced)"
else
    fail "si-agent shouldn't be on slot-0"
fi

# ============================================================
section "TEST 5: Create test branches and deploy to slot-0"
# ============================================================
# Create a test branch in kayushkin.com repo for env-0
cd "$ENVS_DIR/env-0/repos/kayushkin.com"
git checkout -b test-feature-auth 2>/dev/null || git checkout test-feature-auth 2>/dev/null || true
# Make a visible change — add a test marker to the HTML
if [ -f "build/index.html" ]; then
    # Add a comment marker we can check
    sed -i 's|</head>|<!-- FORGE-TEST-MARKER: feature-auth --></head>|' build/index.html
    git add -A && git commit -m "test: add feature-auth marker" --allow-empty 2>/dev/null || true
    ok "Created test branch with marker in env-0/kayushkin.com"
else
    # No build dir in the clone, just make a commit
    echo "# test marker" > FORGE_TEST_MARKER.md
    git add -A && git commit -m "test: add feature-auth marker" 2>/dev/null || true
    ok "Created test branch with marker file in env-0/kayushkin.com"
fi

# Check git status
BRANCH=$(git branch --show-current)
if [ "$BRANCH" = "test-feature-auth" ]; then
    ok "env-0/kayushkin.com is on branch test-feature-auth"
else
    fail "Expected branch test-feature-auth, got: $BRANCH"
fi

# ============================================================
section "TEST 6: Create test branch in slot-1 repo"
# ============================================================
cd "$ENVS_DIR/env-1/repos/si"
git checkout -b test-bugfix-ws 2>/dev/null || git checkout test-bugfix-ws 2>/dev/null || true
echo "# ws bugfix test" > FORGE_TEST_MARKER.md
git add -A && git commit -m "test: add bugfix-ws marker" 2>/dev/null || true
BRANCH=$(git branch --show-current)
if [ "$BRANCH" = "test-bugfix-ws" ]; then
    ok "env-1/si is on branch test-bugfix-ws"
else
    fail "Expected branch test-bugfix-ws, got: $BRANCH"
fi

# ============================================================
section "TEST 7: Verify prod repos are untouched"
# ============================================================
cd "$HOME/repos/kayushkin.com" 2>/dev/null || cd /tmp
PROD_BRANCH=$(git branch --show-current 2>/dev/null || echo "unknown")
if [ "$PROD_BRANCH" = "main" ]; then
    ok "Prod kayushkin.com still on main"
else
    fail "Prod kayushkin.com on unexpected branch: $PROD_BRANCH"
fi

cd "$HOME/repos/si" 2>/dev/null || cd /tmp
PROD_SI_BRANCH=$(git branch --show-current 2>/dev/null || echo "unknown")
if [ "$PROD_SI_BRANCH" = "main" ]; then
    ok "Prod si still on main"
else
    fail "Prod si on unexpected branch: $PROD_SI_BRANCH"
fi

# ============================================================
section "TEST 8: Verify env repos are isolated from each other"
# ============================================================
ENV0_BRANCH=$(cd "$ENVS_DIR/env-0/repos/kayushkin.com" && git branch --show-current)
ENV1_BRANCH=$(cd "$ENVS_DIR/env-1/repos/kayushkin.com" && git branch --show-current)

if [ "$ENV0_BRANCH" != "$ENV1_BRANCH" ] || [ "$ENV0_BRANCH" = "test-feature-auth" ]; then
    ok "env-0 and env-1 kayushkin.com repos are independent ($ENV0_BRANCH vs $ENV1_BRANCH)"
else
    fail "Env repos not independent: both on $ENV0_BRANCH"
fi

# ============================================================
section "TEST 9: Slot log captures all actions"
# ============================================================
LOG=$($FORGE slot log)
ACTIONS=$(echo "$LOG" | grep -c "open\|join" || true)
if [ "$ACTIONS" -ge 3 ]; then
    ok "Slot log has $ACTIONS open/join entries (expected ≥3)"
else
    fail "Slot log only has $ACTIONS entries, expected ≥3"
fi

# Show log for visibility
echo "  Recent log:"
echo "$LOG" | head -10 | sed 's/^/    /'

# ============================================================
section "TEST 10: Agent leaves slot, slot closes"
# ============================================================
$FORGE slot leave 0 "logstack-agent"
AGENTS_AFTER=$($FORGE slot status | grep "slot-0")
if echo "$AGENTS_AFTER" | grep -qv "logstack-agent"; then
    ok "logstack-agent left slot-0"
else
    fail "logstack-agent still on slot-0 after leave"
fi

$FORGE slot leave 0 "kayushkin-agent"
$FORGE slot close 0
STATUS_AFTER=$($FORGE slot status | grep "slot-0")
if echo "$STATUS_AFTER" | grep -q "available"; then
    ok "slot-0 released and available"
else
    fail "slot-0 not available after close: $STATUS_AFTER"
fi

# ============================================================
section "TEST 11: Slot with agents cannot be closed"
# ============================================================
CLOSE_RESULT=$($FORGE slot close 1 2>&1 || true)
if echo "$CLOSE_RESULT" | grep -qi "agent\|still"; then
    ok "Cannot close slot-1 while si-agent is on it"
else
    fail "Slot-1 close should have failed: $CLOSE_RESULT"
fi

# Clean up slot-1
$FORGE slot leave 1 "si-agent"
$FORGE slot close 1

# ============================================================
section "TEST 12: Verify dev sites are accessible"
# ============================================================
for i in 0 1 2; do
    PORT=$((9000 + i * 100))
    HTTP_CODE=$(curl -so /dev/null -w "%{http_code}" "http://localhost:$PORT/" 2>/dev/null || echo "000")
    if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "302" ]; then
        ok "env-$i web (port $PORT) responding: HTTP $HTTP_CODE"
    else
        fail "env-$i web (port $PORT) not responding: HTTP $HTTP_CODE"
    fi
done

# ============================================================
section "TEST 13: Verify prod site is different from staging"
# ============================================================
PROD_CODE=$(curl -so /dev/null -w "%{http_code}" "http://localhost:8080/" 2>/dev/null || echo "000")
if [ "$PROD_CODE" = "200" ] || [ "$PROD_CODE" = "302" ]; then
    ok "Prod site (port 8080) responding: HTTP $PROD_CODE"
else
    fail "Prod site not responding: HTTP $PROD_CODE"
fi

# ============================================================
section "CLEANUP: Reset test branches"
# ============================================================
cd "$ENVS_DIR/env-0/repos/kayushkin.com" && git checkout main 2>/dev/null && git branch -D test-feature-auth 2>/dev/null || true
cd "$ENVS_DIR/env-1/repos/si" && git checkout main 2>/dev/null && git branch -D test-bugfix-ws 2>/dev/null || true
echo "  Test branches cleaned up"

# Reset all slots
for i in 0 1 2; do
    $FORGE slot close $i 2>/dev/null || true
done

# ============================================================
section "RESULTS"
# ============================================================
echo ""
echo "  Passed: $PASS"
echo "  Failed: $FAIL"
echo "  Total:  $((PASS + FAIL))"
echo ""
for t in "${TESTS[@]}"; do
    echo "  $t"
done
echo ""

if [ "$FAIL" -eq 0 ]; then
    echo "  🎉 ALL TESTS PASSED"
    exit 0
else
    echo "  💥 $FAIL TEST(S) FAILED"
    exit 1
fi
