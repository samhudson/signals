#!/bin/bash
# Live check of the lights against a real Claude Code session (not run in CI:
# it uses a little of your plan). It starts `claude` in a folder you already
# trust, then acts like a person: asks for a file (Claude asks permission,
# amber), presses Esc (green, interrupted), asks for a long answer (red),
# presses Esc mid-answer (green, interrupted). The light is read every second.
#
#   test/live-claude.sh ~/code/some-trusted-folder
#
# Claude Code runs with a clean environment, as if started in Terminal (a
# session started from inside another Claude Code session behaves differently).
# Nothing in the folder changes: the file write is cancelled at the prompt.
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIR="$(cd "${1:?usage: test/live-claude.sh <a folder you trust in Claude Code>}" && pwd)"
command -v claude >/dev/null && command -v expect >/dev/null || { echo "needs claude and expect"; exit 1; }
OUT="$(mktemp -d)"
CLAUDE="$(command -v claude)"

cat > "$OUT/drive.exp" <<EOF
set timeout 40
log_file -noappend $OUT/claude.log
cd $DIR
spawn env -i HOME=\$env(HOME) USER=\$env(USER) LOGNAME=\$env(USER) SHELL=/bin/zsh TERM=xterm-256color LANG=en_US.UTF-8 PATH=$(dirname "$CLAUDE"):/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin claude --permission-mode default
expect { -re "(?i)for shortcuts|\\\\? for" {} -re "(?i)trust this folder" { exit 3 } timeout {} }
sleep 3
send -- "Use the Write tool to create a file named signals-live-check.txt containing the word hello."
sleep 1; send "\r"
expect { -re "(?i)do you want to" {} timeout {} }
sleep 5
exec touch $OUT/esc1
send "\033"
sleep 8
send -- "Write a very long, detailed essay about the history of traffic lights, at least 2000 words."
sleep 1; send "\r"
expect { -re "(?i)esc to interrupt" {} timeout {} }
sleep 6
exec touch $OUT/esc2
send "\033"
sleep 8
send -- "/exit"
sleep 1; send "\r"
expect eof
EOF

start=$(date +%s)
expect "$OUT/drive.exp" >/dev/null 2>&1 &
pid=$!
echo "Driving a Claude Code session in $DIR (about 2 minutes)…"
while kill -0 $pid 2>/dev/null; do
  f=""
  for x in $(ls -t "$HOME"/.signals/sessions/claude-*.json 2>/dev/null); do
    [ "$(jq -r .cwd "$x")" = "$DIR" ] && [ "$(jq -r '.updated // 0' "$x")" -ge "$start" ] && { f="$x"; break; }
  done
  ev=""; for m in esc1 esc2; do [ -f "$OUT/$m" ] && [ ! -f "$OUT/$m.seen" ] && { ev="$m"; touch "$OUT/$m.seen"; }; done
  light="-"
  [ -n "$f" ] && light=$("$ROOT/libexec/signals-state" | jq -r --arg s "$(jq -r .session_id "$f")" '.rows[] | select(.session_id == $s) | "\(.status)\(if .detail then "(\(.detail))" else "" end)"')
  echo "$(( $(date +%s) - start )) ${light:--} $ev" >> "$OUT/timeline"
  sleep 1
done
[ -f "$OUT/esc1" ] || { echo "Claude Code didn't get to the first prompt. See $OUT/claude.log"; exit 1; }

pass=0 fail=0
check() { if [ "$2" = yes ]; then pass=$((pass+1)); printf '  \033[32m✓\033[0m %s\n' "$1"; else fail=$((fail+1)); printf '  \033[31m✗\033[0m %s\n' "$1"; fi; }
t1=$(awk '$3 == "esc1" { print $1 }' "$OUT/timeline"); t2=$(awk '$3 == "esc2" { print $1 }' "$OUT/timeline")
seen() { awk -v from="$1" -v to="$2" -v want="$3" '$1 >= from && $1 <= to && $2 ~ want { found = 1 } END { print (found ? "yes" : "no") }' "$OUT/timeline"; }
check "asking permission shows amber" "$(seen 0 "$t1" '^waiting')"
check "Esc on the permission prompt shows green within 5 seconds" "$(seen "$t1" $((t1 + 5)) '^done')"
check "a new prompt shows red" "$(seen "$t1" "$t2" '^working')"
check "Esc mid-answer shows green within 5 seconds" "$(seen "$t2" $((t2 + 5)) '^done')"
echo
echo "Timeline (seconds, light): $OUT/timeline"
awk '{ if ($2 != last || $3 != "") print "  " $0; last = $2 }' "$OUT/timeline"
echo
echo "live: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
