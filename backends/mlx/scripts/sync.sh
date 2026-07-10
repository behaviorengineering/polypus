#!/usr/bin/env bash
# Install / refresh MLX backend uv environment (polypus/backends/mlx/.venv).
set -euo pipefail

MLX_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$MLX_DIR"

if ! command -v uv >/dev/null 2>&1; then
  echo "uv not found — install: https://docs.astral.sh/uv/getting-started/installation/" >&2
  exit 1
fi

if [[ "$(uname -s)" != "Darwin" ]] || [[ "$(uname -m)" != "arm64" ]]; then
  echo "WARN: mlx-audio targets Apple Silicon; this host may not be supported." >&2
fi

uv sync "$@"

echo "MLX venv: ${MLX_DIR}/.venv"
echo "Start full stack: make -C polypus serve"
