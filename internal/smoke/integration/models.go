//go:build integration

package integration

// Default MLX and router smoke model ids (match legacy bash smoke-env.sh / Makefile).
const (
	DefaultMLXTTSModel     = "mlx-community/Qwen3-TTS-12Hz-1.7B-CustomVoice-bf16"
	DefaultMLXSTTModel     = "mlx-community/whisper-large-v3-turbo-asr-fp16"
	DefaultHiggsTTSModel   = "mlx-community/higgs-audio-v2-3B-mlx-q6"
	DefaultMLXVoice        = "vivian"
	DefaultRouterChatModel = "router/investigator"
)
