#!/usr/bin/env bash
# Thin wrapper: multi-channel smoke prefers make smoke-all / bin/polypus-smoke.
# Kept for operators who still call scripts/smoke-systemone.sh directly.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=scripts/smoke-env.sh
source "$(cd "$(dirname "$0")" && pwd)/smoke-env.sh"
load_polypus_smoke_env

BIN="${ROOT}/bin/polypus-smoke"
if [[ ! -x "$BIN" ]]; then
  (cd "$ROOT" && go build -buildvcs=false -o "$BIN" ./cmd/polypus-smoke)
fi

HOST="${POLYPUS_HOST:-127.0.0.1}"
PORT="${POLYPUS_PORT:-1320}"
BASE="http://${HOST}:${PORT}"

if ! curl -sf --max-time 2 "${BASE}/health" >/dev/null 2>&1; then
  echo "Polypus not reachable at ${HOST}:${PORT} — run: make serve" >&2
  exit 1
fi

exec "$BIN" -base-url "$BASE" -channels systemone
