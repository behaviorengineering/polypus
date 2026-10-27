package images

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/behaviorengineering/polypus/internal/outbound"
)

type ghcrTokenResponse struct {
	Token string `json:"token"`
}

// registryBearerToken returns a Bearer token for manifest GET (GHCR OAuth exchange when needed).
func registryBearerToken(ctx context.Context, client *http.Client, repo imageRepo, pat string) (string, error) {
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return "", fmt.Errorf("images: empty registry token")
	}
	scope := fmt.Sprintf("repository:%s:pull", repo.Name)
	tokenURL := fmt.Sprintf("%s://%s/token?service=%s&scope=%s",
		registryScheme(repo.Registry),
		repo.Registry,
		url.QueryEscape(repo.Registry),
		url.QueryEscape(scope),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", err
	}
	// GHCR expects docker-login style Basic auth (username + PAT), not Bearer PAT on /token.
	req.SetBasicAuth(registryBasicUser(), pat)
	resp, err := outbound.Do(ctx, outbound.DepGHCR, client, req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry token: HTTP %d: %s", resp.StatusCode, truncate(body, 200))
	}
	var doc ghcrTokenResponse
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("registry token: parse JSON: %w", err)
	}
	tok := strings.TrimSpace(doc.Token)
	if tok == "" {
		return "", fmt.Errorf("registry token: empty token in response")
	}
	return tok, nil
}

// registryBasicUser is the username for GHCR PAT Basic auth (password is the PAT).
// Matches docker login ghcr.io -u <user> --password-stdin. GitHub allows any non-empty username.
func registryBasicUser() string {
	for _, key := range []string{"GHCR_USERNAME", "GITHUB_ACTOR"} {
		if u := strings.TrimSpace(os.Getenv(key)); u != "" {
			return u
		}
	}
	return "token"
}
