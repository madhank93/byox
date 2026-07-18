#!/usr/bin/env bash
# snapshot-local.sh <course> <from-N> <to-N>
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
course="$1"; from="$2"; to="$3"
src="$ROOT/reference-solutions/${course}-work/app/main.go"
tsv="/tmp/${course}_stages.tsv"
while IFS=$'\t' read -r n slug name; do
  num=$((10#$n))
  { [ "$num" -lt "$from" ] || [ "$num" -gt "$to" ]; } && continue
  d="$ROOT/reference-solutions/${course}/${n}-${slug}"
  mkdir -p "$d"; /bin/cp "$src" "$d/main.go"
done < "$tsv"
echo "snapshotted $course stages $from..$to"
