// Package github is the GitHub REST client the deploy uses to follow a release's
// image builds: the commit a tag points at, workflow runs and their jobs.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// DefaultBaseURL is the public GitHub REST API.
const DefaultBaseURL = "https://api.github.com"

// apiVersion pins the REST API version the responses are parsed against.
const apiVersion = "2022-11-28"

// maxBody bounds a response body read into memory.
const maxBody = 4 << 20

// maxErrorBody bounds how much of an error response is kept for the message.
const maxErrorBody = 200

// ErrNotFound means GitHub answered 404 for the resource.
var ErrNotFound = errors.New("github: not found")

// Config holds what a Client needs.
type Config struct {
	// HTTP sends the requests. Nil uses a client with an OpenTelemetry transport and
	// no timeout of its own: the caller's context deadline bounds every request.
	HTTP *http.Client
	// Token is the GitHub token sent as a Bearer header. Required.
	Token string
	// BaseURL is the REST API root; empty uses DefaultBaseURL.
	BaseURL string
	// OwnerRepo is owner/name of the repository every call reads. Required.
	OwnerRepo string
}

// Client reads one repository's tags, workflow runs and jobs.
type Client struct {
	http      *http.Client
	token     string
	base      string
	ownerRepo string
}

// CreateClient returns a Client for cfg, or an error when the token or repository
// is missing.
func (cfg Config) CreateClient() (*Client, error) {
	const op = "github.CreateClient"
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return nil, derrors.New(derrors.CodeFailedPrecondition, op, "GitHub token required (GITHUB_TOKEN or GH_TOKEN)")
	}
	ownerRepo := strings.Trim(strings.TrimSpace(cfg.OwnerRepo), "/")
	if ownerRepo == "" {
		return nil, derrors.New(derrors.CodeInvalid, op, "owner/repo required")
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Transport: observability.WrapTransport(nil)}
	}
	return &Client{http: hc, token: token, base: base, ownerRepo: ownerRepo}, nil
}

// WorkflowRun is one GitHub Actions run.
type WorkflowRun struct {
	ID           int64
	Name         string
	DisplayTitle string
	Status       string
	Conclusion   string
	HeadBranch   string
	HTMLURL      string
	CreatedAt    time.Time
}

// Job is one job of a workflow run.
type Job struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

type runsResponse struct {
	WorkflowRuns []struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		DisplayTitle string `json:"display_title"`
		Status       string `json:"status"`
		Conclusion   string `json:"conclusion"`
		HeadBranch   string `json:"head_branch"`
		HTMLURL      string `json:"html_url"`
		CreatedAt    string `json:"created_at"`
	} `json:"workflow_runs"`
}

type jobsResponse struct {
	Jobs []Job `json:"jobs"`
}

type gitRefResponse struct {
	Object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"object"`
}

// TagCommit returns the commit SHA tag points at, peeling an annotated tag. A
// missing tag wraps ErrNotFound (CodeNotFound).
func (c *Client) TagCommit(ctx context.Context, tag string) (string, error) {
	const op = "github.TagCommit"
	var ref gitRefResponse
	if err := c.get(ctx, op, fmt.Sprintf("%s/repos/%s/git/ref/tags/%s", c.base, c.ownerRepo, tag), &ref); err != nil {
		return "", err
	}
	sha := strings.TrimSpace(ref.Object.SHA)
	if sha == "" {
		return "", derrors.New(derrors.CodeUnavailable, op, "tag ref has no object").With("tag", tag)
	}
	if !strings.EqualFold(ref.Object.Type, "tag") {
		return sha, nil
	}
	var annotated gitRefResponse
	if err := c.get(ctx, op, fmt.Sprintf("%s/repos/%s/git/tags/%s", c.base, c.ownerRepo, sha), &annotated); err != nil {
		return "", err
	}
	commit := strings.TrimSpace(annotated.Object.SHA)
	if commit == "" {
		return "", derrors.New(derrors.CodeUnavailable, op, "annotated tag has no commit").With("tag", tag)
	}
	return commit, nil
}

// WorkflowRuns returns the latest perPage runs of the workflow file (for example
// docker-release.yml).
func (c *Client) WorkflowRuns(ctx context.Context, workflowFile string, perPage int) ([]WorkflowRun, error) {
	const op = "github.WorkflowRuns"
	return c.runs(ctx, op, fmt.Sprintf("%s/repos/%s/actions/workflows/%s/runs?per_page=%d", c.base, c.ownerRepo, workflowFile, perPage))
}

// RunsForSHA returns the latest runs of any workflow for the commit sha.
func (c *Client) RunsForSHA(ctx context.Context, sha string) ([]WorkflowRun, error) {
	const op = "github.RunsForSHA"
	return c.runs(ctx, op, fmt.Sprintf("%s/repos/%s/actions/runs?head_sha=%s&per_page=20", c.base, c.ownerRepo, sha))
}

// RunJobs returns the jobs of the run.
func (c *Client) RunJobs(ctx context.Context, runID int64) ([]Job, error) {
	const op = "github.RunJobs"
	var doc jobsResponse
	if err := c.get(ctx, op, fmt.Sprintf("%s/repos/%s/actions/runs/%d/jobs?per_page=30", c.base, c.ownerRepo, runID), &doc); err != nil {
		return nil, err
	}
	return doc.Jobs, nil
}

func (c *Client) runs(ctx context.Context, op, url string) ([]WorkflowRun, error) {
	var doc runsResponse
	if err := c.get(ctx, op, url, &doc); err != nil {
		return nil, err
	}
	out := make([]WorkflowRun, 0, len(doc.WorkflowRuns))
	for _, wr := range doc.WorkflowRuns {
		created, err := time.Parse(time.RFC3339, wr.CreatedAt)
		if err != nil {
			return nil, derrors.Wrap(err, derrors.CodeUnavailable, op, "run created_at is not RFC 3339").
				With("run", wr.HTMLURL).With("created_at", wr.CreatedAt)
		}
		out = append(out, WorkflowRun{
			ID:           wr.ID,
			Name:         wr.Name,
			DisplayTitle: wr.DisplayTitle,
			Status:       wr.Status,
			Conclusion:   wr.Conclusion,
			HeadBranch:   wr.HeadBranch,
			HTMLURL:      wr.HTMLURL,
			CreatedAt:    created,
		})
	}
	return out, nil
}

// get sends one GET under the outbound retry policy and decodes a 200 answer into
// into. ctx must carry a deadline.
func (c *Client) get(ctx context.Context, op, url string, into any) error {
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return derrors.New(derrors.CodeFailedPrecondition, op, "context needs a deadline")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInvalid, op, "new request").With("url", url)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	resp, err := outbound.Do(ctx, outbound.DepGitHub, c.http, req)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "GET "+url)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, op, "read GET "+url)
	}
	if resp.StatusCode != http.StatusOK {
		return statusError(op, url, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, op, "parse GET "+url)
	}
	return nil
}

func statusError(op, url string, status int, body []byte) error {
	cause := fmt.Errorf("HTTP %d: %s", status, truncate(body, maxErrorBody))
	msg := "GET " + url
	switch status {
	case http.StatusNotFound:
		return derrors.Wrap(fmt.Errorf("%w: %w", ErrNotFound, cause), derrors.CodeNotFound, op, msg)
	case http.StatusUnauthorized, http.StatusForbidden:
		return derrors.Wrap(cause, derrors.CodeUnauthorized, op, msg)
	case http.StatusTooManyRequests:
		return derrors.Wrap(cause, derrors.CodeRateLimited, op, msg)
	default:
		return derrors.Wrap(cause, derrors.CodeUnavailable, op, msg)
	}
}

func truncate(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
