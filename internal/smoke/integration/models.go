//go:build integration

package integration

import (
	"strings"

	"github.com/behaviorengineering/polypus/internal/smoke"
)

// Default MLX and router smoke model ids (match legacy bash smoke-env.sh / Makefile).
const (
	DefaultMLXTTSModel     = "mlx-community/Qwen3-TTS-12Hz-1.7B-CustomVoice-bf16"
	DefaultMLXSTTModel     = "mlx-community/whisper-large-v3-turbo-asr-fp16"
	DefaultHiggsTTSModel   = "mlx-community/higgs-audio-v2-3B-mlx-q6"
	DefaultMLXVoice        = "vivian"
	DefaultRouterChatModel = "router/investigator"
)

// routerLeafModel is the cf_local model the investigator stage router routes to.
const routerLeafModel = "@cf/zai-org/glm-4.7-flash"

// cfLocalAllow is the cf_local allow-list every Cloudflare fixture uses: the smoke
// defaults, held once here, plus the router leaf.
func cfLocalAllow() []string {
	ids := []string{
		smoke.DefaultChatModel, routerLeafModel, smoke.DefaultBatchModel,
		smoke.DefaultTTSModel, smoke.DefaultSTTModel, smoke.DefaultSystemOneModel,
	}
	for i, id := range ids {
		ids[i] = strings.TrimPrefix(id, "cf_local/")
	}
	return ids
}
