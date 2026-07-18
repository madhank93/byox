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
# Some testers (e.g. shell-tester) override $HOME per test run to catch
# HOME-dependent bugs. Go's toolchain/module caches default to paths under
# $HOME, so without pinning them explicitly here, every invocation would
# look like a fresh machine and re-download the pinned Go toolchain over
# and over — and since these testers drive the program over a PTY, that
# download message gets interleaved with real output and breaks the
# expect-style assertions. Pin them to this machine's real cache so they
# stay put regardless of what $HOME the tester sets for the child process.
CODECRAFTERS_REPOSITORY_DIR="$dir" CODECRAFTERS_SUBMISSION_DIR="$dir" \
  CODECRAFTERS_TEST_CASES_JSON="$json" TESTER_DIR="$ROOT/testers/${course}-tester" \
  GOPATH="$(go env GOPATH)" GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)" \
  "$tester"
