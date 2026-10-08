#!/usr/bin/env bash
# OpenTelemetry collector (OTLP host :4317/:4318, health :13133) for process-compose otelcol namespace.
set -euo pipefail

POLYPUS_DIR="${POLYPUS_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
cd "$POLYPUS_DIR"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker not found; otelcol is a container under Polypus" >&2
  exit 1
fi

exec docker compose -f "$POLYPUS_DIR/docker-compose.yml" up otelcol
