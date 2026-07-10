#!/usr/bin/env bash
# Polypus gateway entrypoint (Docker). MLX backend runs on the host (Apple Silicon), not in this image.
set -e

HOST="${POLYPUS_HOST:-127.0.0.1}"
PORT="${POLYPUS_PORT:-1320}"
BACKEND="${POLYPUS_BACKEND_URL:-http://host.docker.internal:1322}"

if [ "$#" -eq 0 ]; then
  set -- serve --host "$HOST" --port "$PORT" --backend "$BACKEND"
fi

exec polypus "$@"
