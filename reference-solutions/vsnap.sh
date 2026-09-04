#!/usr/bin/env bash
# vsnap.sh <course> <stage-N>
# Verifies stages 1..N against the official tester and snapshots stage N only
# if every one of them passed. Guards against a partial pass being taken for a
# success — a bare "grep -c" exits 0 whatever the count, including none.
set -euo pipefail
course="$1"; n="$2"
dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# The grep tester relocates the system grep so your program is used instead.
# On macOS /usr/bin is SIP-protected and the move fails, so hand it a writable
# copy earlier in PATH to move instead; the real binary is never touched. The
# override applies only to the tester, not to this script's own greps.
tester_path="$PATH"
if [ "$course" = "grep" ]; then
  shim="${TMPDIR:-/tmp}/byox-grep-shim"
  mkdir -p "$shim"
  if [ ! -x "$shim/grep" ]; then
    cp /usr/bin/grep "$shim/grep"
    chmod +w "$shim/grep"
  fi
  tester_path="$shim:$PATH"
fi

out=$(mktemp)
PATH="$tester_path" "$dir/verify-local.sh" "$course" "$n" >"$out" 2>&1 || true

passed=$(grep -c "Test passed" "$out" || true)
passed=${passed:-0}
if [ "$passed" != "$n" ]; then
  echo "FAIL stage $n: ${passed}/$n passed"
  grep -E "Test failed|Expected|Received|panic|error" "$out" | head -15
  exit 1
fi

"$dir/snapshot-local.sh" "$course" "$n" "$n" >/dev/null
echo "OK stage $n: $passed/$n passed, snapshotted"
