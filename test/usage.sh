#!/bin/bash
# Usage: the meter, gate, usage commands and text menu. Runs in a throwaway $HOME
# with fixture Claude Code transcripts whose costs are worked out by hand.
#   ./test/usage.sh   (or ./test/run.sh for everything)
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CLI="$ROOT/bin/signals"
METER="$ROOT/libexec/signals-meter"
GATE="$ROOT/libexec/signals-gate"
PLUGIN="$ROOT/libexec/signals-usage-menu"
export HOME="$(mktemp -d)"
export SIGNALS_NO_APP=1
export SIGNALS_NO_NOTIFY=1
trap 'rm -rf "$HOME"' EXIT
# Tests never show real dialogs: a stub osascript answers (or cancels) instead.
mkdir -p "$HOME/bin"; printf '#!/bin/sh\n# stub osascript\nexit 1\n' > "$HOME/bin/osascript"; chmod +x "$HOME/bin/osascript"
export PATH="$HOME/bin:$PATH"

pass=0 fail=0
check() { # name, expected, actual
  if [ "$2" = "$3" ]; then pass=$((pass+1)); printf '  \033[32m✓\033[0m %s\n' "$1"
  else fail=$((fail+1)); printf '  \033[31m✗\033[0m %s\n      expected: %s\n      actual:   %s\n' "$1" "$2" "$3"; fi
}
state() { jq -r "$1" "$HOME/.signals/usage/state.json"; }

# ── Fixtures ──────────────────────────────────────────────────────────────────
noon=$(date -j -f "%Y-%m-%d %H:%M" "$(date +%Y-%m-%d) 12:00" +%s)
TODAY=$(date -u -r "$noon" +%Y-%m-%dT%H:%M:%S.000Z)
LASTMONTH=$(date -u -r "$(date -j -v-1m -f "%Y-%m-%d %H:%M" "$(date +%Y-%m-15) 12:00" +%s)" +%Y-%m-%dT%H:%M:%S.000Z)
msg() { # id model usage-json [timestamp] [cwd]
  jq -nc --arg id "$1" --arg m "$2" --argjson u "$3" --arg ts "${4:-$TODAY}" --arg cwd "${5:-/code/acme/api}" \
    '{type:"assistant", timestamp:$ts, requestId:"req_\($id)", cwd:$cwd,
      message:{id:"msg_\($id)", model:$m, usage:$u}}'
}
P="$HOME/.claude/projects/-code-acme-api"; mkdir -p "$P"
{
  # Opus 5.5: 1000 in, 2000 out, 10k 5m write, 5k 1h write, 100k read
  #   = (1000*4 + 2000*20 + 10000*4*1.25 + 5000*4*2 + 100000*0.2) / 1e6 = $0.154
  msg a claude-opus-5-5 '{"input_tokens":1000,"output_tokens":2000,"cache_creation_input_tokens":15000,"cache_read_input_tokens":100000,"cache_creation":{"ephemeral_5m_input_tokens":10000,"ephemeral_1h_input_tokens":5000}}'
  # same message streamed again: must not double count
  msg a claude-opus-5-5 '{"input_tokens":1000,"output_tokens":2000,"cache_creation_input_tokens":15000,"cache_read_input_tokens":100000,"cache_creation":{"ephemeral_5m_input_tokens":10000,"ephemeral_1h_input_tokens":5000}}'
  # date-suffixed id falls back to the base model: 1M Haiku 4.5 input = $1.00
  msg b claude-haiku-4-5-20251001 '{"input_tokens":1000000,"output_tokens":0}'
  # no cache breakdown: treat writes as 5 min. 1M Sonnet 5.5 writes = 1e6*2*1.25 = $2.50
  msg c claude-sonnet-5-5 '{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":1000000}'
  # fast mode doubles: 1M Opus 5.5 output = 1e6*20*2 = $40.00
  msg d claude-opus-5-5 '{"input_tokens":0,"output_tokens":1000000,"speed":"fast"}'
  # unknown model: counted as unpriced, $0
  msg e gpt-9-turbo '{"input_tokens":5000,"output_tokens":5000}'
  # synthetic and non-assistant lines are ignored
  msg f '<synthetic>' '{"input_tokens":9999999,"output_tokens":9999999}'
  echo '{"type":"user","message":{"content":"hi"}}'
  # last month: excluded from this month's total
  msg g claude-opus-5-5 '{"input_tokens":0,"output_tokens":1000000}' "$LASTMONTH"
} > "$P/session1.jsonl"
# A transcript whose last line is half-written (agent still writing): skipped.
{ msg h0 claude-haiku-4-5 '{"input_tokens":0,"output_tokens":0}'
  printf '{"type":"assistant","timestamp":"%s","message":{"id":"msg_h' "$TODAY"; } > "$P/session3.jsonl"
# A resumed session copies message "a" into a new transcript: still counted once.
mkdir -p "$HOME/.claude/projects/-code-web-app"
msg a claude-opus-5-5 '{"input_tokens":1000,"output_tokens":2000,"cache_creation_input_tokens":15000,"cache_read_input_tokens":100000,"cache_creation":{"ephemeral_5m_input_tokens":10000,"ephemeral_1h_input_tokens":5000}}' \
  > "$HOME/.claude/projects/-code-web-app/session2.jsonl"
# 0.154 + 1 + 2.5 + 40 = 43.654

echo "meter"
mkdir -p "$HOME/.signals/usage"; echo '{"budget":null,"warn":0.8}' > "$HOME/.signals/usage/config.json"
"$METER" >/dev/null;                               check "meter exits 0" 0 "$?"
check "month total, priced by hand" 43.65 "$(state .total)"
check "today" 43.65 "$(state .today)"
check "unknown model reported" gpt-9-turbo "$(state '.unpriced | join(",")')"
check "no budget → status nobudget" nobudget "$(state .status)"
before=$(ls -l "$HOME/.signals/usage/cache" | md5); "$METER" >/dev/null
check "unchanged transcripts are not re-parsed" "$before" "$(ls -l "$HOME/.signals/usage/cache" | md5)"
msg i claude-haiku-4-5 '{"input_tokens":1000000,"output_tokens":0}' >> "$P/session1.jsonl"
"$METER" >/dev/null;                               check "new usage picked up" 44.65 "$(state .total)"
echo '{"budget":null,"warn":0.8}' > "$HOME/.signals/usage/config.json"
cp "$ROOT/libexec/pricing.json" "$HOME/.signals/usage/pricing.json"
jq '.models["claude-haiku-4-5"].input = 2' "$HOME/.signals/usage/pricing.json" > "$HOME/p.json" && mv "$HOME/p.json" "$HOME/.signals/usage/pricing.json"
"$METER" >/dev/null;                               check "user pricing override re-prices" 46.65 "$(state .total)"
rm "$HOME/.signals/usage/pricing.json"; "$METER" >/dev/null

# Two meters at once (menu bar refresh + gate) on a cold cache: no clashes.
rm -rf "$HOME/.signals/usage/cache"
"$METER" >/dev/null 2>"$HOME/m1.err" & "$METER" >/dev/null 2>"$HOME/m2.err" & "$METER" >/dev/null 2>"$HOME/m3.err"; wait
check "concurrent meters don't clash" 0 "$(cat "$HOME"/m?.err | wc -l | tr -d ' ')"
check "  ...and agree on the total" 44.65 "$(state .total)"

echo "budget and gate"
gate() { echo '{"prompt":"hi"}' | "$GATE" 2>"$HOME/gate.err"; echo $?; }
"$CLI" budget 100 >/dev/null;                      check "budget 100 → ok" ok "$(state .status)"
check "pct" 44 "$(state .pct)"
check "gate open under budget" 0 "$(gate)"
"$CLI" budget 50 >/dev/null;                       check "budget 50 → warn (80%+)" warn "$(state .status)"
check "gate open when close" 0 "$(gate)"
"$CLI" budget '$40' >/dev/null;                    check "budget \$40 → over" over "$(state .status)"
check "gate blocks when over" 2 "$(gate)"
check "block message explains the toll" 1 "$(grep -c 'signals pass 1h' "$HOME/gate.err")"
"$CLI" pass 1h >/dev/null;                         check "pass lets prompts through" 0 "$(gate)"
until=$(cat "$HOME/.signals/usage/pass"); now=$(date +%s)
check "pass lasts an hour" 1 "$([ $((until - now)) -ge 3590 ] && [ $((until - now)) -le 3600 ] && echo 1 || echo 0)"
"$CLI" pass 2h30m >/dev/null; until=$(cat "$HOME/.signals/usage/pass")
check "pass 2h30m" 1 "$([ $((until - $(date +%s))) -ge 8990 ] && echo 1 || echo 0)"
"$CLI" pass off >/dev/null;                        check "pass off → blocked again" 2 "$(gate)"
echo $(( $(date +%s) - 10 )) > "$HOME/.signals/usage/pass"
check "expired pass doesn't count" 2 "$(gate)"
"$CLI" pass soon >/dev/null 2>&1;                  check "bad duration rejected" 1 "$?"
"$CLI" budget lots >/dev/null 2>&1;                check "bad budget rejected" 1 "$?"
echo '{not json' > "$HOME/.signals/usage/state.json"
touch "$HOME/.signals/usage/state.json";               check "gate fails open on bad state" 0 "$(gate)"
"$CLI" budget off >/dev/null;                      check "budget off → gate open" 0 "$(gate)"
check "  ...status nobudget" nobudget "$(state .status)"

echo "plugin"
"$CLI" budget 100 >/dev/null; out=$("$PLUGIN")
check "ok icon" "🚧 \$44.65" "$(head -1 <<<"$out")"
check "progress bar" 1 "$(grep -c '▓▓▓▓░░░░░░' <<<"$out")"
"$CLI" budget 50 >/dev/null; out=$("$PLUGIN");    check "warn icon" "⚠️ \$44.65" "$(head -1 <<<"$out")"
"$CLI" budget 40 >/dev/null; out=$("$PLUGIN");    check "over icon" "⛔️ \$44.65" "$(head -1 <<<"$out")"
check "offers to pay the toll" 1 "$(grep -c 'Pay the toll' <<<"$out")"
"$CLI" pass 1h >/dev/null; out=$("$PLUGIN");      check "pass icon" "🎫 \$44.65" "$(head -1 <<<"$out")"
check "offers to cancel pass" 1 "$(grep -ci 'cancel pass' <<<"$out")"

echo "modes"
export SIGNALS_TEST_KEYS="$HOME/keys" SIGNALS_SKIP_VERIFY=1 SIGNALS_NO_PROMPT=1
"$CLI" budget off >/dev/null; "$CLI" pass off >/dev/null
fresh=$(mktemp -d); check "fresh install: usage off" "🚧 Usage off" "$(HOME=$fresh "$PLUGIN" | head -1)"; rm -rf "$fresh"

# Plan mode: account usage, as percentages. Money only when on usage credits.
export SIGNALS_DETECTED_PLAN=pro
"$CLI" usage plan >/dev/null;                      check "setup plan" plan "$(state .mode)"
"$CLI" usage plan max >/dev/null 2>&1;             check "the plan can't be chosen by hand" 1 "$?"
SIGNALS_DETECTED_PLAN= "$CLI" usage plan >/dev/null 2>&1; check "no Claude login: setup explains and stops" 1 "$?"
SIGNALS_DETECTED_PLAN=max20 "$METER" >/dev/null;   check "plan follows the login (upgrade to Max 20x)" "Claude Max 20x" "$(state .plan.label)"
SIGNALS_DETECTED_PLAN=max "$METER" >/dev/null;     check "  ...and back down to Max 5x" "Claude Max 5x 100" "$(state '"\(.plan.label) \(.plan.price)"')"
SIGNALS_DETECTED_PLAN=team "$METER" >/dev/null;    check "  ...other plans by name" "Claude Team" "$(state .plan.label)"
SIGNALS_DETECTED_PLAN= "$METER" >/dev/null;        check "  ...signed out keeps the last known plan" "Claude Team" "$(state .plan.label)"
"$CLI" usage bogus >/dev/null 2>&1;                check "unknown mode rejected" 1 "$?"
"$CLI" usage plan >/dev/null
check "plan detected from the Claude login" "Claude Pro" "$(state .plan.label)"
out=$("$PLUGIN")
check "no reading yet → points to /usage" 1 "$(grep -c 'Run /usage' <<<"$out")"
check "plan view never shows money" 0 "$(grep -c '\$' <<<"$out")"

# Account usage fixtures, shaped like the usage endpoint.
"$CLI" usage plan >/dev/null;                  check "no login switch to set" null "$(jq -r .account_usage "$HOME/.signals/usage/config.json")"
usage() { # session% weekly% [locked] [credits_enabled] [credits_used]
  local r1=$(( $(date +%s) + 7200 )) r7=$(( $(date +%s) + 3*86400 ))
  jq -n --argjson s "$1" --argjson w "$2" --arg locked "${3:-}" --argjson ce "${4:-false}" --argjson cu "${5:-0}" \
        --arg r1 "$(date -u -r $r1 +%Y-%m-%dT%H:%M:%S.000000+00:00)" --arg r7 "$(date -u -r $r7 +%Y-%m-%dT%H:%M:%S.000000+00:00)" '
    { five_hour: {utilization: $s, resets_at: $r1, locked_reason: (if $locked == "" then null else $locked end)},
      seven_day: {utilization: $w, resets_at: $r7, locked_reason: null},
      extra_usage: {is_enabled: $ce, used_credits: $cu, monthly_limit: (if $ce then 50 else null end)},
      limits: [{kind: "session", percent: $s, severity: "normal", is_active: true},
               {kind: "weekly_all", percent: $w, severity: "normal", is_active: true}],
      seven_day_breakdown: {rows: [{display_name: "Claude Code", percent: 95}, {display_name: "Chats", percent: 5}, {display_name: "Cowork", percent: 0}]} }' > "$HOME/usage.json"
}
pv() { SIGNALS_USAGE_FIXTURE="$HOME/usage.json" "$PLUGIN"; }
usage 42 15; out=$(pv)
check "normal: title is the 5-hour window, labelled" "🚧 42%" "$(head -1 <<<"$out")"
check "normal: session bar and reset" 1 "$(grep -c '5-hour ▓▓▓▓░░░░░░ 42% · resets' <<<"$out")"
check "normal: weekly line" 1 "$(grep -c 'This week ▓░░░░░░░░░ 15% · resets' <<<"$out")"
check "normal: product split (zero rows hidden)" "Claude Code 95% · Chats 5%" "$(grep 'Claude Code 95%' <<<"$out" | sed 's/ | .*//')"
check "normal: source line" 1 "$(grep -c 'From Claude Code' <<<"$out")"
check "normal: status ok" ok "$(state .status)"
check "normal: no money" 0 "$(grep -c '\$' <<<"$out")"
check "normal: gate stays open on a plan" 0 "$(gate)"
check "credits off: shown, no money" "Usage credits off" "$(grep '^Usage credits off' <<<"$out" | sed 's/ | .*//')"
check "link to Claude's usage and credits page" 1 "$(grep -c 'Usage and credits on claude.ai ↗ | href=https://claude.ai/settings/usage' <<<"$out")"
jq '.extra_usage.disabled_reason = "out_of_credits"' "$HOME/usage.json" > "$HOME/u2.json" && mv "$HOME/u2.json" "$HOME/usage.json"
check "credits off: says out of credits" 1 "$(pv | grep -c '^Usage credits off · out of credits')"
usage 42 15 "" true 1.5
check "credits on, under the limit: credit spend shown" 1 "$(pv | grep -c '^💳 Usage credits · \$1.50 used of \$50.00 this month')"
check "credits on, under the limit: title stays the 5-hour window" "🚧 42%" "$(pv | head -1)"
usage 86 30; out=$(pv)
check "approaching: ⚠️ title" "⚠️ 86%" "$(head -1 <<<"$out")"
check "approaching: status warn" warn "$(state .status)"
usage 30 88; out=$(pv)
check "week nearly used up (80%+): title switches to the week" "⚠️ 88%" "$(head -1 <<<"$out")"
usage 100 40 session_limit; out=$(pv)
check "limit hit: ⛔️ title" "⛔️ 100%" "$(head -1 <<<"$out")"
check "limit hit: back at" 1 "$(grep -c '5-hour limit reached · back at' <<<"$out")"
check "limit hit: offers credits" 1 "$(grep -c 'Keep going with usage credits' <<<"$out")"
check "limit hit: still no money" 0 "$(grep -c '\$' <<<"$out")"
check "limit hit: status blocked" blocked "$(state .status)"
usage 60 100; out=$(pv)
check "weekly limit hit: ⛔️ 100%" "⛔️ 100%" "$(head -1 <<<"$out")"
check "weekly limit hit: back day" 1 "$(grep -c 'Weekly limit reached · back' <<<"$out")"
usage 100 40 session_limit true 4.2; out=$(pv)
check "on credits: 💳 title with money" "💳 \$4.20" "$(head -1 <<<"$out")"
check "on credits: used of limit" 1 "$(grep -c 'On usage credits · \$4.20 used of \$50.00' <<<"$out")"
check "on credits: status credits" credits "$(state .status)"
usage 0 15; jq '.five_hour.resets_at = null | .limits[0].is_active = false' "$HOME/usage.json" > "$HOME/u2.json" && mv "$HOME/u2.json" "$HOME/usage.json"; out=$(pv)
check "no 5-hour window running: 0%, not the week" "🚧 0%" "$(head -1 <<<"$out")"
check "no session: says not started" 1 "$(grep -c '5-hour window not started' <<<"$out")"
# Pace: two readings 20 minutes apart, rising 20 points → eta inside the window.
rm -f "$HOME/.signals/usage/usage-history.json"
r1=$(( $(date +%s) + 7200 )); r1iso=$(date -u -r $r1 +%Y-%m-%dT%H:%M:%S.000000+00:00); r1e=$(jq -rn --arg i "$r1iso" '$i | sub("\\.[0-9]+"; "") | sub("\\+00:00$"; "Z") | fromdateiso8601')
jq -nc --argjson t $(( $(date +%s) - 1200 )) --argjson r "$r1e" '[{t: $t, pct: 60, resets_at: $r}]' > "$HOME/.signals/usage/usage-history.json"
usage 80 30; jq --arg r "$r1iso" '.five_hour.resets_at = $r' "$HOME/usage.json" > "$HOME/u2.json" && mv "$HOME/u2.json" "$HOME/usage.json"
out=$(pv)
check "pace: projects when the limit is hit" 1 "$(grep -c 'At this pace you hit it around' <<<"$out")"
check "pace: eta is about 20 min out" 1 "$(state '.account.eta - .updated | . > 900 and . < 1500' | grep -c true)"
# Fallback: exact limits from the terminal status line.
"$CLI" usage plan >/dev/null
SL="$ROOT/libexec/signals-statusline"
reset=$(( $(date +%s) + 3600 ))
line=$(jq -nc --argjson r "$reset" '{cost:{total_cost_usd:1.2}, rate_limits:{five_hour:{used_percentage:23.5, resets_at:$r}, seven_day:{used_percentage:41.2, resets_at:($r+86400)}}}' | "$SL")
check "status line shows 5h and week" 1 "$(grep -c '5h 23% .* week 41%' <<<"$line")"
out=$("$PLUGIN")
check "status-line fallback: title" "🚧 23%" "$(head -1 <<<"$out")"
check "status-line fallback: source" 1 "$(grep -c 'From the terminal status line' <<<"$out")"
line=$(echo '{"cost":{"total_cost_usd":0.456}}' | "$SL")
check "plan status line never shows money" "🚧" "$line"
jq '.five_hour.resets_at = 1 | .seven_day.resets_at = 1' "$HOME/.signals/usage/limits.json" > "$HOME/l.json" && mv "$HOME/l.json" "$HOME/.signals/usage/limits.json"
"$METER" >/dev/null;                               check "expired readings dropped" null "$(state .account)"
check "plan status never shows money" 0 "$("$CLI" status | grep -c '\$')"
# Limit hits counted from "You've hit your limit" entries in the logs.
jq -nc --arg ts "$TODAY" '{type:"assistant", timestamp:$ts, isApiErrorMessage:true, error:"rate_limit", message:{model:"<synthetic>", content:[{type:"text", text:"You have hit your limit"}]}}' >> "$P/session1.jsonl"
"$METER" >/dev/null;                               check "limit hits this week" 1 "$(state .limit_hits_week)"

# Anthropic API mode: billed spend from the cost report (fixture, no network).
SIGNALS_KEY=sk-ant-admin01-test "$CLI" usage anthropic >/dev/null
check "setup anthropic" anthropic "$(state .mode)"
check "  ...key stored" sk-ant-admin01-test "$(cat "$HOME/keys/anthropic-admin")"
today_d=$(date -u +%Y-%m-%d)
jq -n --arg d "$today_d" '{data:[{starting_at:"\($d)T00:00:00Z", ending_at:"x", results:[{amount:"12345.6",currency:"USD"},{amount:"100",currency:"USD"}]},
  {starting_at:"2000-01-01T00:00:00Z", ending_at:"x", results:[{amount:"50",currency:"USD"}]}], has_more:false, next_page:null}' > "$HOME/cost.json"
rm -f "$HOME"/.signals/usage/api-cost-*.json
export SIGNALS_COST_FIXTURE="$HOME/cost.json"
"$METER" >/dev/null
check "Anthropic billed (cents → dollars)" 124.96 "$(state .spend)"
check "Anthropic billed today" 124.46 "$(state .today_spend)"
out=$("$PLUGIN")
check "API view title" "🚧 \$125" "$(head -1 <<<"$out")"
check "API view label" 1 "$(grep -c '^Anthropic API · ' <<<"$out")"
"$CLI" budget 100 >/dev/null;                      check "budget applies to billed spend" over "$(state .status)"
check "Anthropic over budget gates Claude" 2 "$(gate)"
"$CLI" budget off >/dev/null; unset SIGNALS_COST_FIXTURE

# OpenAI API mode: amount.value in dollars, buckets in Unix seconds.
SIGNALS_KEY=sk-admin-test "$CLI" usage openai >/dev/null
check "setup openai" openai "$(state .mode)"
today_s=$(date -u -j -f "%Y-%m-%d %H:%M:%S" "$today_d 00:00:00" +%s)
jq -n --argjson t "$today_s" '{object:"page", data:[{start_time:$t, end_time:($t+86400), results:[{amount:{value:3.5,currency:"usd"}},{amount:{value:1.25,currency:"usd"}}]},
  {start_time:1, end_time:86401, results:[{amount:{value:10,currency:"usd"}}]}], has_more:false, next_page:null}' > "$HOME/ocost.json"
rm -f "$HOME"/.signals/usage/api-cost-*.json
export SIGNALS_COST_FIXTURE="$HOME/ocost.json"
"$METER" >/dev/null
check "OpenAI billed (dollars)" 14.75 "$(state .spend)"
check "OpenAI billed today" 4.75 "$(state .today_spend)"
check "API view label (OpenAI)" 1 "$("$PLUGIN" | grep -c '^OpenAI API · ')"
"$CLI" budget 5 >/dev/null;                        check "OpenAI over budget → over" over "$(state .status)"
check "OpenAI spend never gates Claude" 0 "$(gate)"
"$CLI" budget off >/dev/null; unset SIGNALS_COST_FIXTURE

# Failures: bad report, missing key.
echo '{"error":{"type":"authentication_error"}}' > "$HOME/bad.json"; rm -f "$HOME"/.signals/usage/api-cost-*.json
SIGNALS_COST_FIXTURE="$HOME/bad.json" "$METER" >/dev/null
check "bad cost report → no spend" null "$(state .spend)"
check "  ...menu shows ?" "🚧 ?" "$(SIGNALS_COST_FIXTURE="$HOME/bad.json" "$PLUGIN" | head -1)"
"$CLI" disconnect >/dev/null;                      check "disconnect removes keys" 0 "$(ls "$HOME/keys" | wc -l | tr -d ' ')"
"$METER" >/dev/null;                               check "no key → says how to add one" 1 "$(state .api.error | grep -c 'signals usage anthropic')"
"$CLI" usage estimate >/dev/null;                  check "setup estimate" estimate "$(state .mode)"
check "estimate spend is the local total" 44.65 "$(state .spend)"

# Sessions today (estimate / API key): titles, live state, expensive flag,
# spike against your usual day, and the monthly projection.
rm -rf "$HOME/.claude/projects" "$HOME/.signals/usage/cache"; mkdir -p "$HOME/.claude/projects/-s"
now=$(date +%s); iso() { date -u -r "$1" +%Y-%m-%dT%H:%M:%S.000Z; }
haiku() { msg "$1" claude-haiku-4-5 "{\"input_tokens\":$(( $2 * 1000000 )),\"output_tokens\":0}" "$(iso "$3")" "$4"; }
# Past days at local noon: "now minus 48 hours" can land two sessions on one day across a clock change.
noon_ago() { date -j -v-"$1"d -f "%Y-%m-%d %H:%M" "$(date +%Y-%m-%d) 12:00" +%s; }
for d in 2 3 4; do haiku "past$d" 1 "$(noon_ago $d)" /code/old > "$HOME/.claude/projects/-s/past$d.jsonl"; done
{ echo '{"type":"ai-title","aiTitle":"Refactor billing service"}'; haiku a1 5 $(( now - 30 )) /code/acme/api; } > "$HOME/.claude/projects/-s/sessA.jsonl"
{ echo '{"type":"ai-title","aiTitle":"Fix the login redirect"}';   haiku b1 1 $(( now - 120 )) /code/web; } > "$HOME/.claude/projects/-s/sessB.jsonl"
"$CLI" usage estimate >/dev/null; "$CLI" budget 1000 >/dev/null
out=$("$PLUGIN")
check "sessions: Claude titles listed" 2 "$(grep -cE 'Refactor billing service|Fix the login redirect' <<<"$out")"
check "sessions: newest first" "Refactor billing service" "$(state '.sessions[0].title')"
check "sessions: working one is 🔴" 1 "$(grep -c '^🔴 Refactor billing service  \$5.00' <<<"$out")"
check "sessions: idle one is 🟢" 1 "$(grep -c '^🟢 Fix the login redirect  \$1.00' <<<"$out")"
check "sessions: expensive one flagged" 1 "$(grep -c 'Refactor billing service  \$5.00 ⚠️ 2.7×' <<<"$out")"
check "sessions: cheap one not flagged" 0 "$(grep -c 'Fix the login redirect.*⚠️' <<<"$out")"
check "sessions: click opens the folder" 1 "$(grep -c 'href=file:///code/acme/api' <<<"$out")"
check "spike: 6× your usual day" 1 "$(grep -c 'Today \$6.00 · 6× your usual' <<<"$out")"
check "projection shown" 1 "$(grep -c '^On pace for \$' <<<"$out")"
check "projection beyond month-to-date" true "$(state '.projected >= .spend')"
"$CLI" budget "$(state '.spend / 0.85 * 100 | floor / 100')" >/dev/null; out=$("$PLUGIN")   # 85% used, to the cent (whole dollars miss 80% when spend is small)
check "warn: ⚠️ title at 80%+" 1 "$(head -1 <<<"$out" | grep -c '^⚠️ ')"
check "over-by date shown exactly when the pace beats the budget" \
  "$(state 'if .projected > .budget and .spend < .budget then 1 else 0 end')" "$(grep -c 'over by ' <<<"$out")"
unset SIGNALS_TEST_KEYS SIGNALS_SKIP_VERIFY SIGNALS_NO_PROMPT

echo "wizard (interactive, driven by expect)"
# Each run answers prompts like a person would; a prompt that never appears or
# never finishes fails the test after 10 seconds instead of hanging.
export SIGNALS_TEST_KEYS="$HOME/wkeys" SIGNALS_SKIP_VERIFY=1 SIGNALS_DETECTED_PLAN=pro SIGNALS_UI=text
wiz() { # setup-args, expect-steps
  expect -c "set timeout 10; log_user 0; spawn -noecho $CLI usage --text $1; $2
             expect { eof {} timeout { exit 99 } }; catch wait r; exit [lindex \$r 3]" 2>/dev/null
}
cfg() { jq -r "$1" "$HOME/.signals/usage/config.json"; }
wiz "" 'expect "> "; send "1\r"'
check "wizard: plan, detected Pro, nothing else to ask" "plan pro" "$(cfg '"\(.mode) \(.plan.name)"')"
wiz anthropic 'expect "(hidden): "; send "sk-ant-admin01-first\r"; expect "budget"; send "60\r"'
check "wizard: Anthropic, new key, budget" "anthropic 60" "$(cfg '"\(.mode) \(.budget)"')"
check "  ...key saved" sk-ant-admin01-first "$(cat "$HOME/wkeys/anthropic-admin")"
wiz anthropic 'expect "already saved"; expect "> "; send "1\r"; expect "budget"; send "\r"'
check "wizard: reuse the saved key" sk-ant-admin01-first "$(cat "$HOME/wkeys/anthropic-admin")"
check "  ...blank budget keeps it" 60 "$(cfg .budget)"
wiz anthropic 'expect "already saved"; expect "> "; send "2\r"; expect "(hidden): "; send "sk-ant-admin01-second\r"; expect "budget"; send "\r"'
check "wizard: replace the saved key" sk-ant-admin01-second "$(cat "$HOME/wkeys/anthropic-admin")"
wiz "" 'expect "> "; send "9\r"'; rc=$?
check "wizard: invalid choice exits with an error" 1 "$rc"
check "  ...and changes nothing" anthropic "$(cfg .mode)"
wiz openai 'expect "(hidden): "; send "\004"'; rc=$?
check "wizard: cancel at the key prompt exits with an error" 1 "$rc"
check "  ...and changes nothing" anthropic "$(cfg .mode)"
SIGNALS_DETECTED_PLAN="" wiz "" 'expect "> "; send "1\r"'; rc=$?
check "wizard: plan with no Claude login stops with an error" 1 "$rc"
check "  ...and changes nothing" anthropic "$(cfg .mode)"
wiz "" 'expect "> "; send "2\r"; expect "budget"; send "75\r"'
check "wizard: pay as you go, with a budget" "estimate 75" "$(cfg '"\(.mode) \(.budget)"')"
unset SIGNALS_SKIP_VERIFY
wiz openai 'expect "(hidden): "; send "sk-admin-not-a-real-key\r"'; rc=$?
check "wizard: a key the provider rejects is not saved" 1 "$rc"
check "  ...and changes nothing" estimate "$(cfg .mode)"
check "  ...no OpenAI key stored" no "$([ -f "$HOME/wkeys/openai-admin" ] && echo yes || echo no)"
unset SIGNALS_TEST_KEYS SIGNALS_DETECTED_PLAN

echo "install"
"$CLI" budget 40 >/dev/null
S="$HOME/.claude/settings.json"
echo '{"model":"opus"}' > "$S"
"$CLI" install claude >/dev/null 2>&1;             check "install exits 0" 0 "$?"
check "other settings kept" opus "$(jq -r .model "$S")"
"$CLI" uninstall >/dev/null 2>&1;                  check "uninstall keeps the budget" 40 "$(jq -r .budget "$HOME/.signals/usage/config.json")"
"$CLI" install claude --statusline >/dev/null 2>&1; check "install --statusline" 1 "$(jq -r '.statusLine.command' "$S" | grep -c signals-statusline)"
"$CLI" uninstall >/dev/null 2>&1;                  check "uninstall removes our status line" null "$(jq -r '.statusLine' "$S")"
jq '.statusLine = {type:"command", command:"my-line.sh"}' "$S" > "$HOME/s.json" && mv "$HOME/s.json" "$S"
"$CLI" install claude --statusline >/dev/null 2>&1; check "existing status line left alone" my-line.sh "$(jq -r '.statusLine.command' "$S")"
"$CLI" uninstall >/dev/null 2>&1;                  check "  ...and kept on uninstall" my-line.sh "$(jq -r '.statusLine.command' "$S")"
"$CLI" uninstall --purge >/dev/null 2>&1;          check "uninstall --purge" no "$([ -d "$HOME/.signals" ] && echo yes || echo no)"
check "meter hands notifications to the app (Claude and Codex)" 2 "$(grep -c 'SIGNALS_APP' "$ROOT/libexec/signals-meter")"

echo
echo "usage: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
