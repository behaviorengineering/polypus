#!/bin/sh
# Wait for Polypus-generated routes.toml, then run switchyard-server.
set -eu

if [ "$#" -gt 0 ]; then
  case "$1" in
    sh|/bin/sh|bash|/bin/bash)
      exec "$@"
      ;;
    switchyard-server)
      shift
      exec switchyard-server "$@"
      ;;
    *)
      exec switchyard-server "$@"
      ;;
  esac
fi

CONFIG="${SWITCHYARD_CONFIG:-/var/lib/polypus/switchyard/routes.toml}"
HOST="${SWITCHYARD_HOST:-0.0.0.0}"
PORT="${SWITCHYARD_PORT:-4000}"
WAIT_SECS="${SWITCHYARD_WAIT_SECS:-300}"

i=0
while [ ! -f "$CONFIG" ]; do
  i=$((i + 1))
  if [ "$i" -ge "$WAIT_SECS" ]; then
    echo "switchyard entrypoint: timed out waiting for $CONFIG" >&2
    exit 1
  fi
  sleep 1
done

switchyard-server --config "$CONFIG" --dry-run
exec switchyard-server --config "$CONFIG" --host "$HOST" --port "$PORT"
