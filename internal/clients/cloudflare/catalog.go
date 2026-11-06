package cloudflare

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
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
	modelsCacheTTL = 10 * time.Minute
	modelsPerPage  = 100
	modelsMaxPages = 50
	modelsMaxBody  = 8 << 20
)

// Op names for this package's errors.
const (
	opNewClient = "cloudflare.newClient"
	opModels    = "cloudflare.fetchAll"
	opFetchPage = "cloudflare.fetchPage"
)

var accountIDRE = regexp.MustCompile(`/accounts/([^/]+)/`)

// Model is the OpenAI Models API object.
type Model = catalog.Model

// ModelList is the OpenAI list response for GET /v1/models.
type ModelList = catalog.ModelList

// Client implements Cloudflare Workers AI extension hooks for Polypus.
type Client struct {
	apiBase string
	apiKey  string
	// httpClient carries every call (catalog, ping, speech, SystemOne, batch). It sets
	// no Timeout: the caller's ctx deadline bounds each call.
	httpClient *http.Client

	cache  *catalog.Cache
	speech SpeechDefaults

	backendID string             // scopes the outbound breaker to this backend.
	policies  *outbound.Policies // nil: the process default.
	log       *slog.Logger       // nil: no warnings.
}

// NewClient builds an uncached Cloudflare extension client from a remote backend
// definition, with the built-in speech defaults and a wall-clock catalog cache.
// Production code uses Clients.Get, so catalog, health and speech share one client.
func NewClient(b config.BackendDef) (*Client, error) {
	if !b.IsCloudflareExtension() {
		return nil, derrors.Wrap(errNotCloudflareExtension, derrors.CodeInvalid, opNewClient, "backend").With("backend", b.ID)
	}
	token, err := b.Auth.ResolveBearerToken()
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), opNewClient, "resolve token").With("backend", b.ID)
	}
	apiBase, err := AIBaseURL(b.BaseURL)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, opNewClient, "base url").With("backend", b.ID)
	}
	return newClient(apiBase, token, time.Now, SpeechDefaults{})
}

func newClient(apiBase, token string, now func() time.Time, sd SpeechDefaults) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, derrors.Wrap(errAPIKeyRequired, derrors.CodeInvalid, opNewClient, "api key")
	}
	if now == nil {
		return nil, derrors.New(derrors.CodeInternal, opNewClient, "clock required")
	}
	return &Client{
		apiBase: apiBase,
		apiKey:  token,
		httpClient: &http.Client{
			Transport: observability.WrapTransport(http.DefaultTransport),
		},
		cache:  catalog.NewCacheClock(modelsCacheTTL, now),
		speech: sd.withBuiltins(),
	}, nil
}

// Ping checks Cloudflare Model Search reachability (used by gateway health). The
// caller's ctx deadline bounds it.
func (c *Client) Ping(ctx context.Context) error {
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, "cloudflare.Ping", "context required")
	}
	if c == nil {
		return derrors.New(derrors.CodeFailedPrecondition, "cloudflare", "not configured")
	}
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
	if ctx == nil {
		return ModelList{}, derrors.New(derrors.CodeInvalid, "cloudflare.ListModelsStrict", "context required")
	}
	if c == nil {
		return ModelList{}, derrors.New(derrors.CodeFailedPrecondition, "cloudflare", "not configured")
	}
	return catalog.ListStrict(ctx, catalog.Source{Provider: "cloudflare", Backend: c.backendID, Cache: c.cache, Fetch: c.fetchAll, Log: c.log})
}

func (c *Client) fetchAll(ctx context.Context) ([]Model, error) {
	byID := make(map[string]Model)
	for page := 1; ; page++ {
		if page > modelsMaxPages {
			// A partial catalog would be cached as if it were complete.
			return nil, derrors.New(derrors.CodeUnavailable, opModels, fmt.Sprintf("more than %d pages", modelsMaxPages))
		}
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
	Errors []workersAIError `json:"errors"`
}

type cfModelSearchEntry struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

func (c *Client) fetchPage(ctx context.Context, page int) ([]Model, int, error) {
	base := strings.TrimRight(c.apiBase, "/")
	u, err := url.Parse(base + "/models/search")
	if err != nil {
		return nil, 0, derrors.Wrap(err, derrors.CodeInternal, opFetchPage, "parse url")
	}
	q := u.Query()
	q.Set("page", strconv.Itoa(page))
	q.Set("per_page", strconv.Itoa(modelsPerPage))
	q.Set("hide_experimental", "true")
	u.RawQuery = q.Encode()

	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+c.apiKey)
	hdr.Set("Accept", "application/json")
	h := catalog.HTTP{Client: c.httpClient, Policies: c.policies, Dep: c.dep()}
	var body cfModelsSearchResponse
	if err := catalog.GetJSON(ctx, h, opFetchPage, u.String(), hdr, modelsMaxBody, &body); err != nil {
		return nil, 0, err
	}
	if !body.Success {
		return nil, 0, derrors.New(derrors.CodeUnavailable, opFetchPage, firstErrorMessage(body.Errors, "models search failed"))
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
			Object:  catalog.ObjectModel,
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
