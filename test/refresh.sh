#!/bin/bash
# signals-refresh: when Signals asks Claude Code for a fresh plan reading, and
# how. Runs in a throwaway $HOME with a stand-in claude that records its calls.
#   ./test/refresh.sh   (or ./test/run.sh for everything)
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
REFRESH="$ROOT/libexec/signals-refresh"
METER="$ROOT/libexec/signals-meter"
export HOME="$(mktemp -d)"
unset SIGNALS_NO_REFRESH SIGNALS_FRESH CLAUDE_CONFIG_DIR
export SIGNALS_NO_APP=1 SIGNALS_NO_LEDGER=1 SIGNALS_NO_NOTIFY=1
export CLAUDECODE=1 CLAUDE_CODE_ENTRYPOINT=cli   # as if run from inside a Claude Code session
trap 'rm -rf "$HOME"' EXIT

pass=0 fail=0
check() { # name, expected, actual
  if [ "$2" = "$3" ]; then pass=$((pass+1)); printf '  \033[32m✓\033[0m %s\n' "$1"
  else fail=$((fail+1)); printf '  \033[31m✗\033[0m %s\n      expected: %s\n      actual:   %s\n' "$1" "$2" "$3"; fi
}

# A stand-in claude: records how it was run, then saves a reading the way
# Claude Code does (or fails, or hangs, as $HOME/fake.mode says).
FAKE="$HOME/fakebin/claude"; mkdir -p "$HOME/fakebin"
cat > "$FAKE" <<'SH'
#!/bin/bash
echo "$*" >> "$HOME/claude.calls"
env > "$HOME/claude.env"
pwd > "$HOME/claude.cwd"
case "$(cat "$HOME/fake.mode" 2>/dev/null)" in
  fail) echo '{"is_error":true}'; exit 1 ;;
  hang) exec sleep 30 ;;
esac
pct=$(cat "$HOME/fake.pct" 2>/dev/null || echo 42)
[ -f "$HOME/.claude.json" ] || echo "{}" > "$HOME/.claude.json"
jq --argjson t "$(($(date +%s) * 1000))" --argjson p "$pct" \
  '.cachedUsageUtilization = {fetchedAtMs: $t, utilization: {
     five_hour: {utilization: $p, resets_at: "2099-01-01T00:00:00.5+00:00"},
     seven_day: {utilization: 10, resets_at: "2099-01-04T00:00:00.5+00:00"}}}' \
  "$HOME/.claude.json" > "$HOME/.claude.json.tmp" && mv "$HOME/.claude.json.tmp" "$HOME/.claude.json"
echo '{"is_error":false,"result":"Current session: '"$pct"'% used"}'
SH
chmod +x "$FAKE"
export SIGNALS_CLAUDE="$FAKE"

iso() { date -u -r "$1" +%Y-%m-%dT%H:%M:%S.000000+00:00; }
reading() { # age-seconds [5-hour reset epoch]
  local t=$(( $(date +%s) - $1 )) r5=${2:-4070908800}
  jq -n --argjson t "$((t * 1000))" --arg r5 "$(iso "$r5")" --arg r7 "$(iso 4071168000)" \
    '{cachedUsageUtilization: {fetchedAtMs: $t, utilization: {
       five_hour: {utilization: 7, resets_at: $r5}, seven_day: {utilization: 3, resets_at: $r7}}}}' > "$HOME/.claude.json"
}
P="$HOME/.claude/projects/-code-acme-api"; mkdir -p "$P"; T="$P/s1.jsonl"; : > "$T"
transcript_at() { touch -t "$(date -r "$(( $(date +%s) - $1 ))" +%Y%m%d%H%M.%S)" "$T"; }  # written N seconds ago
reset_state() { rm -f "$HOME/.signals/usage/refresh.json" "$HOME/claude.calls" "$HOME/fake.mode"; rmdir "$HOME/.signals/usage/refresh.lock" 2>/dev/null; }
calls() { [ -f "$HOME/claude.calls" ] && wc -l < "$HOME/claude.calls" | tr -d ' ' || echo 0; }
pct() { jq -r '.cachedUsageUtilization.utilization.five_hour.utilization' "$HOME/.claude.json"; }

echo "When it asks"
reset_state; reading 30; transcript_at 5
check "a reading under a minute old: not yet" "fresh (Ns old)" "$("$REFRESH" | sed -E "s/3[0-2]s/Ns/")"
check "  ...and Claude Code isn't run" 0 "$(calls)"

reset_state; reading 120; transcript_at 600
check "nothing written since the reading: idle" "idle (reading Ns old)" "$("$REFRESH" | sed -E "s/12[0-2]s/Ns/")"
check "  ...and Claude Code isn't run" 0 "$(calls)"

reset_state; reading 120; transcript_at 5
check "a transcript written since the reading: asks" "refreshed (working)" "$("$REFRESH")"
check "  ...runs Claude Code once" 1 "$(calls)"
check "  ...and the new reading is saved" 42 "$(pct)"
check "  ...right after, it's fresh again" fresh "$("$REFRESH" | grep -oE '^fresh \([0-5]s old\)' | cut -d' ' -f1)"
check "  ...so Claude Code isn't run twice" 1 "$(calls)"

reset_state; reading 1000; transcript_at 2000
check "a reading over 15 minutes old: asks (other devices count too)" "refreshed (old)" "$("$REFRESH")"

reset_state; rm -f "$HOME/.claude.json"; transcript_at 2000
check "no reading at all: asks" "refreshed (old)" "$("$REFRESH")"

reset_state; reading 300 "$(( $(date +%s) - 10 ))"; transcript_at 2000
check "the 5-hour window reset since the reading: asks" "refreshed (reset)" "$("$REFRESH")"

reset_state; reading 120; transcript_at 600
check "the usage menu was opened: asks" "refreshed (menu)" "$(SIGNALS_FRESH=1 "$REFRESH")"

reset_state; reading 120; transcript_at 5
check "SIGNALS_NO_REFRESH turns it off" "off" "$(SIGNALS_NO_REFRESH=1 "$REFRESH")"
check "no Claude Code installed: does nothing" "no Claude Code" "$(SIGNALS_CLAUDE="$HOME/nope" "$REFRESH")"
check "  ...and nothing was run" 0 "$(calls)"

echo
echo "How it asks"
reset_state; reading 120; transcript_at 5; "$REFRESH" >/dev/null
args=$(cat "$HOME/claude.calls")
check "runs /usage headless" yes "$(grep -q -- '-p /usage' <<<"$args" && echo yes)"
check "  ...saves no session" yes "$(grep -q -- '--no-session-persistence' <<<"$args" && echo yes)"
check "  ...runs no hooks (no session lights)" yes "$(grep -q 'disableAllHooks":true' <<<"$args" && echo yes)"
check "  ...starts no MCP servers" yes "$(grep -q -- '--strict-mcp-config' <<<"$args" && echo yes)"
check "  ...in a clean environment (nothing from the session it runs inside)" 0 "$(grep -c '^CLAUDE' "$HOME/claude.env")"
check "  ...with HOME kept (so Claude Code finds its login)" "$HOME" "$(sed -n 's/^HOME=//p' "$HOME/claude.env")"
check "  ...from the Signals folder, not a project" "$(cd "$HOME/.signals/usage" && pwd -P)" "$(cat "$HOME/claude.cwd")"
check "  ...and nothing is left behind" "" "$(ls "$HOME/.signals/usage" | grep -v -e '^refresh.json$' -e '^refresh.stamp$')"

reset_state; reading 120; transcript_at 5
CLAUDE_CONFIG_DIR="$HOME/.claude" "$REFRESH" >/dev/null
check "CLAUDE_CONFIG_DIR is passed on" "$HOME/.claude" "$(sed -n 's/^CLAUDE_CONFIG_DIR=//p' "$HOME/claude.env")"

echo
echo "When it goes wrong"
reset_state; reading 120; transcript_at 5; echo fail > "$HOME/fake.mode"
check "Claude Code fails: says so" "failed (working, exit 1)" "$("$REFRESH")"
check "  ...and counts the failure" 1 "$(jq .fails "$HOME/.signals/usage/refresh.json")"
check "  ...then waits before asking again" "waiting (asked 0s ago)" "$("$REFRESH" | sed 's/[0-9]*s ago/0s ago/')"
check "  ...so it isn't run again yet" 1 "$(calls)"
jq '.last -= 130' "$HOME/.signals/usage/refresh.json" > "$HOME/r.tmp" && mv "$HOME/r.tmp" "$HOME/.signals/usage/refresh.json"
rm -f "$HOME/fake.mode"
check "  ...two minutes later it tries again, and recovers" "refreshed (working)" "$("$REFRESH")"
check "  ...which clears the failures" 0 "$(jq .fails "$HOME/.signals/usage/refresh.json")"

reset_state; reading 120; transcript_at 5
jq -n --argjson last "$(( $(date +%s) - 1000 ))" '{last: $last, fails: 6}' > "$HOME/.signals/usage/refresh.json"
check "many failures: waits up to 30 minutes" "waiting (asked Ns ago)" "$("$REFRESH" | sed -E "s/100[0-2]s/Ns/")"

reset_state; reading 120; transcript_at 5; echo hang > "$HOME/fake.mode"
start=$(date +%s)
out=$(SIGNALS_REFRESH_TIMEOUT=2 "$REFRESH")
check "Claude Code hangs: stopped after the timeout" yes "$([ $(( $(date +%s) - start )) -lt 10 ] && echo yes)"
check "  ...and counted as a failure" 1 "$(jq .fails "$HOME/.signals/usage/refresh.json")"
check "  ...with the lock let go" no "$([ -d "$HOME/.signals/usage/refresh.lock" ] && echo yes || echo no)"

reset_state; reading 120; transcript_at 5; mkdir -p "$HOME/.signals/usage/refresh.lock"
check "another refresh running: leaves it to that one" busy "$("$REFRESH")"
touch -t "$(date -r "$(( $(date +%s) - 120 ))" +%Y%m%d%H%M.%S)" "$HOME/.signals/usage/refresh.lock"
check "  ...unless its lock is over a minute old" "refreshed (working)" "$("$REFRESH")"

echo
echo "The meter uses it"
reset_state; reading 120; transcript_at 5; echo 64 > "$HOME/fake.pct"
mkdir -p "$HOME/.signals/usage"; echo '{"mode":"plan","plan":{"id":"max","label":"Claude Max 5x"}}' > "$HOME/.signals/usage/config.json"
SIGNALS_DETECTED_PLAN=max "$METER" >/dev/null 2>&1
check "on a plan, the meter refreshes first and shows the new figure" 64 "$(jq -r '.account.session.pct' "$HOME/.signals/usage/state.json")"
check "  ...from a reading taken just now" yes "$([ $(( $(date +%s) - $(jq -r '.account.as_of' "$HOME/.signals/usage/state.json") )) -lt 10 ] && echo yes)"
reset_state; reading 120; transcript_at 5
SIGNALS_NO_REFRESH=1 SIGNALS_DETECTED_PLAN=max "$METER" >/dev/null 2>&1
check "  ...not when refresh is off" 0 "$(calls)"
reset_state; reading 120; transcript_at 5
echo '{"mode":"estimate","budget":100}' > "$HOME/.signals/usage/config.json"
"$METER" >/dev/null 2>&1
check "pay-as-you-go modes never run it" 0 "$(calls)"

echo
echo "$pass passed, $fail failed"
[ "$fail" = 0 ]
