package anthropic

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/clients/catalog"
	"github.com/behaviorengineering/polypus/internal/clients/httpx"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// Op names for this package's errors.
const (
	opNewClient = "anthropic.newClient"
	opList      = "anthropic.ListModelsStrict"
	opFetchAll  = "anthropic.fetchAll"
	opFetchPage = "anthropic.fetchPage"
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

// NewClientWithListURL builds a catalog client for a fixed list URL (tests and
// injected getters). Its cache runs on the wall clock; production clients come from
// Clients.Get, which takes the gateway's clock.
func NewClientWithListURL(listURL, token string) (*Client, error) {
	return newClient(listURL, token, time.Now)
}

func newClient(listURL, token string, now func() time.Time) (*Client, error) {
	listURL = strings.TrimSpace(listURL)
	if listURL == "" {
		return nil, derrors.Wrap(errListModelsURLRequired, derrors.CodeInvalid, opNewClient, "list url")
	}
	if strings.TrimSpace(token) == "" {
		return nil, derrors.Wrap(errAPIKeyRequired, derrors.CodeInvalid, opNewClient, "api key")
	}
	if now == nil {
		return nil, derrors.New(derrors.CodeInternal, opNewClient, "clock required")
	}
	return &Client{
		listURL: listURL,
		apiKey:  token,
		httpClient: &http.Client{
			Transport: observability.WrapTransport(http.DefaultTransport),
		},
		cache: catalog.NewCacheClock(modelsCacheTTL, now),
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
		return ModelList{}, derrors.New(derrors.CodeNotReady, opList, "not configured")
	}
	if ctx == nil {
		return ModelList{}, derrors.New(derrors.CodeInvalid, opList, "context required")
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
	return nil, derrors.New(derrors.CodeUnavailable, opFetchAll, fmt.Sprintf("more than %d pages", modelsMaxPages))
}

func (c *Client) fetchPage(ctx context.Context, afterID string) (anthropicModelPage, error) {
	u, err := url.Parse(c.listURL)
	if err != nil {
		return anthropicModelPage{}, derrors.Wrap(err, derrors.CodeInternal, opFetchPage, "list url")
	}
	q := u.Query()
	q.Set("limit", strconv.Itoa(modelsPageLimit))
	if afterID != "" {
		q.Set("after_id", afterID)
	}
	u.RawQuery = q.Encode()
	hdr := http.Header{}
	hdr.Set("x-api-key", c.apiKey)
	hdr.Set("anthropic-version", apiVersion)
	hdr.Set("Accept", "application/json")
	h := httpx.HTTP{Client: c.httpClient, Policies: c.policies, Dep: outbound.Scoped(outbound.DepAnthropic, c.backendID)}
	return httpx.GetJSON[anthropicModelPage](ctx, h, httpx.JSONGet{Op: opFetchPage, URL: u.String(), Header: hdr, MaxBody: modelsMaxBody})
}

// createdUnix converts the RFC 3339 created_at to Unix seconds (0 when absent or malformed).
func createdUnix(s string) int64 {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return t.Unix()
}
