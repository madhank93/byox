#!/usr/bin/env bash
# vsnap.sh <course> <stage-N>
# Verifies stages 1..N against the official tester and snapshots stage N only
# if every one of them passed. Guards against a partial pass being taken for a
# success — grep -c returns 0 whatever the count.
set -euo pipefail
course="$1"; n="$2"
dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out=$(mktemp)
"$dir/verify-local.sh" "$course" "$n" >"$out" 2>&1 || true
passed=$(grep -c "Test passed" "$out" || true)
if [ "$passed" -ne "$n" ]; then
  echo "FAIL stage $n: $passed/$n passed"
  grep -E "Test failed|expected|Expected|Received|error" "$out" | head -15
  exit 1
fi
"$dir/snapshot-local.sh" "$course" "$n" "$n" >/dev/null
echo "OK stage $n: $passed/$n passed, snapshotted"
