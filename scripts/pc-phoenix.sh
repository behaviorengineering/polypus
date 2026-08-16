#!/usr/bin/env bash
# Arize Phoenix container (OTLP :4317, UI :6006) for process-compose obs namespace.
set -euo pipefail

POLYPUS_DIR="${POLYPUS_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
cd "$POLYPUS_DIR"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker not found; Phoenix is a container under Polypus" >&2
  exit 1
fi

exec docker compose -f "$POLYPUS_DIR/docker-compose.yml" up phoenix
