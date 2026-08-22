#!/usr/bin/env bash
# Polypus gateway process for process-compose.
set -euo pipefail

if [[ -z "${POLYPUS_BIN:-}" || ! -x "${POLYPUS_BIN}" ]]; then
  echo "missing polypus binary (POLYPUS_BIN=${POLYPUS_BIN:-unset})" >&2
  exit 1
fi

HOST="${POLYPUS_HOST:-127.0.0.1}"
PORT="${POLYPUS_PORT:-1320}"
ARGS=(serve --host "$HOST" --port "$PORT")
if [[ -n "${POLYPUS_BACKEND_URL:-}" ]]; then
  ARGS+=(--backend "${POLYPUS_BACKEND_URL}")
fi

exec "$POLYPUS_BIN" "${ARGS[@]}"
