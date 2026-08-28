#!/usr/bin/env bash
# Switchyard routing server for Polypus named routers (process-compose).
set -euo pipefail

if [[ -z "${POLYPUS_BIN:-}" || ! -x "${POLYPUS_BIN}" ]]; then
  echo "missing polypus binary (POLYPUS_BIN=${POLYPUS_BIN:-unset})" >&2
  exit 1
fi

HOST="${POLYPUS_SWITCHYARD_HOST:-127.0.0.1}"
PORT="${POLYPUS_SWITCHYARD_PORT:-4000}"

SWITCHYARD_BIN="${POLYPUS_DIR}/bin/switchyard-server"
if [[ ! -x "$SWITCHYARD_BIN" ]]; then
  SWITCHYARD_BIN=""
fi

CONFIG_PATH="$("$POLYPUS_BIN" switchyard-render --print-path)"

if [[ -n "$SWITCHYARD_BIN" ]]; then
  "$SWITCHYARD_BIN" --config "$CONFIG_PATH" --dry-run
  if [[ -n "${POLYPUS_OTLP_ENDPOINT:-}" ]]; then
    export OTEL_EXPORTER_OTLP_ENDPOINT="$POLYPUS_OTLP_ENDPOINT"
  fi
  exec "$SWITCHYARD_BIN" --config "$CONFIG_PATH" --host "$HOST" --port "$PORT"
fi

MANIFEST="${POLYPUS_DIR}/providers/switchyard/Cargo.toml"
if ! command -v cargo >/dev/null 2>&1; then
  echo "switchyard-server not found in bin/ and cargo unavailable; run: make switchyard-build" >&2
  exit 1
fi

cargo run --release -p switchyard-server --manifest-path "$MANIFEST" -- \
  --config "$CONFIG_PATH" --dry-run

if [[ -n "${POLYPUS_OTLP_ENDPOINT:-}" ]]; then
  export OTEL_EXPORTER_OTLP_ENDPOINT="$POLYPUS_OTLP_ENDPOINT"
fi

exec cargo run --release -p switchyard-server --manifest-path "$MANIFEST" -- \
  --config "$CONFIG_PATH" --host "$HOST" --port "$PORT"
