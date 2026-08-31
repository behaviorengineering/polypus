#!/usr/bin/env bash
# Shared env loading and speech defaults for Polypus smoke scripts.
# Source from scripts/smoke.sh and scripts/smoke-stt.sh (do not execute directly).

smoke_cf_gateway_model() {
  local raw="${1:-}"
  raw="${raw#cf_local/}"
  if [[ "$raw" == @cf/* ]]; then
    echo "cf_local/${raw}"
  else
    echo "cf_local/@cf/${raw}"
  fi
}

load_polypus_smoke_env() {
  POLYPUS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  PARENT_ROOT="$(cd "$POLYPUS_DIR/.." && pwd)"

  if [[ -f "$PARENT_ROOT/stack/.env.example" ]]; then
    PARENT_MONOREPO_ROOT="$PARENT_ROOT"
  else
    PARENT_MONOREPO_ROOT=""
  fi

  if [[ -n "$PARENT_MONOREPO_ROOT" && -f "$PARENT_MONOREPO_ROOT/stack/.env" ]]; then
    set -a
    # shellcheck source=/dev/null
    source "$PARENT_MONOREPO_ROOT/stack/.env"
    set +a
  elif [[ -f "$POLYPUS_DIR/.env" ]]; then
    set -a
    # shellcheck source=/dev/null
    source "$POLYPUS_DIR/.env"
    set +a
  fi

  # shellcheck source=/dev/null
  source "$POLYPUS_DIR/ports.env"
}

# Default speech smokes to cf_local. Set POLYPUS_SMOKE_LOCAL=1 for MLX (make smoke-local).
# Explicit POLYPUS_DEFAULT_* / POLYPUS_CF_* overrides still win when already set.
apply_smoke_speech_defaults() {
  if [[ "${POLYPUS_SMOKE_LOCAL:-0}" == "1" ]]; then
    if [[ -z "${POLYPUS_DEFAULT_VOICE:-}" ]]; then
      export POLYPUS_DEFAULT_VOICE=vivian
    fi
    if [[ -z "${POLYPUS_DEFAULT_MODEL:-}" ]]; then
      export POLYPUS_DEFAULT_MODEL=mlx-community/Qwen3-TTS-12Hz-1.7B-CustomVoice-bf16
    fi
    if [[ -z "${POLYPUS_DEFAULT_STT_MODEL:-}" ]]; then
      export POLYPUS_DEFAULT_STT_MODEL=mlx-community/whisper-large-v3-turbo-asr-fp16
    fi
    return 0
  fi
  if [[ -z "${POLYPUS_DEFAULT_VOICE:-}" ]]; then
    export POLYPUS_DEFAULT_VOICE="${POLYPUS_CF_VOICE:-luna}"
  fi
  if [[ -z "${POLYPUS_DEFAULT_MODEL:-}" ]]; then
    POLYPUS_DEFAULT_MODEL="$(smoke_cf_gateway_model "${POLYPUS_CF_TTS_MODEL:-@cf/deepgram/aura-2-en}")"
    export POLYPUS_DEFAULT_MODEL
  fi
  if [[ -z "${POLYPUS_DEFAULT_STT_MODEL:-}" ]]; then
    POLYPUS_DEFAULT_STT_MODEL="$(smoke_cf_gateway_model "${POLYPUS_CF_STT_MODEL:-@cf/deepgram/nova-3}")"
    export POLYPUS_DEFAULT_STT_MODEL
  fi
}
