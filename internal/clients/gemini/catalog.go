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
	"time"

	"github.com/behaviorengineering/polypus/internal/clients/catalog"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

const (
	defaultOpenAIModelsURL = "https://generativelanguage.googleapis.com/v1beta/openai/models"
	modelsCacheTTL         = 10 * time.Minute
	modelsMaxBody          = 4 << 20
)

// Model is the OpenAI Models API object (bare downstream id for gateway prefixing).
type Model = catalog.Model

// ModelList is the OpenAI list response for GET /v1/models.
type ModelList = catalog.ModelList

// Client lists Gemini Developer API models via the OpenAI-compat endpoint.
type Client struct {
	listURL    string
	apiKey     string
	httpClient *http.Client
	cache      *catalog.Cache
	backendID  string             // scopes the outbound breaker to this backend.
	policies   *outbound.Policies // nil: the process default.
	log        *slog.Logger       // nil: no warnings.
}

// NewClient builds a Gemini catalog client from a backend definition.
func NewClient(b config.BackendDef) (*Client, error) {
	if !b.IsGeminiExtension() {
		return nil, derrors.Wrap(errNotGeminiExtension, derrors.CodeInvalid, "gemini", "backend").With("backend", b.ID)
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
			Transport: transport,
		},
		cache: catalog.NewCache(modelsCacheTTL),
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
		return ModelList{}, derrors.New(derrors.CodeFailedPrecondition, "gemini", "not configured")
	}
	if ctx == nil {
		return ModelList{}, derrors.New(derrors.CodeInvalid, "gemini.ListModelsStrict", "context required")
	}
	return catalog.ListStrict(ctx, catalog.Source{Provider: "gemini", Backend: c.backendID, Cache: c.cache, Fetch: c.fetch, Log: c.log})
}

func (c *Client) fetch(ctx context.Context) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.listURL, nil)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "gemini models", "request")
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.policies.Do(ctx, outbound.Scoped(outbound.DepGemini, c.backendID), c.httpClient, req)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), "gemini models", "fetch")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, modelsMaxBody))
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, "gemini models", "read")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, derrors.Wrap(nil, derrors.CodeUnauthorized, "gemini.fetch", "API key rejected").
				With("status", strconv.Itoa(resp.StatusCode))
		}
		if outbound.HTTPStatusPermanent(resp.StatusCode) {
			return nil, outbound.Abort(derrors.New(catalog.StatusCode(resp.StatusCode), "gemini models", fmt.Sprintf("status %d: %s", resp.StatusCode, catalog.Truncate(string(raw), 200))))
		}
		return nil, derrors.New(catalog.StatusCode(resp.StatusCode), "gemini models", fmt.Sprintf("status %d: %s", resp.StatusCode, catalog.Truncate(string(raw), 200)))
	}
	return parseOpenAIModelList(raw)
}

func parseOpenAIModelList(raw []byte) ([]Model, error) {
	var list ModelList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, "gemini models", "decode")
	}
	out := make([]Model, 0, len(list.Data))
	for _, m := range list.Data {
		id := normalizeModelID(m.ID)
		if id == "" {
			continue
		}
		out = append(out, Model{
			ID:      id,
			Object:  catalog.ObjectModel,
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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
