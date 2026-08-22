#!/usr/bin/env bash
# POST a short phrase to the Polypus gateway; write audio to /tmp or POLYPUS_SMOKE_OUT.
set -euo pipefail

# shellcheck source=scripts/smoke-env.sh
source "$(cd "$(dirname "$0")" && pwd)/smoke-env.sh"
load_polypus_smoke_env
apply_smoke_speech_defaults

HOST="${POLYPUS_HOST:-${TTS_HOST:-127.0.0.1}}"
PORT="${POLYPUS_PORT:-${TTS_PORT:-1320}}"
VOICE="${POLYPUS_DEFAULT_VOICE:-${TTS_VOICE:-vivian}}"
MODEL="${POLYPUS_DEFAULT_MODEL:-${TTS_MODEL:-mlx-community/Qwen3-TTS-12Hz-1.7B-CustomVoice-bf16}}"
FORMAT="${POLYPUS_SMOKE_FORMAT:-${TTS_SMOKE_FORMAT:-mp3}}"
OUT="${POLYPUS_SMOKE_OUT:-${TTS_SMOKE_OUT:-/tmp/polypus-smoke.${FORMAT}}}"
TEXT="${POLYPUS_SMOKE_TEXT:-${TTS_SMOKE_TEXT:-Here is what the file shows for this episode.}}"

BASE="http://${HOST}:${PORT}"
URL="${BASE}/v1/audio/speech"

if ! curl -sf --max-time 2 "${BASE}/health" >/dev/null 2>&1; then
  if ! curl -sf --max-time 2 "${BASE}/" >/dev/null 2>&1; then
    echo "Polypus not reachable at ${HOST}:${PORT} — run: make polypus-serve" >&2
    exit 1
  fi
fi

curl -sf -X POST "$URL" \
  -H "Content-Type: application/json" \
  -d "{\"model\": \"${MODEL}\", \"input\": \"${TEXT}\", \"voice\": \"${VOICE}\", \"response_format\": \"${FORMAT}\"}" \
  --output "$OUT"

echo "Wrote ${OUT} ($(wc -c <"$OUT" | tr -d ' ') bytes)"
