package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
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
	defaultModelsURL = "https://api.anthropic.com/v1/models"
	apiVersion       = "2023-06-01"
	modelsPageLimit  = 1000
	modelsMaxPages   = 20
	modelsCacheTTL   = 10 * time.Minute
	modelsMaxBody    = 4 << 20
)

// Model is the OpenAI Models API object (bare downstream id for gateway prefixing).
type Model = catalog.Model

// ModelList is the OpenAI list response for GET /v1/models.
type ModelList = catalog.ModelList

// anthropicModelPage is one page of the Anthropic Models API.
type anthropicModelPage struct {
	Data []struct {
		ID        string `json:"id"`
		CreatedAt string `json:"created_at"`
	} `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

// Client lists Claude models from the Anthropic Models API.
type Client struct {
	listURL    string
	apiKey     string
	httpClient *http.Client
	cache      *catalog.Cache
	backendID  string             // scopes the outbound breaker to this backend.
	policies   *outbound.Policies // nil: the process default.
	log        *slog.Logger       // nil: no warnings.
}

// NewClient builds an Anthropic catalog client from a backend definition.
func NewClient(b config.BackendDef) (*Client, error) {
	if !b.IsAnthropicExtension() {
		return nil, derrors.Wrap(errNotAnthropicExtension, derrors.CodeInvalid, "anthropic", "backend").With("backend", b.ID)
	}
	token, err := b.Auth.ResolveBearerToken()
	if err != nil {
		return nil, err
	}
	return newClient(modelsListURL(b.BaseURL), token)
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

// modelsListURL resolves the Models API URL from the backend base_url (empty uses the default host).
func modelsListURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return defaultModelsURL
	}
	if strings.HasSuffix(base, "/v1/models") {
		return base
	}
	return strings.TrimSuffix(base, "/v1") + "/v1/models"
}

// ListModelsStrict fetches the catalog or returns an error when fetch fails and no cache exists.
func (c *Client) ListModelsStrict(ctx context.Context) (ModelList, error) {
	if c == nil {
		return ModelList{}, derrors.New(derrors.CodeFailedPrecondition, "anthropic", "not configured")
	}
	if ctx == nil {
		return ModelList{}, derrors.New(derrors.CodeInvalid, "anthropic.ListModelsStrict", "context required")
	}
	return catalog.ListStrict(ctx, catalog.Source{Provider: "anthropic", Backend: c.backendID, Cache: c.cache, Fetch: c.fetchAll, Log: c.log})
}

// fetchAll follows after_id pagination until has_more is false.
func (c *Client) fetchAll(ctx context.Context) ([]Model, error) {
	var out []Model
	afterID := ""
	for page := 0; page < modelsMaxPages; page++ {
		p, err := c.fetchPage(ctx, afterID)
		if err != nil {
			return nil, err
		}
		for _, m := range p.Data {
			id := strings.TrimSpace(m.ID)
			if id == "" {
				continue
			}
			out = append(out, Model{
				ID:      id,
				Object:  catalog.ObjectModel,
				Created: createdUnix(m.CreatedAt),
				OwnedBy: "anthropic",
			})
		}
		if !p.HasMore || strings.TrimSpace(p.LastID) == "" {
			return out, nil
		}
		afterID = p.LastID
	}
	return nil, derrors.New(derrors.CodeUnavailable, "anthropic models", fmt.Sprintf("more than %d pages", modelsMaxPages))
}

func (c *Client) fetchPage(ctx context.Context, afterID string) (anthropicModelPage, error) {
	u, err := url.Parse(c.listURL)
	if err != nil {
		return anthropicModelPage{}, derrors.Wrap(err, derrors.CodeInternal, "anthropic models", "list url")
	}
	q := u.Query()
	q.Set("limit", strconv.Itoa(modelsPageLimit))
	if afterID != "" {
		q.Set("after_id", afterID)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return anthropicModelPage{}, derrors.Wrap(err, derrors.CodeInternal, "anthropic models", "request")
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", apiVersion)
	req.Header.Set("Accept", "application/json")

	resp, err := c.policies.Do(ctx, outbound.Scoped(outbound.DepAnthropic, c.backendID), c.httpClient, req)
	if err != nil {
		return anthropicModelPage{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), "anthropic models", "fetch")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, modelsMaxBody))
	if err != nil {
		return anthropicModelPage{}, derrors.Wrap(err, derrors.CodeUnavailable, "anthropic models", "read")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return anthropicModelPage{}, derrors.Wrap(nil, derrors.CodeUnauthorized, "anthropic.fetchPage", "API key rejected").
				With("status", strconv.Itoa(resp.StatusCode))
		}
		if outbound.HTTPStatusPermanent(resp.StatusCode) {
			return anthropicModelPage{}, outbound.Abort(derrors.New(catalog.StatusCode(resp.StatusCode), "anthropic models", fmt.Sprintf("status %d: %s", resp.StatusCode, catalog.Truncate(string(raw), 200))))
		}
		return anthropicModelPage{}, derrors.New(catalog.StatusCode(resp.StatusCode), "anthropic models", fmt.Sprintf("status %d: %s", resp.StatusCode, catalog.Truncate(string(raw), 200)))
	}
	var page anthropicModelPage
	if err := json.Unmarshal(raw, &page); err != nil {
		return anthropicModelPage{}, derrors.Wrap(err, derrors.CodeUnavailable, "anthropic models", "decode")
	}
	return page, nil
}

// createdUnix converts the RFC 3339 created_at to Unix seconds (0 when absent or malformed).
func createdUnix(s string) int64 {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return t.Unix()
}
