#!/usr/bin/env bash
# snapshot-local.sh <course> <from-N> <to-N>
# snapshot.sh from this checkout's own reference-solutions/<course>-work.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
course="$1"; from="$2"; to="$3"
exec "$ROOT/reference-solutions/snapshot.sh" "$course" "$ROOT/reference-solutions/${course}-work/app/main.go" "$from" "$to"
