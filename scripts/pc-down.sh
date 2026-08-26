#!/usr/bin/env bash
# Stop the Polypus process-compose project only.
set -euo pipefail

if [[ -z "${PROCESS_COMPOSE_POLYPUS_SOCK:-}" ]]; then
  if [[ -n "${XDG_STATE_HOME:-}" ]]; then
    PROCESS_COMPOSE_POLYPUS_SOCK="${XDG_STATE_HOME}/polypus/process-compose.sock"
  else
    PROCESS_COMPOSE_POLYPUS_SOCK="${HOME}/.local/state/polypus/process-compose.sock"
  fi
fi
SOCK="$PROCESS_COMPOSE_POLYPUS_SOCK"

if ! command -v process-compose >/dev/null 2>&1; then
  echo "process-compose not installed" >&2
  exit 1
fi

if [[ ! -S "$SOCK" ]]; then
  echo "Polypus process-compose is not running ($SOCK)"
  exit 0
fi

exec process-compose down -U -u "$SOCK"
