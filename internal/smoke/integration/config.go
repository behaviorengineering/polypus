//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/template"
)

// fixtureTemplates holds the config fixtures. The cf_local and mlx_local backend
// blocks are written once and shared.
const fixtureTemplates = `
{{define "cf_local"}}  cf_local:
    remote: true
    extension: cloudflare
    base_url: {{quote .CFBase}}
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat, tts, stt, voices, systemone, batch]
    models:
      sync: false
      allow:
{{- range .CFAllow}}
        - {{quote .}}
{{- end}}
{{end}}
{{define "mlx_local"}}  mlx_local:
    base_url: {{quote .MLXBase}}
    capabilities: [tts, stt, voices]
    models:
      sync: false
      allow:
{{- range .MLXAllow}}
        - {{quote .}}
{{- end}}
{{end}}
{{define "cf"}}processes:
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
  chat: {{.ChatTimeout}}
  speech: 180s
  backends:
    cf_local:
      chat: {{.ChatTimeout}}

backends:
{{template "cf_local" .}}{{end}}
{{define "mlx"}}processes:
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
{{template "mlx_local" .}}{{end}}
{{define "router"}}processes:
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
  base_url: {{quote .SwitchyardBase}}
  config_path: {{quote .SwitchyardConfig}}

timeouts:
  min: 5s
  max: 900s
  chat: {{.ChatTimeout}}
  speech: 180s
  backends:
    cf_local:
      chat: {{.ChatTimeout}}

backends:
{{template "mlx_local" .}}{{template "cf_local" .}}
routers:
  investigator:
    capability: chat
    route:
      type: stage_router
      picker: efficient_first
      confidence_threshold: 0.5
      capable: {{quote .RouterLeaf}}
      efficient: {{quote .RouterLeaf}}
{{end}}`

// quote writes s as a JSON string, which YAML reads as a double-quoted scalar.
func quote(s string) (string, error) {
	b, err := json.Marshal(s)
	return string(b), err
}

var fixtures = template.Must(template.New("fixtures").Funcs(template.FuncMap{"quote": quote}).Parse(fixtureTemplates))

// fixture is the data every config fixture reads; each fills what its template uses.
type fixture struct {
	CFBase           string
	CFAllow          []string
	MLXBase          string
	MLXAllow         []string
	SwitchyardBase   string
	SwitchyardConfig string
	ChatTimeout      string
	RouterLeaf       string
}

func writeFixture(path, name string, data fixture) error {
	var b strings.Builder
	if err := fixtures.ExecuteTemplate(&b, name, data); err != nil {
		return fmt.Errorf("integration: %s config: %w", name, err)
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func trimURL(u string) string {
	return strings.TrimRight(strings.TrimSpace(u), "/")
}

func writeHermeticConfig(path string, cfBaseV1 string) error {
	return writeFixture(path, "cf", fixture{CFBase: trimURL(cfBaseV1), CFAllow: cfLocalAllow(), ChatTimeout: "60s"})
}

func writeLiveConfig(path string, accountID string) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return fmt.Errorf("integration: CF_ACCOUNT_ID required for live config")
	}
	base := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/ai/v1", accountID)
	return writeFixture(path, "cf", fixture{CFBase: base, CFAllow: cfLocalAllow(), ChatTimeout: "120s"})
}

func writeMlxConfig(path string, mlxBaseURL string) error {
	return writeFixture(path, "mlx", fixture{
		MLXBase:  trimURL(mlxBaseURL),
		MLXAllow: []string{DefaultMLXTTSModel, DefaultMLXSTTModel, DefaultHiggsTTSModel},
	})
}

func writeRouterConfig(path string, cfBaseV1, switchyardBase, mlxBaseURL, switchyardConfigPath string) error {
	return writeFixture(path, "router", fixture{
		CFBase:           trimURL(cfBaseV1),
		CFAllow:          cfLocalAllow(),
		MLXBase:          trimURL(mlxBaseURL),
		MLXAllow:         []string{DefaultMLXTTSModel},
		SwitchyardBase:   trimURL(switchyardBase),
		SwitchyardConfig: switchyardConfigPath,
		ChatTimeout:      "60s",
		RouterLeaf:       "cf_local/" + routerLeafModel,
	})
}
