#!/usr/bin/env bash
# verify-local.sh <course> <to-stage-N> [from-stage]
# Runs the official tester for stages [from..to] against reference-solutions/<course>-work.
# Local variant of verify.sh: uses this checkout's actual paths instead of hardcoded /Volumes/...
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
course="$1"; upto="$2"; from="${3:-1}"
dir="$ROOT/reference-solutions/${course}-work"
tester="$ROOT/testers/${course}-tester/dist/main.out"
stages="/tmp/${course}_stages.tsv"

json="["
first=1
while IFS=$'\t' read -r n slug name; do
  num=$((10#$n))
  [ "$num" -gt "$upto" ] && break
  [ "$num" -lt "$from" ] && continue
  [ $first -eq 0 ] && json+=","
  json+="{\"slug\":\"$slug\",\"tester_log_prefix\":\"stage-$num\",\"title\":\"Stage #$num: $name\"}"
  first=0
done < "$stages"
json+="]"
CODECRAFTERS_REPOSITORY_DIR="$dir" CODECRAFTERS_SUBMISSION_DIR="$dir" \
  CODECRAFTERS_TEST_CASES_JSON="$json" TESTER_DIR="$ROOT/testers/${course}-tester" \
  "$tester"
