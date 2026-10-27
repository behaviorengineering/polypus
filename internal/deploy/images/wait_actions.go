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

// WaitOptions configures GitHub Actions polling when GHCR tags are missing.
type WaitOptions struct {
	OwnerRepo     string
	WorkflowFiles []string
	PollInterval  time.Duration
	Timeout       time.Duration
	HTTPClient    *http.Client
	Token         string
	// WorkflowRunsBaseURL is optional (tests). Default https://api.github.com/repos/{owner}/{repo}/actions/workflows
	WorkflowRunsBaseURL string
	// GitHubAPIBaseURL is optional (tests). Default https://api.github.com
	GitHubAPIBaseURL string
}

// DefaultWaitOptions returns deploy defaults (20m cap, 30s poll).
func DefaultWaitOptions() WaitOptions {
	return WaitOptions{
		OwnerRepo: "behaviorengineering/polypus",
		WorkflowFiles: []string{
			"docker-release.yml",
			"packaging-rebuild.yml",
			"auto-patch-release.yml",
		},
		PollInterval: 30 * time.Second,
		Timeout:      20 * time.Minute,
	}
}

func applyWaitDefaults(opts WaitOptions) WaitOptions {
	def := DefaultWaitOptions()
	if strings.TrimSpace(opts.OwnerRepo) == "" {
		opts.OwnerRepo = def.OwnerRepo
	}
	if len(opts.WorkflowFiles) == 0 {
		opts.WorkflowFiles = def.WorkflowFiles
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = def.PollInterval
	}
	if opts.Timeout <= 0 {
		opts.Timeout = def.Timeout
	}
	return opts
}

// ErrImageNeverPublished means no matching Actions run exists for the version.
var ErrImageNeverPublished = errors.New("images never published for version")

// ErrImageBuildFailed means the latest matching Actions run failed.
var ErrImageBuildFailed = errors.New("image build workflow failed")

// WaitForImages polls GitHub Actions until docker release work finishes or fails fast.
func WaitForImages(ctx context.Context, version string, opts WaitOptions) error {
	if ctx == nil {
		return fmt.Errorf("images: context required")
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Errorf("images: empty version for wait")
	}
	opts = applyWaitDefaults(opts)

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	token := opts.Token
	if token == "" {
		token = registryToken()
	}
	if token == "" {
		return fmt.Errorf("images: GITHUB_TOKEN or GH_TOKEN required to wait for image builds")
	}

	deadline := time.Now().Add(opts.Timeout)
	parsed, err := ParseImageTag(version)
	if err != nil {
		return err
	}
	tag := AppGitTag(parsed.App)
	pinTag := version
	if parsed.Rebuild == 0 {
		pinTag = parsed.App
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("images: timed out waiting for GHCR tags for %s", tag)
		}
		runs, err := listMatchingRuns(ctx, client, token, opts, tag, pinTag)
		if err != nil {
			return err
		}
		if done, err := imageRunsDone(runs, tag); done || err != nil {
			return err
		}
		if err := sleepCtx(ctx, opts.PollInterval); err != nil {
			return err
		}
	}
}

// imageRunsDone reads one poll of the matching workflow runs: done with a nil error
// when the latest run succeeded, done with an error when it failed, and not done
// while no run exists yet or one is still running.
func imageRunsDone(runs []workflowRun, tag string) (bool, error) {
	if len(runs) == 0 {
		fmt.Printf("deploy: waiting for image workflow to appear for %s\n", tag)
		return false, nil
	}
	latest, inProgress := latestRun(runs)
	if inProgress {
		fmt.Printf("deploy: waiting for image build %s\n", latest.HTMLURL)
		return false, nil
	}
	switch strings.ToLower(latest.Conclusion) {
	case "success":
		return true, nil
	case "failure", "cancelled", "timed_out", "action_required", "stale":
		return true, fmt.Errorf("%w: %s (%s)", ErrImageBuildFailed, tag, latest.HTMLURL)
	default:
		return false, nil
	}
}

// latestRun returns the most recently created run in runs (which must not be
// empty), and whether any run is still in progress.
func latestRun(runs []workflowRun) (workflowRun, bool) {
	latest := runs[0]
	inProgress := false
	for _, r := range runs {
		if r.CreatedAt.After(latest.CreatedAt) {
			latest = r
		}
		if runInProgress(r) {
			inProgress = true
		}
	}
	return latest, inProgress
}

type workflowRun struct {
	Status       string
	Conclusion   string
	HeadBranch   string
	DisplayTitle string
	Name         string
	HTMLURL      string
	CreatedAt    time.Time
}

type workflowRunsResponse struct {
	WorkflowRuns []struct {
		Status       string `json:"status"`
		Conclusion   string `json:"conclusion"`
		HeadBranch   string `json:"head_branch"`
		DisplayTitle string `json:"display_title"`
		Name         string `json:"name"`
		HTMLURL      string `json:"html_url"`
		CreatedAt    string `json:"created_at"`
	} `json:"workflow_runs"`
}

func listMatchingRuns(ctx context.Context, client *http.Client, token string, opts WaitOptions, tag string, version string) ([]workflowRun, error) {
	var matched []workflowRun
	for _, wf := range opts.WorkflowFiles {
		url := workflowRunsURL(opts, wf)
		var page []workflowRun
		var fetchErr error
		err := outbound.Run(ctx, outbound.DepGitHub, func() error {
			page, fetchErr = fetchWorkflowRuns(ctx, client, token, url)
			if fetchErr == nil {
				return nil
			}
			if !githubActionsRetryable(fetchErr) {
				return outbound.Abort(fetchErr)
			}
			return outbound.Retry(fetchErr)
		})
		if err != nil {
			return nil, err
		}
		for _, r := range page {
			if runMatchesVersion(r, tag, version) {
				matched = append(matched, r)
			}
		}
	}
	return matched, nil
}

func workflowRunsURL(opts WaitOptions, workflowFile string) string {
	if strings.TrimSpace(opts.WorkflowRunsBaseURL) != "" {
		return strings.TrimRight(opts.WorkflowRunsBaseURL, "/") + "/" + workflowFile + "/runs?per_page=20"
	}
	return fmt.Sprintf("https://api.github.com/repos/%s/actions/workflows/%s/runs?per_page=20", opts.OwnerRepo, workflowFile)
}

// runMatchesVersion prefers tag head_branch; otherwise title/name must contain the full vX.Y.Z tag.
func runMatchesVersion(r workflowRun, tag string, version string) bool {
	hb := strings.TrimSpace(r.HeadBranch)
	if hb == tag || hb == version {
		return true
	}
	blob := strings.ToLower(r.DisplayTitle + "\n" + r.Name)
	if containsTagToken(blob, strings.ToLower(tag)) {
		return true
	}
	ver := strings.TrimPrefix(version, "v")
	if ver == "" {
		return false
	}
	if strings.Contains(ver, "-r") {
		return strings.Contains(blob, strings.ToLower(ver))
	}
	if containsTagToken(blob, strings.ToLower(ver)) {
		return true
	}
	return strings.Contains(blob, "release "+strings.ToLower(ver)) ||
		strings.Contains(blob, "tag "+strings.ToLower(ver))
}

func containsTagToken(blob, tagLower string) bool {
	if tagLower == "" {
		return false
	}
	offset := 0
	for {
		j := strings.Index(blob[offset:], tagLower)
		if j < 0 {
			return false
		}
		j += offset
		end := j + len(tagLower)
		if end >= len(blob) {
			return true
		}
		next := blob[end]
		if next < '0' || next > '9' {
			return true
		}
		offset = end
	}
}

func runInProgress(r workflowRun) bool {
	switch strings.ToLower(r.Status) {
	case "queued", "in_progress", "waiting", "requested", "pending":
		return true
	default:
		return false
	}
}

func fetchWorkflowRuns(ctx context.Context, client *http.Client, token string, url string) ([]workflowRun, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := outbound.Do(ctx, outbound.DepGitHub, client, req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github actions: HTTP %d: %s", resp.StatusCode, truncate(body, 200))
	}
	var doc workflowRunsResponse
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("github actions: parse runs: %w", err)
	}
	var out []workflowRun
	for _, wr := range doc.WorkflowRuns {
		created, _ := time.Parse(time.RFC3339, wr.CreatedAt)
		out = append(out, workflowRun{
			Status:       wr.Status,
			Conclusion:   wr.Conclusion,
			HeadBranch:   wr.HeadBranch,
			DisplayTitle: wr.DisplayTitle,
			Name:         wr.Name,
			HTMLURL:      wr.HTMLURL,
			CreatedAt:    created,
		})
	}
	return out, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func githubActionsRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "github actions: http 429") ||
		strings.Contains(msg, "github actions: http 5") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "temporary failure")
}

func truncate(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
