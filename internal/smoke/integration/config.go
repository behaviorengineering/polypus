//go:build integration

package integration

import (
	"fmt"
	"os"
	"strings"
)

func writeHermeticConfig(path string, cfBaseV1 string) error {
	cfBaseV1 = strings.TrimRight(strings.TrimSpace(cfBaseV1), "/")
	content := fmt.Sprintf(`processes:
  mlx: false

chat_backend:
  enabled: true
  default: cf_local
tts_backend:
  enabled: true
  default: cf_local
stt_backend:
  enabled: true
  default: cf_local
proxy_backend:
  enabled: true
  default: cf_local
systemone_backend:
  enabled: true
  default: cf_local
batch_backend:
  enabled: true
  default: cf_local

timeouts:
  min: 5s
  max: 900s
  chat: 60s
  speech: 180s
  backends:
    cf_local:
      chat: 60s

backends:
  cf_local:
    remote: true
    extension: cloudflare
    base_url: %s
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat, tts, stt, voices, systemone, batch]
    models:
      sync: false
      allow:
        - "@cf/ibm-granite/granite-4.0-h-micro"
        - "@cf/zai-org/glm-4.7-flash"
        - "@cf/google/gemma-4-26b-a4b-it"
        - "@cf/deepgram/aura-2-en"
        - "@cf/deepgram/nova-3"
        - "typesafe/jev"
`, cfBaseV1)
	return os.WriteFile(path, []byte(content), 0o600)
}

func writeLiveConfig(path string, accountID string) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return fmt.Errorf("integration: CF_ACCOUNT_ID required for live config")
	}
	base := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/ai/v1", accountID)
	content := fmt.Sprintf(`processes:
  mlx: false

chat_backend:
  enabled: true
  default: cf_local
tts_backend:
  enabled: true
  default: cf_local
stt_backend:
  enabled: true
  default: cf_local
proxy_backend:
  enabled: true
  default: cf_local
systemone_backend:
  enabled: true
  default: cf_local
batch_backend:
  enabled: true
  default: cf_local

timeouts:
  min: 5s
  max: 900s
  chat: 120s
  speech: 180s
  backends:
    cf_local:
      chat: 120s

backends:
  cf_local:
    remote: true
    extension: cloudflare
    base_url: %s
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat, tts, stt, voices, systemone, batch]
    models:
      sync: false
      allow:
        - "@cf/ibm-granite/granite-4.0-h-micro"
        - "@cf/zai-org/glm-4.7-flash"
        - "@cf/google/gemma-4-26b-a4b-it"
        - "@cf/deepgram/aura-2-en"
        - "@cf/deepgram/nova-3"
        - "typesafe/jev"
`, base)
	return os.WriteFile(path, []byte(content), 0o600)
}

func writeMlxConfig(path string, mlxBaseURL string) error {
	mlxBaseURL = strings.TrimRight(strings.TrimSpace(mlxBaseURL), "/")
	content := fmt.Sprintf(`processes:
  mlx: false

chat_backend:
  enabled: false
tts_backend:
  enabled: true
  default: mlx_local
stt_backend:
  enabled: true
  default: mlx_local
proxy_backend:
  enabled: true
  default: mlx_local

timeouts:
  min: 5s
  max: 900s
  speech: 180s

backends:
  mlx_local:
    base_url: %s
    capabilities: [tts, stt, voices]
    models:
      sync: false
      allow:
        - %s
        - %s
        - %s
`, mlxBaseURL, DefaultMLXTTSModel, DefaultMLXSTTModel, DefaultHiggsTTSModel)
	return os.WriteFile(path, []byte(content), 0o600)
}

func writeRouterConfig(path string, cfBaseV1, switchyardBase, mlxBaseURL, switchyardConfigPath string) error {
	cfBaseV1 = strings.TrimRight(strings.TrimSpace(cfBaseV1), "/")
	switchyardBase = strings.TrimRight(strings.TrimSpace(switchyardBase), "/")
	mlxBaseURL = strings.TrimRight(strings.TrimSpace(mlxBaseURL), "/")
	content := fmt.Sprintf(`processes:
  mlx: false

chat_backend:
  enabled: true
  default: cf_local
tts_backend:
  enabled: true
  default: mlx_local
stt_backend:
  enabled: true
  default: mlx_local
proxy_backend:
  enabled: true
  default: mlx_local

switchyard:
  base_url: %s
  config_path: %s

timeouts:
  min: 5s
  max: 900s
  chat: 60s
  speech: 180s
  backends:
    cf_local:
      chat: 60s

backends:
  mlx_local:
    base_url: %s
    capabilities: [tts, stt, voices]
    models:
      sync: false
      allow:
        - %s
  cf_local:
    remote: true
    extension: cloudflare
    base_url: %s
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat, tts, stt, voices, systemone, batch]
    models:
      sync: false
      allow:
        - "@cf/ibm-granite/granite-4.0-h-micro"
        - "@cf/zai-org/glm-4.7-flash"
        - "@cf/google/gemma-4-26b-a4b-it"
        - "@cf/deepgram/aura-2-en"
        - "@cf/deepgram/nova-3"
        - "typesafe/jev"

routers:
  investigator:
    capability: chat
    route:
      type: stage_router
      picker: efficient_first
      confidence_threshold: 0.5
      capable: cf_local/@cf/zai-org/glm-4.7-flash
      efficient: cf_local/@cf/zai-org/glm-4.7-flash
`, switchyardBase, switchyardConfigPath, mlxBaseURL, DefaultMLXTTSModel, cfBaseV1)
	return os.WriteFile(path, []byte(content), 0o600)
}
