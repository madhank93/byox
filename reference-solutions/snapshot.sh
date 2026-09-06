#!/usr/bin/env bash
# snapshot.sh <course> <src-main.go> <from-N> <to-N>
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
course="$1"; src="$2"; from="$3"; to="$4"
tsv="$ROOT/reference-solutions/stages/${course}.tsv"
[ -f "$tsv" ] || { echo "no stage table at $tsv"; exit 2; }
while IFS=$'\t' read -r n slug name; do
  num=$((10#$n))
  { [ "$num" -lt "$from" ] || [ "$num" -gt "$to" ]; } && continue
  d="$ROOT/reference-solutions/${course}/${n}-${slug}"
  mkdir -p "$d"; /bin/cp "$src" "$d/main.go"
done < "$tsv"
echo "snapshotted $course stages $from..$to"
