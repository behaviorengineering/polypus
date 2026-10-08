#!/usr/bin/env bash
# HyperDX / ClickStack local container (UI :8080; OTLP internal only, ingest via otelcol on host :4317/:4318).
set -euo pipefail

POLYPUS_DIR="${POLYPUS_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
cd "$POLYPUS_DIR"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker not found; HyperDX is a container under Polypus" >&2
  exit 1
fi

exec docker compose -f "$POLYPUS_DIR/docker-compose.yml" up hyperdx
