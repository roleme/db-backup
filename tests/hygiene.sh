#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
pattern='/home/[a-z]|/Users/[A-Za-z]|op://|[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}|(^|[^0-9.])(10\.[0-9]+\.[0-9]+\.[0-9]+|192\.168\.[0-9]+\.[0-9]+|172\.(1[6-9]|2[0-9]|3[01])\.[0-9]+\.[0-9]+)|\.(local|lan)([^a-z]|$)|ts\.net'
if [ -n "${DBB_HYGIENE_EXTRA:-}" ]; then
  pattern="$pattern|$DBB_HYGIENE_EXTRA"
fi
grep -rnEi "$pattern" "$ROOT" \
  --exclude-dir=.git --exclude=.git --exclude-dir=.superpowers \
  --exclude=hygiene.sh --exclude=90_public_hygiene.sh --exclude=LICENSE.upstream || true
