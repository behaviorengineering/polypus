#!/usr/bin/env bash
# HyperDX / ClickStack local container (OTLP host :4319/:4318, UI :8080).
# Phoenix keeps OTLP :4317 for OpenInference; app traces use HyperDX ports.
set -euo pipefail

POLYPUS_DIR="${POLYPUS_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
cd "$POLYPUS_DIR"

if ! command -v docker >/dev/null 2>&1; then
  echo "docker not found; HyperDX is a container under Polypus" >&2
  exit 1
fi

exec docker compose -f "$POLYPUS_DIR/docker-compose.yml" up hyperdx
