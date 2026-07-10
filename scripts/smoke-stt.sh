#!/usr/bin/env bash
# Round-trip smoke: TTS via gateway, then STT on the same audio file.
set -euo pipefail

POLYPUS_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PARENT_ROOT="$(cd "$POLYPUS_DIR/.." && pwd)"

if [[ -f "$PARENT_ROOT/stack/.env.example" ]]; then
  CONSILIUM_ROOT="$PARENT_ROOT"
else
  CONSILIUM_ROOT=""
fi

if [[ -n "$CONSILIUM_ROOT" && -f "$CONSILIUM_ROOT/stack/.env" ]]; then
  set -a
  # shellcheck source=/dev/null
  source "$CONSILIUM_ROOT/stack/.env"
  set +a
elif [[ -f "$POLYPUS_DIR/.env" ]]; then
  set -a
  # shellcheck source=/dev/null
  source "$POLYPUS_DIR/.env"
  set +a
fi

# shellcheck source=/dev/null
source "$POLYPUS_DIR/ports.env"

HOST="${POLYPUS_HOST:-127.0.0.1}"
PORT="${POLYPUS_PORT:-1320}"
VOICE="${POLYPUS_DEFAULT_VOICE:-vivian}"
TTS_MODEL="${POLYPUS_DEFAULT_MODEL:-mlx-community/Qwen3-TTS-12Hz-1.7B-CustomVoice-bf16}"
STT_MODEL="${POLYPUS_DEFAULT_STT_MODEL:-mlx-community/whisper-large-v3-turbo-asr-fp16}"
FORMAT="${POLYPUS_SMOKE_FORMAT:-mp3}"
AUDIO_OUT="${POLYPUS_STT_SMOKE_AUDIO:-/tmp/polypus-stt-smoke.${FORMAT}}"
TEXT="${POLYPUS_STT_SMOKE_TEXT:-Here is what the file shows for this episode.}"
MIN_CHARS="${POLYPUS_STT_SMOKE_MIN_CHARS:-8}"

BASE="http://${HOST}:${PORT}"

if ! curl -sf --max-time 2 "${BASE}/health" >/dev/null 2>&1; then
  echo "Polypus not reachable at ${HOST}:${PORT} — run: make polypus-serve" >&2
  exit 1
fi

curl -sf -X POST "${BASE}/v1/audio/speech" \
  -H "Content-Type: application/json" \
  -d "{\"model\": \"${TTS_MODEL}\", \"input\": \"${TEXT}\", \"voice\": \"${VOICE}\", \"response_format\": \"${FORMAT}\"}" \
  --output "$AUDIO_OUT"

echo "TTS wrote ${AUDIO_OUT} ($(wc -c <"$AUDIO_OUT" | tr -d ' ') bytes)"

TRANSCRIPT="$(curl -sf -X POST "${BASE}/v1/audio/transcriptions" \
  -F "file=@${AUDIO_OUT}" \
  -F "model=${STT_MODEL}" \
  -F "response_format=json")"

echo "STT transcript: ${TRANSCRIPT}"

if ! python3 -c "import json,sys; t=json.loads(sys.argv[1]).get('text',''); sys.exit(0 if len(t.strip())>=int(sys.argv[2]) else 1)" "$TRANSCRIPT" "$MIN_CHARS" 2>/dev/null; then
  echo "STT smoke failed: transcript too short or invalid JSON" >&2
  exit 1
fi

echo "STT smoke ok (model ${STT_MODEL})"
