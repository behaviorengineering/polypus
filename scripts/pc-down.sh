#!/usr/bin/env bash
# Stop the Polypus process-compose project only (not Consilium make serve).
set -euo pipefail

POLYPUS_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SOCK="${PROCESS_COMPOSE_POLYPUS_SOCK:-$POLYPUS_DIR/.polypus/process-compose.sock}"

if ! command -v process-compose >/dev/null 2>&1; then
  echo "process-compose not installed" >&2
  exit 1
fi

if [[ ! -S "$SOCK" ]]; then
  echo "Polypus process-compose is not running ($SOCK)"
  exit 0
fi

exec process-compose down -U -u "$SOCK"
