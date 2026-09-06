#!/usr/bin/env bash
# verify.sh <course> <author-dir> <to-stage-N> [from-stage]
# Runs the official tester for stages [from..to] against author-dir.
# Each stage spawns a fresh program, so a subrange is a valid check.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
course="$1"; dir="$2"; upto="$3"; from="${4:-1}"

tester="$ROOT/testers/${course}-tester/dist/main.out"
stages="$ROOT/reference-solutions/stages/${course}.tsv"
[ -x "$tester" ] || { echo "no tester binary at $tester — build it with: (cd testers/${course}-tester && go build -o dist/main.out ./cmd/tester)"; exit 2; }
[ -f "$stages" ] || { echo "no stage table at $stages"; exit 2; }

# build JSON array for stages from..upto
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

# Some testers read fixtures relative to their own repo root (bittorrent's
# torrents/ directory, for one), so run from there. dir and tester are absolute.
cd "$ROOT/testers/${course}-tester"

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
