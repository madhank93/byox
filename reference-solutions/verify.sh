#!/usr/bin/env bash
# verify.sh <course> <author-dir> <to-stage-N> [from-stage]
# Runs the official tester for stages [from..to] against author-dir.
# Each stage spawns a fresh program, so a subrange is a valid check.
set -euo pipefail
ROOT="/Volumes/work/git-repos/build-your-own-x"
course="$1"; dir="$2"; upto="$3"; from="${4:-1}"
case "$course" in
  redis) tester="$ROOT/testers/redis-tester/dist/main.out"; stages="/tmp/redis_stages.tsv";;
  http-server) tester="$ROOT/testers/http-server-tester/dist/main.out"; stages="/tmp/http_stages.tsv";;
  *) echo "unknown course"; exit 2;;
esac
# build JSON array for stages 1..upto
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
  CODECRAFTERS_TEST_CASES_JSON="$json" "$tester"
