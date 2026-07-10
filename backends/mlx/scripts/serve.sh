#!/usr/bin/env bash
# Start MLX-Audio OpenAI-compatible server on internal loopback (Polypus backend).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MLX_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
POLYPUS_ROOT="$(cd "$MLX_DIR/../.." && pwd)"
CONSILIUM_ROOT="$(cd "$POLYPUS_ROOT/../.." && pwd)"
if [[ ! -f "$CONSILIUM_ROOT/stack/.env.example" ]]; then
  CONSILIUM_ROOT=""
fi
PARENT_ROOT="$(cd "$POLYPUS_ROOT/.." && pwd)"

cd "$MLX_DIR"

if ! command -v uv >/dev/null 2>&1; then
  echo "uv not found — install: https://docs.astral.sh/uv/getting-started/installation/" >&2
  exit 1
fi

if [[ ! -d "$MLX_DIR/.venv" ]]; then
  echo "Missing .venv — run: make -C polypus mlx-sync" >&2
  exit 1
fi

if [[ -n "$CONSILIUM_ROOT" && -f "$CONSILIUM_ROOT/stack/.env" ]]; then
  set -a
  # shellcheck source=/dev/null
  source "$CONSILIUM_ROOT/stack/.env"
  set +a
elif [[ -f "$POLYPUS_ROOT/.env" ]]; then
  set -a
  # shellcheck source=/dev/null
  source "$POLYPUS_ROOT/.env"
  set +a
fi

export MLX_HOST="${POLYPUS_MLX_HOST:-${MLX_HOST:-127.0.0.1}}"
export MLX_PORT="${POLYPUS_MLX_PORT:-${MLX_PORT:-1322}}"
export MLX_MODEL="${POLYPUS_DEFAULT_MODEL:-${TTS_MODEL:-mlx-community/Qwen3-TTS-12Hz-1.7B-CustomVoice-bf16}}"

echo "Polypus MLX backend: http://${MLX_HOST}:${MLX_PORT}/v1/audio/speech"
echo "Default model (request body): ${MLX_MODEL}"

exec uv run python scripts/serve_launcher.py \
  --host "${MLX_HOST}" \
  --port "${MLX_PORT}"
