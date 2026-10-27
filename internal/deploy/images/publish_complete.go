package images

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/outbound"
)

// ErrPublishIncomplete means upstream auto-patch did not finish green through verify for the tag.
var ErrPublishIncomplete = errors.New("upstream auto-patch publish incomplete")

// VerifyPublishComplete checks that behaviorengineering/polypus Auto patch release succeeded
// through the verify job for the commit tagged vX.Y.Z (publish-complete gate).
func VerifyPublishComplete(ctx context.Context, version string, opts WaitOptions) error {
	if ctx == nil {
		return fmt.Errorf("images: context required")
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("images: empty version for publish-complete check")
	}
	parsed, err := ParseImageTag(version)
	if err != nil {
		return err
	}
	opts = applyWaitDefaults(opts)
	token := strings.TrimSpace(opts.Token)
	if token == "" {
		token = registryToken()
	}
	if token == "" {
		return fmt.Errorf("images: GITHUB_TOKEN or GH_TOKEN required for publish-complete check")
	}

	if parsed.Rebuild >= 1 {
		return verifyDockerReleasePublishComplete(ctx, version, opts, token)
	}

	tag := AppGitTag(parsed.App)
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}

	apiBase := githubAPIBase(opts)
	sha, err := resolveTagCommit(ctx, client, token, opts.OwnerRepo, tag, apiBase)
	if err != nil {
		return err
	}

	runs, err := fetchRunsForHeadSHA(ctx, client, token, opts.OwnerRepo, sha, apiBase)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if !strings.EqualFold(run.Name, "Auto patch release") {
			continue
		}
		if !strings.EqualFold(run.Conclusion, "success") {
			continue
		}
		ok, err := runHasSuccessfulJob(ctx, client, token, opts.OwnerRepo, run.ID, "verify", apiBase)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return fmt.Errorf("%w: no successful Auto patch release with verify for %s (%s)", ErrPublishIncomplete, tag, sha)
}

// verifyDockerReleasePublishComplete gates -rN pins on a successful Docker release workflow run.
func verifyDockerReleasePublishComplete(ctx context.Context, imageTag string, opts WaitOptions, token string) error {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	apiBase := githubAPIBase(opts)
	want := strings.ToLower(strings.TrimSpace(imageTag))
	for _, wf := range []string{"docker-release.yml", "packaging-rebuild.yml"} {
		url := fmt.Sprintf("%s/repos/%s/actions/workflows/%s/runs?per_page=30", apiBase, opts.OwnerRepo, wf)
		body, status, err := githubGET(ctx, client, token, url)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("github: workflow runs HTTP %d: %s", status, truncate(body, 200))
		}
		var doc actionsRunsListResponse
		if err := json.Unmarshal(body, &doc); err != nil {
			return fmt.Errorf("github: parse workflow runs: %w", err)
		}
		for _, wr := range doc.WorkflowRuns {
			if !strings.EqualFold(wr.Conclusion, "success") {
				continue
			}
			blob := strings.ToLower(wr.Name + "\n" + wr.DisplayTitle)
			if strings.Contains(blob, want) {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: no successful Docker release for image tag %s", ErrPublishIncomplete, imageTag)
}

type gitRefResponse struct {
	Object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"object"`
}

type gitTagResponse struct {
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

func githubAPIBase(opts WaitOptions) string {
	if strings.TrimSpace(opts.GitHubAPIBaseURL) != "" {
		return strings.TrimRight(opts.GitHubAPIBaseURL, "/")
	}
	return "https://api.github.com"
}

func resolveTagCommit(ctx context.Context, client *http.Client, token string, ownerRepo string, tag string, apiBase string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/git/ref/tags/%s", apiBase, ownerRepo, tag)
	body, status, err := githubGET(ctx, client, token, url)
	if err != nil {
		return "", err
	}
	if status == http.StatusNotFound {
		return "", fmt.Errorf("images: tag %s not found on %s", tag, ownerRepo)
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("github: tag ref HTTP %d: %s", status, truncate(body, 200))
	}
	var doc gitRefResponse
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("github: parse tag ref: %w", err)
	}
	sha := strings.TrimSpace(doc.Object.SHA)
	if sha == "" {
		return "", fmt.Errorf("images: empty object for tag %s", tag)
	}
	if strings.EqualFold(doc.Object.Type, "tag") {
		return peelAnnotatedTag(ctx, client, token, ownerRepo, sha, apiBase)
	}
	return sha, nil
}

func peelAnnotatedTag(ctx context.Context, client *http.Client, token string, ownerRepo string, tagSHA string, apiBase string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/git/tags/%s", apiBase, ownerRepo, tagSHA)
	body, status, err := githubGET(ctx, client, token, url)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("github: tag object HTTP %d: %s", status, truncate(body, 200))
	}
	var doc gitTagResponse
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("github: parse tag object: %w", err)
	}
	sha := strings.TrimSpace(doc.Object.SHA)
	if sha == "" {
		return "", fmt.Errorf("images: annotated tag %s has no commit", tagSHA)
	}
	return sha, nil
}

type actionsRunsListResponse struct {
	WorkflowRuns []struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		DisplayTitle string `json:"display_title"`
		Conclusion   string `json:"conclusion"`
	} `json:"workflow_runs"`
}

type actionRunSummary struct {
	ID         int64
	Name       string
	Conclusion string
}

func fetchRunsForHeadSHA(ctx context.Context, client *http.Client, token string, ownerRepo string, sha string, apiBase string) ([]actionRunSummary, error) {
	url := fmt.Sprintf("%s/repos/%s/actions/runs?head_sha=%s&per_page=20", apiBase, ownerRepo, sha)
	body, status, err := githubGET(ctx, client, token, url)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("github: actions runs HTTP %d: %s", status, truncate(body, 200))
	}
	var doc actionsRunsListResponse
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("github: parse actions runs: %w", err)
	}
	var out []actionRunSummary
	for _, wr := range doc.WorkflowRuns {
		out = append(out, actionRunSummary{ID: wr.ID, Name: wr.Name, Conclusion: wr.Conclusion})
	}
	return out, nil
}

type actionsJobsResponse struct {
	Jobs []struct {
		Name       string `json:"name"`
		Conclusion string `json:"conclusion"`
	} `json:"jobs"`
}

func runHasSuccessfulJob(ctx context.Context, client *http.Client, token string, ownerRepo string, runID int64, jobName string, apiBase string) (bool, error) {
	url := fmt.Sprintf("%s/repos/%s/actions/runs/%d/jobs?per_page=30", apiBase, ownerRepo, runID)
	body, status, err := githubGET(ctx, client, token, url)
	if err != nil {
		return false, err
	}
	if status != http.StatusOK {
		return false, fmt.Errorf("github: actions jobs HTTP %d: %s", status, truncate(body, 200))
	}
	var doc actionsJobsResponse
	if err := json.Unmarshal(body, &doc); err != nil {
		return false, fmt.Errorf("github: parse actions jobs: %w", err)
	}
	want := strings.ToLower(strings.TrimSpace(jobName))
	for _, j := range doc.Jobs {
		if strings.ToLower(strings.TrimSpace(j.Name)) != want {
			continue
		}
		if strings.EqualFold(j.Conclusion, "success") {
			return true, nil
		}
	}
	return false, nil
}

func githubGET(ctx context.Context, client *http.Client, token string, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := outbound.Do(ctx, outbound.DepGitHub, client, req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}
