package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/observability"
)

const modelsTimeout = 30 * time.Second
const pingTimeout = 5 * time.Second
const modelsCacheTTL = 10 * time.Minute
const modelsPerPage = 100
const modelsMaxBody = 8 << 20

var accountIDRE = regexp.MustCompile(`/accounts/([^/]+)/`)

// Model is the OpenAI Models API object.
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

// Client implements Cloudflare Workers AI extension hooks for Polypus.
type Client struct {
	apiBase      string
	apiKey       string
	httpClient   *http.Client // catalog / ping (short timeout)
	speechClient *http.Client // /run TTS/STT (shared transport; timeout via ctx)

	mu       sync.Mutex
	cache    []Model
	cacheAt  time.Time
	cacheTTL time.Duration
}

// NewClient builds an uncached Cloudflare extension client from a remote backend
// definition. Prefer GetClient in production so catalog/health/speech share a
// process-scoped client (and transport after speech-client wiring).
func NewClient(b config.BackendDef) (*Client, error) {
	if !b.IsCloudflareExtension() {
		return nil, fmt.Errorf("%w: %q", errNotCloudflareExtension, b.ID)
	}
	token, err := b.Auth.ResolveBearerToken()
	if err != nil {
		return nil, err
	}
	apiBase, err := AIBaseURL(b.BaseURL)
	if err != nil {
		return nil, err
	}
	return newClient(apiBase, token)
}

func newClient(apiBase, token string) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errAPIKeyRequired
	}
	transport := observability.WrapTransport(http.DefaultTransport)
	return &Client{
		apiBase: apiBase,
		apiKey:  token,
		httpClient: &http.Client{
			Timeout:   modelsTimeout,
			Transport: transport,
		},
		// Timeout 0: hop deadline comes from context (gateway ResolveSpeech).
		speechClient: &http.Client{
			Timeout:   0,
			Transport: transport,
		},
		cacheTTL: modelsCacheTTL,
	}, nil
}

// Ping checks Cloudflare Model Search reachability (used by gateway health).
func (c *Client) Ping(ctx context.Context) error {
	if c == nil {
		return fmt.Errorf("cloudflare: not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	_, _, err := c.fetchPage(ctx, 1)
	return err
}

// ListModelsStrict fetches the catalog or returns an error when the dial fails
// and no in-memory cache is available.
//
// Stale-cache fallback: on fetch failure, a previously successful catalog is
// returned with a nil error (so circuit breakers treat it as success) and a
// warning is logged. Operators should treat that as degraded inventory, not a
// fresh sync. With no cache, the dial error is returned.
func (c *Client) ListModelsStrict(ctx context.Context) (ModelList, error) {
	if c == nil {
		return ModelList{}, fmt.Errorf("cloudflare: not configured")
	}
	models, err := c.fetchAll(ctx)
	if err != nil {
		if cached, ok := c.cached(); ok {
			slog.Warn("cloudflare: models catalog fetch failed; using stale cache",
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

// ListModels returns OpenAI-shaped models (ids are bare @cf/... for Polypus to prefix).
// Fetch failures with no cache yield an empty list; prefer ListModelsStrict when
// the caller must distinguish errors from an empty catalog.
func (c *Client) ListModels(ctx context.Context) ModelList {
	list, err := c.ListModelsStrict(ctx)
	if err != nil {
		slog.Warn("cloudflare: models catalog fetch failed; empty list", "err", err)
		return ModelList{Object: "list", Data: nil}
	}
	return list
}

// GetModel returns one model by id or false when missing.
func (c *Client) GetModel(ctx context.Context, id string) (Model, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Model{}, false
	}
	list := c.ListModels(ctx)
	for _, m := range list.Data {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

func (c *Client) fetchAll(ctx context.Context) ([]Model, error) {
	byID := make(map[string]Model)
	page := 1
	for {
		pageModels, totalPages, err := c.fetchPage(ctx, page)
		if err != nil {
			return nil, err
		}
		for _, m := range pageModels {
			byID[m.ID] = m
		}
		if totalPages <= 0 || page >= totalPages {
			break
		}
		page++
		if page > 50 {
			break
		}
	}
	out := make([]Model, 0, len(byID))
	for _, m := range byID {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

type cfModelsSearchResponse struct {
	Success    bool                 `json:"success"`
	Result     []cfModelSearchEntry `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		PerPage    int `json:"per_page"`
		TotalCount int `json:"total_count"`
		TotalPages int `json:"total_pages"`
		Count      int `json:"count"`
	} `json:"result_info"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type cfModelSearchEntry struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

func (c *Client) fetchPage(ctx context.Context, page int) ([]Model, int, error) {
	base := strings.TrimRight(c.apiBase, "/")
	u, err := url.Parse(base + "/models/search")
	if err != nil {
		return nil, 0, fmt.Errorf("cloudflare models: parse url: %w", err)
	}
	q := u.Query()
	q.Set("page", fmt.Sprintf("%d", page))
	q.Set("per_page", fmt.Sprintf("%d", modelsPerPage))
	q.Set("hide_experimental", "true")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("cloudflare models: request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("cloudflare models: fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, modelsMaxBody))
	if err != nil {
		return nil, 0, fmt.Errorf("cloudflare models: read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("cloudflare models: status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var body cfModelsSearchResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, 0, fmt.Errorf("cloudflare models: decode: %w", err)
	}
	if !body.Success {
		msg := "models search failed"
		if len(body.Errors) > 0 && strings.TrimSpace(body.Errors[0].Message) != "" {
			msg = body.Errors[0].Message
		}
		return nil, 0, fmt.Errorf("cloudflare models: %s", msg)
	}
	out := make([]Model, 0, len(body.Result))
	for _, e := range body.Result {
		id := normalizeWorkersAIModelName(e.Name)
		if id == "" {
			id = normalizeWorkersAIModelName(e.ID)
		}
		if id == "" {
			continue
		}
		out = append(out, Model{
			ID:      id,
			Object:  "model",
			Created: 0,
			OwnedBy: "cloudflare",
		})
	}
	totalPages := body.ResultInfo.TotalPages
	if totalPages <= 0 && len(body.Result) >= modelsPerPage {
		totalPages = page + 1
	}
	if totalPages <= 0 {
		totalPages = page
	}
	return out, totalPages, nil
}

func normalizeWorkersAIModelName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = NormalizeModel(name)
	if strings.HasPrefix(name, "@") {
		return name
	}
	if strings.Contains(name, "/") && !strings.HasPrefix(name, "cf_local/") {
		return "@cf/" + strings.TrimPrefix(name, "cf/")
	}
	return name
}

func (c *Client) cached() ([]Model, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) == 0 || c.cacheAt.IsZero() {
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
	c.cache = make([]Model, len(models))
	copy(c.cache, models)
	c.cacheAt = time.Now()
}
