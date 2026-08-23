#!/usr/bin/env bash
# Start Polypus via process-compose (standalone stack).
set -euo pipefail

POLYPUS_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$POLYPUS_DIR"

if ! command -v process-compose >/dev/null 2>&1; then
  echo "process-compose not found; install: https://f1bonacc1.github.io/process-compose/installation/" >&2
  echo "  macOS: brew install process-compose" >&2
  exit 1
fi

PARENT_MONOREPO_ROOT="$(cd "$POLYPUS_DIR/../.." && pwd)"
if [[ ! -f "$PARENT_MONOREPO_ROOT/stack/.env.example" ]]; then
  PARENT_MONOREPO_ROOT=""
fi

if [[ -n "$PARENT_MONOREPO_ROOT" && -f "$PARENT_MONOREPO_ROOT/stack/.env" ]]; then
  set -a
  # shellcheck source=/dev/null
  source "$PARENT_MONOREPO_ROOT/stack/.env"
  set +a
elif [[ -f "$POLYPUS_DIR/.env" ]]; then
  set -a
  # shellcheck source=/dev/null
  source "$POLYPUS_DIR/.env"
  set +a
fi

# shellcheck source=/dev/null
source "$POLYPUS_DIR/ports.env"

export POLYPUS_DIR
export POLYPUS_ROOT="$POLYPUS_DIR"
export PARENT_MONOREPO_ROOT
export POLYPUS_HOST="${POLYPUS_HOST:-127.0.0.1}"
export POLYPUS_PORT="${POLYPUS_PORT:-1320}"
export POLYPUS_MLX_HOST="${POLYPUS_MLX_HOST:-127.0.0.1}"
export POLYPUS_MLX_PORT="${POLYPUS_MLX_PORT:-1322}"
export INFERENCE_CLOUD_CASE="${INFERENCE_CLOUD_CASE:-0}"
export POLYPUS_CONFIG="${POLYPUS_CONFIG:-}"
export POLYPUS_BACKEND_URL="${POLYPUS_BACKEND_URL:-}"
export PHOENIX_PORT="${PHOENIX_PORT:-6006}"
export PHOENIX_OTLP_PORT="${PHOENIX_OTLP_PORT:-4317}"

POLYPUS_BIN=""
if [[ -x "$POLYPUS_DIR/bin/polypus" ]]; then
  POLYPUS_BIN="$POLYPUS_DIR/bin/polypus"
elif [[ -n "$PARENT_MONOREPO_ROOT" && -x "$PARENT_MONOREPO_ROOT/bin/polypus" ]]; then
  POLYPUS_BIN="$PARENT_MONOREPO_ROOT/bin/polypus"
fi
if [[ -z "$POLYPUS_BIN" ]]; then
  echo "missing polypus binary; run: make build" >&2
  exit 1
fi
export POLYPUS_BIN

NAMESPACES=(core)
if [[ "$INFERENCE_CLOUD_CASE" == "1" ]]; then
  if [[ -z "$POLYPUS_CONFIG" ]]; then
    if [[ -f "$POLYPUS_DIR/config.yaml" ]]; then
      export POLYPUS_CONFIG="$POLYPUS_DIR/config.yaml"
    elif [[ -f "$POLYPUS_DIR/config.cloud.yaml" ]]; then
      export POLYPUS_CONFIG="$POLYPUS_DIR/config.cloud.yaml"
    else
      export POLYPUS_CONFIG="$POLYPUS_DIR/config.cloud.yaml.example"
    fi
  fi
fi

ENABLE_MLX="${POLYPUS_ENABLE_MLX:-}"
if [[ -z "$ENABLE_MLX" ]]; then
  if [[ "$INFERENCE_CLOUD_CASE" == "1" ]]; then
    ENABLE_MLX=0
  else
    ENABLE_MLX=1
  fi
fi

if [[ "$ENABLE_MLX" == "1" ]]; then
  if [[ ! -d "$POLYPUS_DIR/backends/mlx/.venv" ]]; then
    echo "missing MLX venv; run: make mlx-sync" >&2
    exit 1
  fi
  NAMESPACES+=(mlx)
  if [[ -z "$POLYPUS_BACKEND_URL" ]]; then
    export POLYPUS_BACKEND_URL="http://${POLYPUS_MLX_HOST}:${POLYPUS_MLX_PORT}"
  fi
fi

ENABLE_PHOENIX="${POLYPUS_PHOENIX:-1}"
if [[ "$ENABLE_PHOENIX" == "1" ]]; then
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    NAMESPACES+=(obs)
  else
    echo "WARN: Docker not available; Phoenix skipped (OTLP :${PHOENIX_OTLP_PORT}). Set POLYPUS_PHOENIX=0 to silence." >&2
  fi
fi

chmod +x "$POLYPUS_DIR/scripts/"*.sh "$POLYPUS_DIR/backends/mlx/scripts/"*.sh 2>/dev/null || true

mkdir -p "$POLYPUS_DIR/.polypus"
SOCK="${PROCESS_COMPOSE_POLYPUS_SOCK:-$POLYPUS_DIR/.polypus/process-compose.sock}"
export PROCESS_COMPOSE_POLYPUS_SOCK="$SOCK"

NS_FLAGS=()
for ns in "${NAMESPACES[@]}"; do
  NS_FLAGS+=(-n "$ns")
done

echo "Polypus gateway: http://${POLYPUS_HOST}:${POLYPUS_PORT}/  namespaces: ${NAMESPACES[*]}"
if [[ " ${NAMESPACES[*]} " == *" obs "* ]]; then
  echo "Phoenix UI: http://127.0.0.1:${PHOENIX_PORT}/  OTLP gRPC: 127.0.0.1:${PHOENIX_OTLP_PORT}"
fi
echo "process-compose TUI (this project only); 0 quit."

exec process-compose up \
  --config "$POLYPUS_DIR/process-compose.yaml" \
  --shortcuts "$POLYPUS_DIR/process-compose-shortcuts.yaml" \
  -U -u "$SOCK" \
  "${NS_FLAGS[@]}" \
  "$@"
