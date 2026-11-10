#!/bin/bash
# Signals as one product: the gate inside the session hook, Usage on and off,
# moving over from trafficlight and tollbooth, the combined menu, the app and
# the release checks. Runs in a throwaway $HOME.
#   ./test/signals.sh   (or ./test/run.sh for everything)
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CLI="$ROOT/bin/signals"
HOOK="$ROOT/libexec/signals-hook"
export HOME="$(mktemp -d)"
export SIGNALS_NO_APP=1 SIGNALS_NO_NOTIFY=1 SIGNALS_TEST_KEYS="$HOME/keys" SIGNALS_SKIP_VERIFY=1 SIGNALS_NO_PROMPT=1
trap 'rm -rf "$HOME"' EXIT
mkdir -p "$HOME/bin"; printf '#!/bin/sh\nexit 1\n' > "$HOME/bin/osascript"; chmod +x "$HOME/bin/osascript"
export PATH="$HOME/bin:$PATH"

pass=0 fail=0
check() { # name, expected, actual
  if [ "$2" = "$3" ]; then pass=$((pass+1)); printf '  \033[32m✓\033[0m %s\n' "$1"
  else fail=$((fail+1)); printf '  \033[31m✗\033[0m %s\n      expected: %s\n      actual:   %s\n' "$1" "$2" "$3"; fi
}
U="$HOME/.signals/usage"
prompt() { # sid → exit code of the Claude prompt hook; stderr kept in $HOME/held
  jq -nc --arg s "$1" '{session_id:$s, cwd:"/work/\($s)", prompt:"go"}' | "$HOOK" claude UserPromptSubmit 2>"$HOME/held"; echo $?
}
status() { jq -r .status "$HOME/.signals/sessions/claude-$1.json" 2>/dev/null || echo none; }
# One priced reply today: 1M Haiku 4.5 input tokens = $1.00.
P="$HOME/.claude/projects/-work"; mkdir -p "$P"
jq -nc --arg ts "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" \
  '{type:"assistant", timestamp:$ts, requestId:"r1", cwd:"/work", message:{id:"m1", model:"claude-haiku-4-5", usage:{input_tokens:1000000, output_tokens:0}}}' > "$P/s1.jsonl"

echo "usage off by default"
check "fresh install: usage off" off "$("$ROOT/libexec/signals-meter" | jq -r .mode)"
check "  ...and the meter doesn't scan logs" no "$([ -d "$U/cache" ] && ls "$U/cache" | grep -q . && echo yes || echo no)"
check "_usage_info: no mode" null "$("$CLI" _usage_info | jq -r .mode)"
check "prompt goes through with usage off" 0 "$(prompt a1)"
check "  ...and the session is working" working "$(status a1)"
check "usage status says it's off" 1 "$("$CLI" usage | grep -c 'Usage is off')"

echo "the gate, inside the session hook"
"$CLI" usage estimate >/dev/null; "$CLI" budget 5 >/dev/null
check "under budget: prompt goes through" 0 "$(prompt b1)"
"$CLI" budget 0.5 >/dev/null
jq -nc '{session_id:"b2", cwd:"/work/b2"}' | "$HOOK" claude Stop
check "over budget: prompt held" 2 "$(prompt b2)"
check "  ...with the way through" 1 "$(grep -c 'signals pass 1h' "$HOME/held")"
check "  ...and the session doesn't start working" done "$(status b2)"
check "other agents aren't gated" 0 "$(jq -nc '{session_id:"x1", cwd:"/w"}' | "$HOOK" codex UserPromptSubmit; echo $?)"
"$CLI" pass 1h >/dev/null
check "a pass lets it through" 0 "$(prompt b2)"
"$CLI" pass off >/dev/null
"$CLI" usage off >/dev/null
check "usage off: gate open again" 0 "$(prompt b3)"
check "  ...budget kept for next time" 0.5 "$(jq -r .budget "$U/config.json")"
SIGNALS_DETECTED_PLAN=pro "$CLI" usage plan >/dev/null
check "plan mode never gates" 0 "$(prompt b4)"
"$CLI" usage off >/dev/null

echo "the plan and its usage, from what Claude Code saves (never its login)"
# A stand-in for the Keychain tool that records any use: Signals must never call it for Claude.
printf '#!/bin/sh\ncase "$*" in *Claude*) touch "%s/keychain-touched" ;; esac\nexit 44\n' "$HOME" > "$HOME/bin/security"; chmod +x "$HOME/bin/security"
account() { jq -n --arg t "$1" --arg r "$2" '{oauthAccount: {organizationType: $t, organizationRateLimitTier: $r, emailAddress: "someone@example.com"}}' > "$HOME/.claude.json"; }
account claude_pro default_claude_ai;           check "Pro" "Claude Pro" "$("$ROOT/libexec/signals-plan" | jq -r .label)"
account claude_max default_claude_max_5x;       check "Max 5x" "Claude Max 5x" "$("$ROOT/libexec/signals-plan" | jq -r .label)"
account claude_max default_claude_max_20x;      check "Max 20x" "Claude Max 20x" "$("$ROOT/libexec/signals-plan" | jq -r .label)"
check "_usage_info passes it to the window" "max20 Claude Max 20x" "$("$CLI" _usage_info | jq -r '"\(.detected_plan) \(.detected_label)"')"
account claude_team default_claude_team;        check "Team" "Claude Team" "$("$ROOT/libexec/signals-plan" | jq -r .label)"
echo '{}' > "$HOME/.claude.json";               check "not signed in: nothing" "" "$("$ROOT/libexec/signals-plan")"
check "  ...and the window knows" null "$("$CLI" _usage_info | jq -r .detected_plan)"

# Claude Code's saved usage, shaped like ~/.claude.json's cachedUsageUtilization.
saved() { # session% weekly% reading-age-seconds [session-resets-in-seconds]
  local now=$(date +%s); local r5=$(( now + ${4:-3600} )) r7=$(( now + 3*86400 ))
  jq -n --argjson s "$1" --argjson w "$2" --argjson at $(( (now - $3) * 1000 )) \
        --arg r5 "$(date -u -r $r5 +%Y-%m-%dT%H:%M:%S.000000+00:00)" --arg r7 "$(date -u -r $r7 +%Y-%m-%dT%H:%M:%S.000000+00:00)" '
    {oauthAccount: {organizationType: "claude_pro"},
     cachedUsageUtilization: {fetchedAtMs: $at, utilization: {
       five_hour: {utilization: $s, resets_at: $r5}, seven_day: {utilization: $w, resets_at: $r7},
       extra_usage: {is_enabled: false, used_credits: 0, disabled_reason: "out_of_credits"},
       limits: [{kind: "session", percent: $s, is_active: true}, {kind: "weekly_all", percent: $w, is_active: true}],
       seven_day_breakdown: {rows: [{display_name: "Claude Code", percent: 98}, {display_name: "Chats", percent: 2}]}}}}' > "$HOME/.claude.json"
}
meter() { "$ROOT/libexec/signals-meter" | jq -r "$1"; }
mv "$P" "$HOME/work-aside"   # plan tests: only the activity each test writes
saved 23 39 600
"$CLI" usage plan >/dev/null
check "usage from Claude Code's saved copy" "claude 23 39" "$(meter '"\(.account.source) \(.account.session.pct) \(.account.weekly.pct)"')"
check "  ...with how old the reading is" 1 "$(meter '.updated - .account.as_of | . >= 590 and . <= 700' | grep -c true)"
check "  ...and the per-product split" "Claude Code" "$(meter '.account.breakdown[0].name')"
saved 23 39 600 -60
check "a session that has reset since starts from zero" "0 false 39" "$(meter '"\(.account.session.pct) \(.account.session.active) \(.account.weekly.pct)"')"
check "  ...from what this Mac did since (nothing): an estimate, not 'out of date'" "true false" "$(meter '"\(.account.estimated) \(.account.outdated)"')"
check "  ...with estimates off, the reading is marked out of date" true "$(SIGNALS_NO_ESTIMATE=1 meter '.account.outdated')"
check "  ...and says so, instead of 'not started'" 1 "$(SIGNALS_NO_ESTIMATE=1 "$CLI" usage | grep -c 'reset since the last reading')"
saved 23 39 600
check "a reading inside its windows isn't out of date" false "$(meter '.account.outdated')"
saved 23 39 600
jq -nc --argjson now "$(date +%s)" '{five_hour: {used_percentage: 31, resets_at: ($now + 3000)}, seven_day: {used_percentage: 40, resets_at: ($now + 86400)}, captured_at: $now}' > "$U/limits.json"
check "a newer status line reading wins" "statusline 31" "$(meter '"\(.account.source) \(.account.session.pct)"')"

echo "usage between readings: estimated from Claude Code's activity on this Mac"
rm -f "$U/limits.json" "$U/calibration.json"
EST="$HOME/.claude/projects/-est/s1.jsonl"; mkdir -p "$(dirname "$EST")"; : > "$EST"
n=0
activity() { # seconds-ago output-tokens: one Sonnet 5.5 reply ($10 per million output tokens)
  n=$((n + 1))
  jq -nc --arg ts "$(date -u -r $(( $(date +%s) - $1 )) +%Y-%m-%dT%H:%M:%S.000Z)" --argjson o "$2" --arg id "m$n" \
    '{type: "assistant", sessionId: "s1", cwd: "/w", timestamp: $ts, requestId: "r\($id)",
      message: {id: $id, model: "claude-sonnet-5-5", usage: {input_tokens: 0, output_tokens: $o}}}' >> "$EST"
}
limit_hit() { jq -nc --arg ts "$(date -u -r $(( $(date +%s) - $1 )) +%Y-%m-%dT%H:%M:%S.000Z)" \
  '{type: "assistant", sessionId: "s1", timestamp: $ts, isApiErrorMessage: true, error: "rate_limit"}' >> "$EST"; }
saved 20 10 600
activity 900 400000
check "activity before the reading isn't counted again" "20 10 null" "$(meter '"\(.account.session.pct) \(.account.weekly.pct) \(.account.estimated)"')"
activity 300 400000
check "\$4 of activity since the reading: +10 points of a Pro 5-hour window, +1 of the week" "30 11 true" \
  "$(meter '"\(.account.session.pct) \(.account.weekly.pct) \(.account.estimated)"')"
check "  ...from the built-in Pro ratio" "2.5 0.25" "$(jq -r '"\(.k5) \(.k7)"' "$U/calibration.json")"
: > "$EST"; rm -f "$U/calibration.json"; saved 20 10 7200 -60
activity 30 400000
r=$(meter '.account.session | "\(.pct) \(.active) \(.resets_at - (now | floor))"')
check "after a reset, a new window starts with the first message" "10 true" "$(cut -d' ' -f1,2 <<<"$r")"
check "  ...and resets 5 hours after it" 1 "$(awk '{print ($3 > 17900 && $3 <= 17980)}' <<<"$r")"
: > "$EST"; rm -f "$U/calibration.json"; saved 20 10 600
activity 120 4000000
check "an estimate never says the limit was hit (99% at most)" "99 ok" "$(meter '"\(.account.session.pct) \(.status == "warn" | if . then "ok" else "no" end)"')"
limit_hit 60
check "  ...unless Claude Code was refused for a limit in that window" "100 blocked" "$(meter '"\(.account.session.pct) \(.status)"')"
: > "$EST"; rm -f "$U/calibration.json"; saved 20 10 1200; meter . >/dev/null
activity 600 400000; saved 80 10 60
check "a reading 6x off the built-in ratio (Claude used elsewhere) is ignored" "2.5 1" \
  "$(meter . >/dev/null; jq -r '"\(.k5) \(.ignored | length)"' "$U/calibration.json")"
: > "$EST"; rm -f "$U/calibration.json"; saved 20 10 1200; meter . >/dev/null
activity 600 400000; saved 32 10 60
check "  ...a close one refines it (+12 points for \$4)" 3 "$(meter . >/dev/null; jq -r .k5 "$U/calibration.json")"
: > "$EST"; rm -f "$U/calibration.json"; saved 20 10 600
jq '.oauthAccount = {organizationType: "claude_max", organizationRateLimitTier: "default_claude_max_20x"}' "$HOME/.claude.json" > "$HOME/c.json" && mv "$HOME/c.json" "$HOME/.claude.json"
check "Max 20x: a twentieth of the Pro ratio" 0.125 "$(meter . >/dev/null; jq -r .base.k5 "$U/calibration.json")"
rm -rf "$HOME/.claude/projects/-est" "$U/calibration.json"; mv "$HOME/work-aside" "$P"; saved 23 39 600; "$CLI" usage plan >/dev/null
check "  ...keeping the split only Claude Code saves" "Claude Code" "$(meter '.account.breakdown[0].name')"
jq '.captured_at -= 3600' "$U/limits.json" > "$U/l.json" && mv "$U/l.json" "$U/limits.json"
check "an older status line reading doesn't" claude "$(meter .account.source)"
rm -f "$U/limits.json"
check "Signals never opened Claude's login in the Keychain" no "$([ -f "$HOME/keychain-touched" ] && echo yes || echo no)"
check "no network calls in the meter for plan usage" 0 "$(grep -c 'oauth/usage' "$ROOT/libexec/signals-meter")"
rm -f "$HOME/bin/security" "$HOME/.claude.json"
"$CLI" usage off >/dev/null

echo "Codex: plan limits from its own session logs"
export CODEX_HOME="$HOME/codex"; mkdir -p "$CODEX_HOME/sessions/2026/10/30"
codexlog() { # session% weekly% [session-resets-in-seconds] [plan]
  local now=$(date +%s)
  { echo '{"timestamp":"2026-10-30T06:27:18.667Z","type":"session_meta","payload":{"id":"x","cwd":"/w"}}'
    jq -nc --argjson s "$1" --argjson w "$2" --argjson r5 $(( now + ${3:-3600} )) --argjson r7 $(( now + 5*86400 )) --arg plan "${4:-plus}" \
      --arg ts "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" \
      '{timestamp: $ts, type: "event_msg", payload: {type: "token_count", info: {total_token_usage: {input_tokens: 100, output_tokens: 5}},
        rate_limits: {primary: {used_percent: $s, window_minutes: 300, resets_at: $r5}, secondary: {used_percent: $w, window_minutes: 10080, resets_at: $r7},
                      plan_type: $plan}}}'; } > "$CODEX_HOME/sessions/2026/10/30/rollout-test.jsonl"
}
SIGNALS_DETECTED_PLAN=pro "$CLI" usage plan >/dev/null
codexlog 12 30
meter() { SIGNALS_DETECTED_PLAN=pro "$ROOT/libexec/signals-meter" | jq -r "$1"; }
check "Codex plan and limits read" "ChatGPT Plus 12 30 ok" "$(meter '"\(.codex.plan) \(.codex.session.pct) \(.codex.weekly.pct) \(.codex.status)"')"
codexlog 85 30;      check "  ...getting close" warn "$(meter .codex.status)"
codexlog 100 30;     check "  ...limit reached" blocked "$(meter .codex.status)"
codexlog 100 30 -60; check "  ...a session that has reset starts from zero" "0 false ok" "$(meter '"\(.codex.session.pct) \(.codex.session.active) \(.codex.status)"')"
codexlog 5 10 3600 pro; check "  ...other ChatGPT plans by name" "ChatGPT Pro" "$(meter .codex.plan)"
check "  ...shown in signals usage" 1 "$("$CLI" usage | grep -c 'Codex · ChatGPT Pro')"
check "  ...and in the text menu" 1 "$("$CLI" menu | grep -c '^Codex · ChatGPT Pro')"
check "  ...with a link to Codex's usage page" 1 "$("$CLI" menu | grep -c 'Usage on chatgpt.com')"
check "  ...never in money" 0 "$("$CLI" menu | sed -n '/^Codex/,$p' | grep -c '\$')"
touch -t 202001010000 "$CODEX_HOME/sessions/2026/10/30/rollout-test.jsonl"
check "  ...logs older than a week are ignored" null "$(meter .codex)"
"$CLI" usage off >/dev/null
codexlog 50 50;      check "  ...and nothing when usage is off" null "$(meter '.codex // null')"
rm -rf "$CODEX_HOME"; unset CODEX_HOME

echo "moving over from trafficlight and tollbooth"
M=$(mktemp -d); mkdir -p "$M/.claude" "$M/.trafficlight/sessions" "$M/.trafficlight/created" "$M/.tollbooth" "$M/.copilot/hooks"
echo '{"agent":"codex","session_id":"old1","status":"working","cwd":"/w","updated":1}' > "$M/.trafficlight/sessions/codex-old1.json"
touch "$M/.trafficlight/created/gemini"
echo '{"mode":"estimate","budget":42,"warn":0.8}' > "$M/.tollbooth/config.json"
echo '{}' > "$M/.copilot/hooks/trafficlight.json"
jq -n '{model:"opus", statusLine:{type:"command", command:"[ ! -x /x/tollbooth-statusline ] || /x/tollbooth-statusline"},
        hooks:{UserPromptSubmit:[{hooks:[{type:"command", command:"[ ! -x /x/tollbooth-gate ] || /x/tollbooth-gate"}]},
                                 {hooks:[{type:"command", command:"echo keep-me"}]}],
               Stop:[{hooks:[{type:"command", command:"[ ! -x /x ] || /x/trafficlight-hook claude Stop"}]}]}}' > "$M/.claude/settings.json"
out=$(HOME=$M "$CLI" install claude 2>&1)
check "says it moved the old setup" 1 "$(grep -c 'Moved your trafficlight and tollbooth setup' <<<"$out")"
check "sessions moved" working "$(jq -r .status "$M/.signals/sessions/codex-old1.json" 2>/dev/null)"
check "created-folder markers moved" yes "$([ -f "$M/.signals/created/gemini" ] && echo yes || echo no)"
check "usage settings moved" "estimate 42" "$(jq -r '"\(.mode) \(.budget)"' "$M/.signals/usage/config.json" 2>/dev/null)"
check "old folders removed" "no no" "$([ -d "$M/.trafficlight" ] && echo yes || echo no) $([ -d "$M/.tollbooth" ] && echo yes || echo no)"
check "tollbooth gate hook removed" 0 "$(grep -c 'tollbooth-gate' "$M/.claude/settings.json")"
check "trafficlight hooks replaced" "0 8" "$(grep -c 'trafficlight-hook' "$M/.claude/settings.json") $(jq '[.. | objects | .command? // empty | strings | select(test("signals-hook"))] | length' "$M/.claude/settings.json")"
check "status line now Signals'" 1 "$(jq -r .statusLine.command "$M/.claude/settings.json" | grep -c 'signals-statusline')"
check "other hooks and settings kept" "echo keep-me opus" "$(jq -r '"\([.hooks.UserPromptSubmit[].hooks[].command | select(. == "echo keep-me")][0]) \(.model)"' "$M/.claude/settings.json")"
check "old Copilot file removed" no "$([ -f "$M/.copilot/hooks/trafficlight.json" ] && echo yes || echo no)"
out=$(HOME=$M "$CLI" install claude 2>&1)
check "moving over happens once" 0 "$(grep -c 'Moved your' <<<"$out")"
rm -rf "$M"

echo "one product"
"$CLI" install claude >/dev/null 2>&1
check "status covers sessions, usage and the app" 3 "$("$CLI" status | grep -cE '^(Sessions|Usage|App)$')"
"$CLI" usage estimate >/dev/null
menu=$("$CLI" menu)
check "text menu: sessions then usage" "Sessions Pay as you go" "$(grep -oE '^🚦 Sessions|^Pay as you go' <<<"$menu" | sed 's/🚦 //' | tr '\n' ' ' | sed 's/ $//')"
check "text menu has no SwiftBar markup" 0 "$(grep -c ' | ' <<<"$menu")"
check "_usage_info for the window" "estimate false" "$("$CLI" _usage_info | jq -r '"\(.mode) \(.saved_keys.anthropic)"')"
check "settings without a screen shows status" 1 "$(SSH_CONNECTION=x "$CLI" settings | grep -c '^Usage$')"
check "unknown usage mode rejected" 1 "$("$CLI" usage bogus >/dev/null 2>&1; echo $?)"
check "help names every part" 1 "$("$CLI" 2>&1 | grep -c 'signals usage off')"
check "no DLP mentioned anywhere public" 0 "$(cat "$ROOT"/bin/* "$ROOT"/libexec/signals-* "$ROOT"/app/*.swift "$ROOT"/README.md 2>/dev/null | grep -ciE 'DLP|data protection|data loss')"

echo "app"
if command -v swiftc >/dev/null && [ "$(launchctl managername 2>/dev/null)" = Aqua ] && [ -f "$ROOT/app/Signals.swift" ]; then
  A="$HOME/app/Signals.app"
  "$ROOT/script/build-app" "$HOME/app" >/dev/null 2>&1
  check "menu bar app builds" yes "$([ -x "$A/Contents/MacOS/Signals" ] && echo yes || echo no)"
  check "  ...with its icon" yes "$([ -f "$A/Contents/Resources/AppIcon.icns" ] && echo yes || echo no)"
  check "  ...menu bar only (no Dock icon)" true "$(plutil -extract LSUIElement raw "$A/Contents/Info.plist")"
  check "  ...identifier" com.risestack.signals "$(plutil -extract CFBundleIdentifier raw "$A/Contents/Info.plist")"
  check "  ...handles signals:// links" signals "$(plutil -extract CFBundleURLTypes.0.CFBundleURLSchemes.0 raw "$A/Contents/Info.plist")"
  "$A/Contents/MacOS/Signals" --render "$HOME/shots" >/dev/null 2>&1
  check "  ...renders the setup pages" 1 "$([ "$(ls "$HOME/shots" 2>/dev/null | grep -cE '^[0-9]-.*\.png$')" -ge 6 ] && echo 1 || echo 0)"
  check "  ...the menu bar item in each state" 1 "$([ "$(ls "$HOME/shots" 2>/dev/null | grep -c '^bar-.*\.png$')" -ge 4 ] && echo 1 || echo 0)"
  check "  ...and the usage panel" 1 "$([ "$(ls "$HOME/shots" 2>/dev/null | grep -c '^menu-.*\.png$')" -ge 4 ] && echo 1 || echo 0)"
else
  echo "  (skipped: needs swiftc, a logged-in screen and app/Signals.swift)"
fi

echo "download counting (validated offline, with stand-ins for gh and curl)"
STUB="$HOME/stub"; mkdir -p "$STUB"
SHA=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
cat > "$STUB/gh" <<STUBEOF
#!/bin/bash
case "\$*" in
  *"releases?per_page"*) cat "$HOME/releases.json" ;;
  *traffic*) echo '{"message":"Must have push access to repository","status":"403"}'; exit 1 ;;
  *) echo "\${VISIBILITY:-public}" ;;
esac
STUBEOF
printf '#!/bin/sh\necho "%s  signals-9.9.9.tar.gz"\n' "$SHA" > "$STUB/curl"
chmod +x "$STUB/gh" "$STUB/curl"
out=$(PATH="$STUB:$PATH" "$ROOT/script/update-tap" v9.9.9 --print)
check "public: the formula downloads the release's tarball (counted by GitHub)" 1 \
  "$(grep -c 'url "https://github.com/samhudson/signals/releases/download/v9.9.9/signals-9.9.9.tar.gz"' <<<"$out")"
check "  ...with the checksum the release publishes" 1 "$(grep -c "sha256 \"$SHA\"" <<<"$out")"
# The real formula, in a throwaway local tap: update-tap rewrites it and commits.
TAPDIR="$HOME/tap.git"; git init -q --bare -b main "$TAPDIR"; W="$HOME/tapwork"; git clone -q "$TAPDIR" "$W" 2>/dev/null
mkdir -p "$W/Formula"
printf 'class Signals < Formula\n  desc "x"\n  homepage "https://github.com/samhudson/signals"\n  url "https://github.com/samhudson/signals.git",\n      tag:      "v0.0.1",\n      revision: "abc"\n  license "MIT"\nend\n' > "$W/Formula/signals.rb"
git -C "$W" add -A; git -C "$W" -c user.name=t -c user.email=t@t commit -qm init; git -C "$W" push -q origin HEAD:main 2>/dev/null
PATH="$STUB:$PATH" SIGNALS_TAP_URL="$TAPDIR" GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t "$ROOT/script/update-tap" v9.9.9 >/dev/null 2>&1
f=$(git --git-dir="$TAPDIR" show main:Formula/signals.rb)
check "  ...tap formula rewritten to the counted tarball" "1 1 0" \
  "$(grep -c 'releases/download/v9.9.9/signals-9.9.9.tar.gz' <<<"$f") $(grep -c "$SHA" <<<"$f") $(grep -c 'revision:' <<<"$f")"
check "  ...rest of the formula kept" 1 "$(grep -c '^  license "MIT"' <<<"$f")"
check "  ...and committed as the new version" "signals 9.9.9" "$(git --git-dir="$TAPDIR" log -1 --format=%s main)"
C="$HOME/clone"; git clone -q "$ROOT" "$C" 2>/dev/null; cp "$ROOT/script/update-tap" "$C/script/"; git -C "$C" tag v9.9.8
out=$(VISIBILITY=private PATH="$STUB:$PATH" "$C/script/update-tap" v9.9.8 --print)
check "private: the formula uses the git tag and commit (not counted)" "1 1" \
  "$(grep -c 'tag:      "v9.9.8"' <<<"$out") $(grep -c "revision: \"$(git -C "$C" rev-parse v9.9.8)\"" <<<"$out")"
check "release candidates never go to the tap" 1 "$(PATH="$STUB:$PATH" "$ROOT/script/update-tap" v9.9.9-rc.1 --print >/dev/null 2>&1; echo $?)"
jq -n '[{tag_name: "v1.1.0", published_at: "2026-11-02T00:00:00Z", prerelease: false,
         assets: [{name: "signals-1.1.0.tar.gz", download_count: 12}, {name: "signals-1.1.0.tar.gz.sha256", download_count: 12},
                  {name: "signals-1.1.0-linux-amd64.tar.gz", download_count: 4}, {name: "signals-1.1.0-windows-amd64.zip", download_count: 3},
                  {name: "SHA256SUMS", download_count: 9}]},
        {tag_name: "v1.1.0-rc.1", published_at: "2026-11-01T00:00:00Z", prerelease: true,
         assets: [{name: "signals-1.1.0-rc.1.tar.gz", download_count: 2}]},
        {tag_name: "v1.0.0", published_at: "2026-10-30T00:00:00Z", prerelease: false,
         assets: [{name: "signals-1.0.0.tar.gz", download_count: 30}, {name: "Signals-1.0.0.dmg", download_count: 5}]}]' > "$HOME/releases.json"
dl=$(PATH="$STUB:$PATH" "$ROOT/script/downloads" --json)
check "downloads: per release" "12 2 30" "$(jq -r '[.releases[].downloads] | join(" ")' <<<"$dl")"
check "  ...checksum fetches aren't counted" 12 "$(jq -r '.releases[0].downloads' <<<"$dl")"
check "  ...total counts final releases only" 42 "$(jq -r .total_downloads <<<"$dl")"
check "  ...installs are about half (Homebrew checks, then downloads)" "6 21" "$(jq -r '"\(.releases[0].installs) \(.total_installs)"' <<<"$dl")"
check "  ...other files (like a future .dmg) counted separately" 5 "$(jq -r '.releases[2].other' <<<"$dl")"
check "  ...works without traffic access" null "$(jq -r .views_14d <<<"$dl")"
check "  ...Linux and Windows downloads counted on their own" "4 3 0" "$(jq -r '.releases[0] | "\(.linux) \(.windows) \(.other)"' <<<"$dl")"
check "  ...and not as Homebrew installs" 12 "$(jq -r '.releases[0].downloads' <<<"$dl")"
check "  ...and prints a table" 1 "$(PATH="$STUB:$PATH" "$ROOT/script/downloads" | grep -c 'Total (final releases): 42 Homebrew downloads (about 21 installs), 4 Linux, 3 Windows')"

echo "release"
check "no em dashes in shipped files" 0 "$(cat "$ROOT"/bin/* "$ROOT"/libexec/signals-* "$ROOT"/app/*.swift "$ROOT"/README.md "$ROOT"/assets/*.svg 2>/dev/null | grep -c '—')"
check "no SwiftBar in the app or engine (the CLI mentions it only to move over)" 0 "$(cat "$ROOT"/libexec/signals-* "$ROOT"/app/*.swift 2>/dev/null | grep -ci 'swiftbar')"
check "no old product names in the app or engine" 0 "$(cat "$ROOT"/libexec/signals-* "$ROOT"/app/*.swift 2>/dev/null | grep -ciE 'tollbooth|trafficlight')"
if tag=$(git -C "$ROOT" describe --tags --exact-match 2>/dev/null); then
  base="${tag#v}"; base="${base%%-rc.*}"
  check "CLI version matches tag $tag" "$base" "$("$CLI" --version)"
fi

echo
echo "signals: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
