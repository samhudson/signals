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
CORE="$WORK/signals-core"
cp "$CORE" "$WORK/repo/libexec/signals-core"
printf '#!/bin/bash\nexec "$(dirname "$0")/signals-core" hook "$@"\n' > "$WORK/repo/libexec/signals-hook"
printf '#!/bin/bash\nexec "$(dirname "$0")/signals-core" state "$@"\n' > "$WORK/repo/libexec/signals-state"
chmod +x "$WORK/repo/libexec/signals-hook" "$WORK/repo/libexec/signals-state"
failed=0
for suite in sessions signals; do
  echo "━━ $suite (Go engine)"
  "$WORK/repo/test/$suite.sh" || failed=1
done
exit $failed
