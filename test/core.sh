#!/bin/bash
# The session and Signals tests, against the Go engine (core/) instead of the shell
# scripts: a throwaway copy of the repo with libexec/signals-hook and
# libexec/signals-state calling `signals-core`, then the same test/sessions.sh.
#   ./test/core.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
(cd "$ROOT/core" && go build -o "$WORK/signals-core" ./cmd/signals-core)
mkdir -p "$WORK/repo"
cp -R "$ROOT/." "$WORK/repo/"
# The program lives outside libexec/ (the tests scan libexec/ for wording),
# next to a link to the budget gate, which the hook runs before a prompt.
mkdir -p "$WORK/core"; mv "$WORK/signals-core" "$WORK/core/signals-core"
ln -s "$WORK/repo/libexec/signals-gate" "$WORK/core/signals-gate"
printf '#!/bin/bash\nexec "%s" hook "$@"\n' "$WORK/core/signals-core" > "$WORK/repo/libexec/signals-hook"
printf '#!/bin/bash\nexec "%s" state "$@"\n' "$WORK/core/signals-core" > "$WORK/repo/libexec/signals-state"
chmod +x "$WORK/repo/libexec/signals-hook" "$WORK/repo/libexec/signals-state"
failed=0
for suite in sessions signals; do
  echo "━━ $suite (Go engine)"
  "$WORK/repo/test/$suite.sh" || failed=1
done
exit $failed
