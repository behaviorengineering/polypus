package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

const (
	defaultOpenAIModelsURL = "https://generativelanguage.googleapis.com/v1beta/openai/models"
	modelsTimeout          = 30 * time.Second
	modelsCacheTTL         = 10 * time.Minute
	modelsMaxBody          = 4 << 20
)

// Model is the OpenAI Models API object (bare downstream id for gateway prefixing).
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelList is the OpenAI list response for GET /v1/models.
type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// Client lists Gemini Developer API models via the OpenAI-compat endpoint.
type Client struct {
	listURL    string
	apiKey     string
	httpClient *http.Client

	mu       sync.Mutex
	cache    []Model
	cacheAt  time.Time
	cacheTTL time.Duration
}

// NewClient builds a Gemini catalog client from a backend definition.
func NewClient(b config.BackendDef) (*Client, error) {
	if !b.IsGeminiExtension() {
		return nil, fmt.Errorf("%w: %q", errNotGeminiExtension, b.ID)
	}
	token, err := b.Auth.ResolveBearerToken()
	if err != nil {
		return nil, err
	}
	return newClient(openAIModelsListURL(b.BaseURL), token)
}

// NewClientWithListURL builds a catalog client for a fixed list URL (tests and injected getters).
func NewClientWithListURL(listURL, token string) (*Client, error) {
	return newClient(listURL, token)
}

func newClient(listURL, token string) (*Client, error) {
	listURL = strings.TrimSpace(listURL)
	if listURL == "" {
		return nil, errListModelsURLRequired
	}
	if strings.TrimSpace(token) == "" {
		return nil, errAPIKeyRequired
	}
	transport := observability.WrapTransport(http.DefaultTransport)
	return &Client{
		listURL: listURL,
		apiKey:  token,
		httpClient: &http.Client{
			Timeout:   modelsTimeout,
			Transport: transport,
		},
		cacheTTL: modelsCacheTTL,
	}, nil
}

// openAIModelsListURL resolves the upstream OpenAI-compat models list URL.
// Chat inference keeps empty base_url; inventory uses this URL only.
func openAIModelsListURL(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return defaultOpenAIModelsURL
	}
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/openai/models") {
		return base
	}
	if strings.HasSuffix(base, "/v1beta") {
		return base + "/openai/models"
	}
	return base + "/v1beta/openai/models"
}

// ListModelsStrict fetches the catalog or returns an error when fetch fails and no cache exists.
func (c *Client) ListModelsStrict(ctx context.Context) (ModelList, error) {
	if c == nil {
		return ModelList{}, fmt.Errorf("gemini: not configured")
	}
	if ctx == nil {
		return ModelList{}, derrors.New(derrors.CodeInvalid, "gemini.ListModelsStrict", "context required")
	}
	if cached, ok := c.cached(); ok {
		return ModelList{Object: "list", Data: cached}, nil
	}
	models, err := c.fetch(ctx)
	if err != nil {
		if cached, ok := c.cached(); ok {
			slog.Warn("gemini: models catalog fetch failed; using stale cache",
				"err", err,
				"cached_models", len(cached),
			)
			return ModelList{Object: "list", Data: cached}, nil
		}
		return ModelList{Object: "list"}, err
	}
	c.storeCache(models)
	return ModelList{Object: "list", Data: models}, nil
}

func (c *Client) fetch(ctx context.Context) ([]Model, error) {
	ctx, cancel := outbound.WithDeadlineIfMissing(ctx, modelsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gemini models: request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := outbound.Do(ctx, outbound.DepGemini, c.httpClient, req)
	if err != nil {
		return nil, fmt.Errorf("gemini models: fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, modelsMaxBody))
	if err != nil {
		return nil, fmt.Errorf("gemini models: read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, derrors.Wrap(nil, derrors.CodeUnauthorized, "gemini.fetch", "API key rejected").
				With("status", strconv.Itoa(resp.StatusCode))
		}
		if outbound.HTTPStatusPermanent(resp.StatusCode) {
			return nil, outbound.Abort(fmt.Errorf("gemini models: status %d: %s", resp.StatusCode, truncate(string(raw), 200)))
		}
		return nil, fmt.Errorf("gemini models: status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	return parseOpenAIModelList(raw)
}

func parseOpenAIModelList(raw []byte) ([]Model, error) {
	var list ModelList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("gemini models: decode: %w", err)
	}
	out := make([]Model, 0, len(list.Data))
	for _, m := range list.Data {
		id := normalizeModelID(m.ID)
		if id == "" {
			continue
		}
		out = append(out, Model{
			ID:      id,
			Object:  "model",
			Created: m.Created,
			OwnedBy: firstNonEmpty(m.OwnedBy, "google"),
		})
	}
	return out, nil
}

func normalizeModelID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "models/")
	return strings.TrimSpace(id)
}

func (c *Client) cached() ([]Model, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) == 0 {
		return nil, false
	}
	if time.Since(c.cacheAt) > c.cacheTTL {
		return nil, false
	}
	out := make([]Model, len(c.cache))
	copy(out, c.cache)
	return out, true
}

func (c *Client) storeCache(models []Model) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = append([]Model(nil), models...)
	c.cacheAt = time.Now()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
