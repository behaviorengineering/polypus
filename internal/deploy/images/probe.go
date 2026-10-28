package images

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/outbound"
)

var manifestAccept = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

// ErrManifestNotFound means the registry has no manifest for the pinned tag.
var ErrManifestNotFound = errors.New("registry manifest not found")

// ErrRegistryUnauthorized means GHCR rejected credentials.
var ErrRegistryUnauthorized = errors.New("registry unauthorized")

// ProbeManifest checks that ref exists in the registry (GHCR manifest GET).
func ProbeManifest(ctx context.Context, ref string, token string) error {
	if ctx == nil {
		return fmt.Errorf("images: context required")
	}
	repo, tag, err := splitImageRef(ref)
	if err != nil {
		return err
	}
	manifestURL := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", registryScheme(repo.Registry), repo.Registry, repo.Name, tag)

	client := &http.Client{Timeout: 30 * time.Second}
	var lastErr error
	err = outbound.Run(ctx, outbound.DepGHCR, func() error {
		lastErr = probeOnce(ctx, client, repo, manifestURL, token)
		if lastErr == nil {
			return nil
		}
		if !registryRetryable(lastErr) {
			return outbound.Abort(lastErr)
		}
		return outbound.Retry(lastErr)
	})
	if err != nil {
		if errors.Is(err, outbound.ErrAbort) {
			return fmt.Errorf("images: probe %q: %w", ref, lastErr)
		}
		return fmt.Errorf("images: probe %q: %w", ref, err)
	}
	return nil
}

func probeOnce(ctx context.Context, client *http.Client, repo imageRepo, manifestURL string, token string) error {
	status, err := doManifestGet(ctx, client, manifestURL, "")
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		pat := strings.TrimSpace(token)
		if pat == "" {
			pat = registryToken()
		}
		bearer, err := registryBearerToken(ctx, client, repo, pat)
		switch {
		case err == nil:
		case pat == "":
			return fmt.Errorf("%w: no anonymous pull token (%w); set GHCR_TOKEN or GITHUB_TOKEN with read:packages for private packages", ErrRegistryUnauthorized, err)
		default:
			// Fall back to PAT-as-Bearer for hosts that accept it directly.
			bearer = pat
		}
		status, err = doManifestGet(ctx, client, manifestURL, bearer)
		if err != nil {
			return err
		}
	}
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrManifestNotFound, manifestURL)
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: manifest GET returned %d", ErrRegistryUnauthorized, status)
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return fmt.Errorf("registry HTTP %d", status)
	default:
		if status >= 500 {
			return fmt.Errorf("registry HTTP %d", status)
		}
		return fmt.Errorf("registry HTTP %d", status)
	}
}

func doManifestGet(ctx context.Context, client *http.Client, url string, bearer string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", manifestAccept)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := outbound.Do(ctx, outbound.DepGHCR, client, req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func registryRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrManifestNotFound) || errors.Is(err, ErrRegistryUnauthorized) {
		return false
	}
	if errors.Is(err, outbound.ErrAbort) {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "registry http 429") ||
		strings.Contains(msg, "registry http 5") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "temporary failure")
}

type imageRepo struct {
	Registry string
	Name     string
}

func splitImageRef(ref string) (imageRepo, string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return imageRepo{}, "", fmt.Errorf("images: empty image ref")
	}
	if strings.Contains(ref, "@") {
		return imageRepo{}, "", fmt.Errorf("images: digest refs are not supported (%s)", ref)
	}
	idx := strings.LastIndex(ref, ":")
	if idx <= 0 || idx == len(ref)-1 {
		return imageRepo{}, "", fmt.Errorf("images: missing tag in %s", ref)
	}
	tag := ref[idx+1:]
	path := ref[:idx]
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return imageRepo{}, "", fmt.Errorf("images: invalid image ref %s", ref)
	}
	return imageRepo{Registry: parts[0], Name: parts[1]}, tag, nil
}

func registryScheme(registry string) string {
	if strings.HasPrefix(registry, "127.0.0.1") || strings.HasPrefix(registry, "localhost") {
		return "http"
	}
	return "https"
}

func registryToken() string {
	for _, key := range []string{"GHCR_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}
