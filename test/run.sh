#!/bin/bash
# Every test: Sessions, Usage, and Signals as one product. Each suite runs in
# its own throwaway $HOME.
#   ./test/run.sh
cd "$(dirname "$0")"
failed=0
for suite in sessions usage signals ledger; do
  echo "━━ $suite"
  ./$suite.sh || failed=1
  echo
done
[ $failed = 0 ] && echo "All suites passed." || echo "Some tests failed."
exit $failed
