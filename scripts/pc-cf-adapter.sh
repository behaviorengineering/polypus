#!/usr/bin/env bash
# Cloudflare loopback adapter process for process-compose (cloud namespace).
set -euo pipefail

POLYPUS_DIR="${POLYPUS_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
REPO="${CONSILIUM_ROOT:-}"
if [[ -z "$REPO" || ! -f "$REPO/stack/.env.example" ]]; then
  echo "cf-adapter needs a Consilium repo (CONSILIUM_ROOT with stack/.env.example)" >&2
  exit 1
fi

CF_BIN=""
if [[ -x "$REPO/bin/polypus-cf-adapter" ]]; then
  CF_BIN="$REPO/bin/polypus-cf-adapter"
fi
if [[ -z "$CF_BIN" ]]; then
  echo "missing bin/polypus-cf-adapter; run: make build (Polypus or Consilium: $REPO)" >&2
  exit 1
fi

HOST="${POLYPUS_CF_HOST:-127.0.0.1}"
PORT="${POLYPUS_CF_PORT:-1323}"
exec "$CF_BIN" -repo "$REPO" -addr "${HOST}:${PORT}"
