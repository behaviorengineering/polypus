#!/usr/bin/env bash
# POST a short phrase to the local TTS server; write audio to /tmp or TTS_SMOKE_OUT.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"

if [[ -f "$REPO_ROOT/stack/.env" ]]; then
  set -a
  # shellcheck source=/dev/null
  source "$REPO_ROOT/stack/.env"
  set +a
fi

HOST="${TTS_HOST:-127.0.0.1}"
PORT="${TTS_PORT:-1320}"
VOICE="${TTS_VOICE:-vivian}"
MODEL="${TTS_MODEL:-mlx-community/Qwen3-TTS-12Hz-1.7B-CustomVoice-bf16}"
FORMAT="${TTS_SMOKE_FORMAT:-mp3}"
OUT="${TTS_SMOKE_OUT:-/tmp/polypus-tts-smoke.${FORMAT}}"
TEXT="${TTS_SMOKE_TEXT:-Here is what the file shows for this episode.}"

URL="http://${HOST}:${PORT}/v1/audio/speech"

if ! curl -sf --max-time 2 "http://${HOST}:${PORT}/" >/dev/null 2>&1; then
  echo "TTS not reachable at ${HOST}:${PORT} — run: make tts-serve" >&2
  exit 1
fi

# SpeechRequest requires model in JSON (server does not take --model on the CLI).
curl -sf -X POST "$URL" \
  -H "Content-Type: application/json" \
  -d "{\"model\": \"${MODEL}\", \"input\": \"${TEXT}\", \"voice\": \"${VOICE}\", \"response_format\": \"${FORMAT}\"}" \
  --output "$OUT"

echo "Wrote ${OUT} ($(wc -c <"$OUT" | tr -d ' ') bytes)"
