package gemini

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/clients/catalog"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// Op names for this package's errors.
const (
	opNewClient = "gemini.newClient"
	opList      = "gemini.ListModelsStrict"
	opFetch     = "gemini.fetch"
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
		return ModelList{}, derrors.New(derrors.CodeFailedPrecondition, opList, "not configured")
	}
	if ctx == nil {
		return ModelList{}, derrors.New(derrors.CodeInvalid, opList, "context required")
	}
	return catalog.ListStrict(ctx, catalog.Source{Provider: "gemini", Backend: c.backendID, Cache: c.cache, Fetch: c.fetch, Log: c.log})
}

func (c *Client) fetch(ctx context.Context) ([]Model, error) {
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+c.apiKey)
	hdr.Set("Accept", "application/json")
	h := catalog.HTTP{Client: c.httpClient, Policies: c.policies, Dep: outbound.Scoped(outbound.DepGemini, c.backendID)}
	var list ModelList
	if err := catalog.GetJSON(ctx, h, opFetch, c.listURL, hdr, modelsMaxBody, &list); err != nil {
		return nil, err
	}
	return toModels(list), nil
}

// toModels keeps the entries with an id, without the "models/" prefix.
func toModels(list ModelList) []Model {
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
			OwnedBy: catalog.FirstNonEmpty(m.OwnedBy, "google"),
		})
	}
	return out
}

func normalizeModelID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "models/")
	return strings.TrimSpace(id)
}
