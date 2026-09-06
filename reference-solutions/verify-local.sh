#!/usr/bin/env bash
# verify-local.sh <course> <to-stage-N> [from-stage]
# verify.sh against this checkout's own reference-solutions/<course>-work.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
course="$1"; upto="$2"; from="${3:-1}"
exec "$ROOT/reference-solutions/verify.sh" "$course" "$ROOT/reference-solutions/${course}-work" "$upto" "$from"
