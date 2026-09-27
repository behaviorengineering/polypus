#!/usr/bin/env bash
# Polypus gateway entrypoint (Docker). Cloudflare/compose deploy uses config.yaml + CF_* env.
# Optional POLYPUS_BACKEND_URL for MLX-on-host dev only.
set -e

HOST="${POLYPUS_HOST:-0.0.0.0}"
PORT="${POLYPUS_PORT:-1320}"

if [ "$#" -eq 0 ]; then
  ARGS=(serve --host "$HOST" --port "$PORT")
  if [ -n "${POLYPUS_BACKEND_URL:-}" ]; then
    ARGS+=(--backend "$POLYPUS_BACKEND_URL")
  fi
  set -- "${ARGS[@]}"
fi

exec polypus "$@"
