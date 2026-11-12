#!/bin/bash
# The usage ledger end to end: the engine (signals-core) reads Claude Code and
# Codex logs, the meter adds the month by tool and Codex sessions, and the
# signals command shows them. Runs in a throwaway $HOME with fixture logs whose
# costs are worked out by hand. Skipped when Go isn't installed.
#   ./test/ledger.sh   (or ./test/run.sh for everything)
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CLI="$ROOT/bin/signals"
METER="$ROOT/libexec/signals-meter"
command -v go >/dev/null || { echo "  (skipped: needs Go to build the engine)"; exit 0; }
export HOME="$(mktemp -d)"
export SIGNALS_NO_REFRESH=1   # signals-refresh has its own suite (refresh.sh)
export SIGNALS_NO_APP=1 SIGNALS_NO_NOTIFY=1
unset SIGNALS_NO_LEDGER CLAUDE_CONFIG_DIR CODEX_HOME
trap 'rm -rf "$HOME"' EXIT
# The engine is built outside libexec, as a release would place it.
(cd "$ROOT/core" && CGO_ENABLED=0 go build -o "$HOME/engine/signals-core" ./cmd/signals-core) || { echo "engine build failed"; exit 1; }
export SIGNALS_CORE="$HOME/engine/signals-core"

pass=0 fail=0
check() { # name, expected, actual
  if [ "$2" = "$3" ]; then pass=$((pass+1)); printf '  \033[32m✓\033[0m %s\n' "$1"
  else fail=$((fail+1)); printf '  \033[31m✗\033[0m %s\n      expected: %s\n      actual:   %s\n' "$1" "$2" "$3"; fi
}
meter() { "$METER" | jq -r "$1"; }

# ── Fixtures: today at local noon (so a clock change can't move the day) ─────
noon=$(date -j -f "%Y-%m-%d %H:%M" "$(date +%Y-%m-%d) 12:00" +%s)
TS=$(date -u -r "$noon" +%Y-%m-%dT%H:%M:%S.000Z)
DAY=$(date -r "$noon" +%Y/%m/%d)
P="$HOME/.claude/projects/-work-api"; mkdir -p "$P"
# Claude Code: 400k Sonnet 5.5 output tokens = $4.00, written as two lines of one reply; and a prompt that must never be stored.
for i in 1 2; do
  jq -nc --arg ts "$TS" '{type:"assistant", timestamp:$ts, sessionId:"s1", cwd:"/work/api", requestId:"req_1",
    message:{id:"msg_1", model:"claude-sonnet-5-5", usage:{input_tokens:0, output_tokens:400000}}}' >> "$P/s1.jsonl"
done
jq -nc --arg ts "$TS" '{type:"user", timestamp:$ts, sessionId:"s1", message:{role:"user", content:"SECRET-PROMPT-TEXT about usage"}}' >> "$P/s1.jsonl"
# Codex: gpt-6.1-sol, 10k input ($0.02) + 100k output ($1.00) = $1.02.
X="$HOME/.codex/sessions/$DAY"; mkdir -p "$X"
{
  jq -nc --arg ts "$TS" '{timestamp:$ts, type:"session_meta", payload:{id:"cx-1", session_id:"cx-1", cwd:"/work/web", originator:"codex_tui", creator_account_id:"acct-raw-9"}}'
  jq -nc --arg ts "$TS" '{timestamp:$ts, type:"turn_context", payload:{turn_id:"t1", model:"gpt-6.1-sol", cwd:"/work/web"}}'
  jq -nc --arg ts "$TS" '{timestamp:$ts, type:"token_usage_record", payload:{session_id:"cx-1", turn_id:"t1", response_id:"resp_1",
    usage:{input_tokens:10000, cached_input_tokens:0, cache_write_input_tokens:0, output_tokens:100000, reasoning_output_tokens:0, total_tokens:110000},
    thread_token_usage:{input_tokens:10000, output_tokens:100000, total_tokens:110000}}}'
  jq -nc --arg ts "$TS" '{timestamp:$ts, type:"event_msg", payload:{type:"token_count", rate_limits:{plan_type:"plus"}}}'
} > "$X/rollout-a.jsonl"

echo "pay as you go: the month by tool, and Codex sessions"
mkdir -p "$HOME/.signals/usage"; echo '{"mode":"estimate","budget":null,"warn":0.8}' > "$HOME/.signals/usage/config.json"
check "by tool: Claude Code and Codex, priced by hand" "claude 1 4 codex 1 1.02" \
  "$(meter '[.by_tool.tools[] | "\(.agent) \(.calls) \(.cost)"] | sort | join(" ")')"
check "  ...labelled an API-price estimate" "API-price estimate" "$(meter '.by_tool.label')"
check "  ...the ledger's Claude Code total equals the meter's" "$(meter '.total')" "$(meter '.by_tool.tools[] | select(.agent=="claude") | .cost')"
check "Codex sessions join today's sessions, with their cost" "codex 1.02" "$(meter '.sessions[] | select(.agent=="codex") | "\(.agent) \(.cost)"')"
out=$("$CLI" usage)
check "signals usage: the section, at API prices" 1 "$(grep -c '📊 This month by tool, at API prices' <<<"$out")"
check "  ...Codex with its money" 1 "$(grep -c 'Codex: 1 calls.*\$1.02' <<<"$out")"
check "  ...Codex sessions are named" 1 "$(grep -c 'Codex · web' <<<"$out")"

echo "on a plan: calls and tokens, never money"
echo '{"mode":"plan","warn":0.8}' > "$HOME/.signals/usage/config.json"
out=$("$CLI" usage)
check "the section, without 'at API prices'" "📊 This month by tool" "$(grep '📊' <<<"$out")"
check "  ...and no money in it" 0 "$(grep -A2 '📊' <<<"$out" | grep -c '\$')"

echo "the ledger commands"
check "signals usage summary goes to the engine" 2 "$("$CLI" usage summary | grep -c -E '^  (Claude Code|Codex) ')"
check "signals usage verify: Codex matches its own totals" 1 "$("$CLI" usage verify | grep -c 'Codex: 1 of 1 match')"
check "signals usage ledger: one line per call" 2 "$("$CLI" usage ledger | wc -l | tr -d ' ')"
L="$HOME/.signals/usage/ledger"
check "no prompt text, no raw account id in the ledger" 0 "$(cat "$L"/*/* "$L"/*.json 2>/dev/null | grep -c -E 'SECRET-PROMPT|acct-raw-9')"
check "  ...the account is there, hashed" 1 "$(grep -c '"acct":"[0-9a-f]\{16\}"' "$L/calls/$(date +%Y-%m-%d).jsonl" | tr -d ' ' | awk '{print ($1>=1)}')"

echo "switches"
check "SIGNALS_NO_LEDGER leaves the ledger out" null "$(SIGNALS_NO_LEDGER=1 meter '.by_tool')"
rm -rf "$L"; echo '{"mode":"off"}' > "$HOME/.signals/usage/config.json"
"$METER" >/dev/null
check "Usage off: no ledger is created" no "$([ -d "$L" ] && echo yes || echo no)"

echo
echo "ledger: $pass passed, $fail failed"
[ $fail = 0 ]
