#!/usr/bin/env bash
# Start speech backend(s) then Polypus gateway (foreground).
# INFERENCE_CLOUD_CASE=1 → cf adapter :1323 + config.cloud.yaml (Workers AI via Polypus).
# Default → MLX backend :1322.
set -euo pipefail

POLYPUS_DIR="$(cd "$(dirname "$0")/.." && pwd)"
CONSILIUM_ROOT="$(cd "$POLYPUS_DIR/../.." && pwd)"
if [[ ! -f "$CONSILIUM_ROOT/stack/.env.example" ]]; then
  CONSILIUM_ROOT=""
fi
PARENT_ROOT="$(cd "$POLYPUS_DIR/.." && pwd)"

if [[ -n "$CONSILIUM_ROOT" && -f "$CONSILIUM_ROOT/stack/.env" ]]; then
  set -a
  # shellcheck source=/dev/null
  source "$CONSILIUM_ROOT/stack/.env"
  set +a
elif [[ -f "$POLYPUS_DIR/.env" ]]; then
  set -a
  # shellcheck source=/dev/null
  source "$POLYPUS_DIR/.env"
  set +a
fi

# shellcheck source=/dev/null
source "$POLYPUS_DIR/ports.env"

MLX_HOST="${POLYPUS_MLX_HOST:-127.0.0.1}"
MLX_PORT="${POLYPUS_MLX_PORT:-1322}"
CF_HOST="${POLYPUS_CF_HOST:-127.0.0.1}"
CF_PORT="${POLYPUS_CF_PORT:-1323}"
GATEWAY_HOST="${POLYPUS_HOST:-127.0.0.1}"
GATEWAY_PORT="${POLYPUS_PORT:-1320}"
INFERENCE_CLOUD_CASE="${INFERENCE_CLOUD_CASE:-0}"

POLYPUS_BIN=""
if [[ -x "$POLYPUS_DIR/bin/polypus" ]]; then
  POLYPUS_BIN="$POLYPUS_DIR/bin/polypus"
elif [[ -n "$CONSILIUM_ROOT" && -x "$CONSILIUM_ROOT/bin/polypus" ]]; then
  POLYPUS_BIN="$CONSILIUM_ROOT/bin/polypus"
fi

if [[ -z "$POLYPUS_BIN" ]]; then
  echo "missing polypus binary — run: make -C polypus build" >&2
  exit 1
fi

export POLYPUS_ROOT="$POLYPUS_DIR"

PIDS=()
cleanup() {
  local pid
  for pid in "${PIDS[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
  for pid in "${PIDS[@]}"; do
    wait "$pid" 2>/dev/null || true
  done
}
trap cleanup EXIT INT TERM

wait_http() {
  local url="$1"
  local label="$2"
  local tries="${3:-60}"
  echo "Waiting for ${label} at ${url} ..."
  for _ in $(seq 1 "$tries"); do
    if curl -sf --max-time 2 "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "${label} did not become ready at ${url}" >&2
  return 1
}

if [[ "$INFERENCE_CLOUD_CASE" == "1" ]]; then
  REPO="$CONSILIUM_ROOT"
  if [[ -z "$REPO" ]]; then
    REPO="$(cd "$POLYPUS_DIR/../.." && pwd)"
  fi
  CF_BIN=""
  if [[ -x "$REPO/bin/polypus-cf-adapter" ]]; then
    CF_BIN="$REPO/bin/polypus-cf-adapter"
  fi
  if [[ -z "$CF_BIN" ]]; then
    echo "missing bin/polypus-cf-adapter — run: make build (repo: $REPO)" >&2
    exit 1
  fi
  "$CF_BIN" -repo "$REPO" -addr "${CF_HOST}:${CF_PORT}" &
  PIDS+=($!)
  wait_http "http://${CF_HOST}:${CF_PORT}/health" "Cloudflare adapter"

  if [[ -z "${POLYPUS_CONFIG:-}" ]]; then
    if [[ -f "$POLYPUS_DIR/config.yaml" ]]; then
      export POLYPUS_CONFIG="$POLYPUS_DIR/config.yaml"
    elif [[ -f "$POLYPUS_DIR/config.cloud.yaml" ]]; then
      export POLYPUS_CONFIG="$POLYPUS_DIR/config.cloud.yaml"
    else
      export POLYPUS_CONFIG="$POLYPUS_DIR/config.cloud.yaml.example"
    fi
  fi
  echo "Polypus gateway (cloud): http://${GATEWAY_HOST}:${GATEWAY_PORT}/ config=${POLYPUS_CONFIG}"
  exec "$POLYPUS_BIN" serve --host "$GATEWAY_HOST" --port "$GATEWAY_PORT"
fi

chmod +x "$POLYPUS_DIR/backends/mlx/scripts/serve.sh"
"$POLYPUS_DIR/backends/mlx/scripts/serve.sh" &
PIDS+=($!)
wait_http "http://${MLX_HOST}:${MLX_PORT}/" "MLX backend" 120

echo "Polypus gateway (local MLX): http://${GATEWAY_HOST}:${GATEWAY_PORT}/"
exec "$POLYPUS_BIN" serve --host "$GATEWAY_HOST" --port "$GATEWAY_PORT" \
  --backend "http://${MLX_HOST}:${MLX_PORT}"
