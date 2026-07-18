#!/usr/bin/env bash
# snapshot.sh <course> <src-main.go> <from-N> <to-N>
set -euo pipefail
course="$1"; src="$2"; from="$3"; to="$4"
case "$course" in redis) tsv=/tmp/redis_stages.tsv;; http-server) tsv=/tmp/http_stages.tsv;; esac
while IFS=$'\t' read -r n slug name; do
  num=$((10#$n))
  { [ "$num" -lt "$from" ] || [ "$num" -gt "$to" ]; } && continue
  d="reference-solutions/${course}/${n}-${slug}"
  mkdir -p "$d"; /bin/cp "$src" "$d/main.go"
done < "$tsv"
echo "snapshotted $course stages $from..$to"
