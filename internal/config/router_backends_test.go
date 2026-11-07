package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func writeRouterFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// assertNoBackendsRefused checks a config file without backends fails with
// CodeInvalid and a message that names the file.
func assertNoBackendsRefused(t *testing.T, path string) {
	t.Helper()
	_, err := LoadRouterConfig(ServeOptions{ConfigPath: path})
	if err == nil {
		t.Fatal("a config file with no backends must fail, not fall back to the env default")
	}
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("code %s, want %s: %v", derrors.CodeOf(err), derrors.CodeInvalid, err)
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "has no backends") {
		t.Fatalf("error should name the file and say it has no backends: %v", err)
	}
}

func TestLoadRouterConfigFileWithoutBackendsFails(t *testing.T) {
	path := writeRouterFile(t, `
chat_backend:
  enabled: true
  default: cf_local
routers:
  fast:
    capability: chat
    route:
      type: passthrough
      target: cf_local/@cf/a
`)
	assertNoBackendsRefused(t, path)
}

func TestLoadRouterConfigEmptyBackendsFails(t *testing.T) {
	assertNoBackendsRefused(t, writeRouterFile(t, "backends: {}\n"))
}

// noConfigFile leaves discovery nothing to find: no POLYPUS_CONFIG, no user
// config, no $POLYPUS_ROOT and a working directory without config.yaml.
func noConfigFile(t *testing.T) {
	t.Helper()
	t.Setenv("POLYPUS_CONFIG", "")
	t.Setenv("POLYPUS_ROOT", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
}

func TestLoadRouterConfigNoFileUsesEnvDefault(t *testing.T) {
	noConfigFile(t)
	cfg, err := LoadRouterConfig(ServeOptions{BackendURL: "http://127.0.0.1:1322/"})
	if err != nil {
		t.Fatal(err)
	}
	b, ok := cfg.Backends[mlxLocalID]
	if len(cfg.Backends) != 1 || !ok {
		t.Fatalf("backends %v, want only %s", cfg.BackendIDs(), mlxLocalID)
	}
	if b.BaseURL != "http://127.0.0.1:1322" {
		t.Fatalf("base url %q", b.BaseURL)
	}
}

func TestSwitchyardEnvRejectsUnknownWord(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "disable")
	noConfigFile(t)
	opts := ServeOptions{BackendURL: "http://127.0.0.1:1322"}
	_, err := LoadRouterConfig(opts)
	if err == nil {
		t.Fatal("POLYPUS_SWITCHYARD=disable must fail config load")
	}
	if derrors.CodeOf(err) != derrors.CodeInvalid || !strings.Contains(err.Error(), "POLYPUS_SWITCHYARD") {
		t.Fatalf("want CodeInvalid naming POLYPUS_SWITCHYARD, got %s: %v", derrors.CodeOf(err), err)
	}
	for _, word := range []string{"1", "true", "yes", "on", "0", "false", "no", "off", ""} {
		t.Setenv("POLYPUS_SWITCHYARD", word)
		if _, err := LoadRouterConfig(opts); err != nil {
			t.Errorf("POLYPUS_SWITCHYARD=%q: %v", word, err)
		}
	}
}

// TestLoadRouterConfigExplicitPathMustExist keeps --config strict: only discovery
// may find no file and fall back to the env default.
func TestLoadRouterConfigExplicitPathMustExist(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	_, err := LoadRouterConfig(ServeOptions{ConfigPath: missing})
	if err == nil {
		t.Fatal("a --config path that does not exist must fail the load")
	}
	if derrors.CodeOf(err) != derrors.CodeInvalid || !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), missing) {
		t.Fatalf("want CodeInvalid naming the missing file, got %s: %v", derrors.CodeOf(err), err)
	}
}

func TestLoadRouterConfigNoFileDefaultsMLXURL(t *testing.T) {
	noConfigFile(t)
	cfg, err := LoadRouterConfig(ServeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Backends[mlxLocalID].BaseURL; got != "http://127.0.0.1:1322" {
		t.Fatalf("no file and no backend URL: mlx_local %q, want the default MLX port", got)
	}
}

// clearMLXEnv unsets every variable LoadServeOptions reads the MLX backend from.
func clearMLXEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"POLYPUS_BACKEND_URL", "POLYPUS_MLX_URL", "POLYPUS_MLX_HOST", "POLYPUS_MLX_PORT"} {
		t.Setenv(k, "")
	}
}

// TestYAMLMLXBaseURLStandsWithoutOperatorOverride keeps a config's
// backends.mlx_local.base_url when the operator set no MLX variable and no
// --backend, and replaces it when they did.
func TestYAMLMLXBaseURLStandsWithoutOperatorOverride(t *testing.T) {
	path := writeRouterFile(t, `
tts_backend:
  enabled: true
  default: mlx_local
backends:
  mlx_local:
    base_url: http://127.0.0.1:9999
    capabilities: [tts, stt, voices]
`)
	load := func(t *testing.T) string {
		t.Helper()
		opts, err := LoadServeOptions()
		if err != nil {
			t.Fatal(err)
		}
		opts.ConfigPath = path
		cfg, err := LoadRouterConfig(opts)
		if err != nil {
			t.Fatal(err)
		}
		return cfg.Backends[mlxLocalID].BaseURL
	}

	clearMLXEnv(t)
	if got := load(t); got != "http://127.0.0.1:9999" {
		t.Fatalf("no MLX env: mlx_local %q, want the config's http://127.0.0.1:9999", got)
	}
	for env, want := range map[string]string{
		"POLYPUS_BACKEND_URL": "http://10.0.0.5:1322",
		"POLYPUS_MLX_URL":     "http://10.0.0.6:1322",
		"POLYPUS_MLX_PORT":    "http://127.0.0.1:2000",
		"POLYPUS_MLX_HOST":    "http://10.0.0.7:1322",
	} {
		t.Run(env, func(t *testing.T) {
			clearMLXEnv(t)
			switch env {
			case "POLYPUS_MLX_PORT":
				t.Setenv(env, "2000")
			case "POLYPUS_MLX_HOST":
				t.Setenv(env, "10.0.0.7")
			default:
				t.Setenv(env, want+"/")
			}
			if got := load(t); got != want {
				t.Fatalf("%s set: mlx_local %q, want %q", env, got, want)
			}
		})
	}
}

// TestConfigErrorsNameTheSameEntryEveryRun checks validation walks backends and
// routers in sorted order, so with two broken entries every run names the first.
func TestConfigErrorsNameTheSameEntryEveryRun(t *testing.T) {
	for name, tc := range map[string]struct {
		content, want string
	}{
		"backends": {
			content: `
backends:
  zz_bad:
    base_url: http://127.0.0.1:9
    capabilities: [chatt]
  aa_bad:
    base_url: http://127.0.0.1:9
    capabilities: [chatt]
`,
			want: "backends.aa_bad",
		},
		"routers": {
			content: `
chat_backend:
  enabled: true
  default: b
backends:
  b:
    base_url: http://127.0.0.1:9
    capabilities: [chat, embed]
routers:
  zz_router:
    capability: embed
    route:
      type: passthrough
      target: b/m
  aa_router:
    capability: embed
    route:
      type: passthrough
      target: b/m
`,
			want: "routers.aa_router",
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := writeRouterFile(t, tc.content)
			for range 20 {
				_, err := LoadRouterConfig(ServeOptions{ConfigPath: path})
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("want an error naming %s, got %v", tc.want, err)
				}
			}
		})
	}
}
