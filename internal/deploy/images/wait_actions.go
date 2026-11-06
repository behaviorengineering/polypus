package images

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/clients/github"
	"github.com/behaviorengineering/polypus/internal/deploy/progress"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// conclusionSuccess is a GitHub Actions run or job that passed.
const conclusionSuccess = "success"

// runsPerPage is how many recent runs of each workflow file a poll reads.
const runsPerPage = 20

// WaitOptions configures GitHub Actions polling when registry tags are missing.
type WaitOptions struct {
	OwnerRepo     string
	WorkflowFiles []string
	PollInterval  time.Duration
	Timeout       time.Duration
	// GitHub reads the workflow runs. Nil builds one from HTTPClient, Token and
	// GitHubAPIBaseURL.
	GitHub     *github.Client
	HTTPClient *http.Client
	Token      string
	// GitHubAPIBaseURL is optional (tests). Default https://api.github.com.
	GitHubAPIBaseURL string
	// Out receives progress lines; nil writes to os.Stdout.
	Out io.Writer
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

// githubClient returns opts.GitHub, or a client built from the token (else
// GHCR_TOKEN, GITHUB_TOKEN or GH_TOKEN) and the other fields.
func (o WaitOptions) githubClient() (*github.Client, error) {
	if o.GitHub != nil {
		return o.GitHub, nil
	}
	token := strings.TrimSpace(o.Token)
	if token == "" {
		token = registryToken()
	}
	return github.Config{
		HTTP:      o.HTTPClient,
		Token:     token,
		BaseURL:   o.GitHubAPIBaseURL,
		OwnerRepo: o.OwnerRepo,
	}.CreateClient()
}

// ErrImageBuildFailed means the latest matching Actions run failed.
var ErrImageBuildFailed = errors.New("image build workflow failed")

// WaitForImages polls GitHub Actions until the release's image workflow succeeds,
// fails, or opts.Timeout passes. It waits while no matching run exists yet.
func WaitForImages(ctx context.Context, version string, opts WaitOptions) error {
	const op = "images.WaitForImages"
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return derrors.New(derrors.CodeInvalid, op, "empty version for wait")
	}
	opts = applyWaitDefaults(opts)
	gh, err := opts.githubClient()
	if err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeFailedPrecondition), op, "wait for image builds")
	}
	parsed, err := ParseImageTag(version)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInvalid, op, "parse version")
	}
	tag := appGitTag(parsed.App)
	pinTag := version
	if parsed.Rebuild == 0 {
		pinTag = parsed.App
	}
	out := progress.New(opts.Out)
	deadline := time.Now().Add(opts.Timeout)
	for {
		if err := ctx.Err(); err != nil {
			return derrors.Wrap(err, derrors.CodeCanceled, op, "wait for "+tag)
		}
		if time.Now().After(deadline) {
			return derrors.New(derrors.CodeTimeout, op, "timed out waiting for image tags").
				With("tag", tag).With("timeout", opts.Timeout.String())
		}
		runs, err := listMatchingRuns(ctx, gh, opts.WorkflowFiles, tag, pinTag)
		if err != nil {
			return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "list workflow runs")
		}
		if done, err := imageRunsDone(out, runs, tag); done || err != nil {
			if err == nil {
				err = out.Err()
			}
			return err
		}
		if err := sleepCtx(ctx, opts.PollInterval); err != nil {
			return derrors.Wrap(err, derrors.CodeCanceled, op, "wait for "+tag)
		}
	}
}

// imageRunsDone reads one poll of the matching workflow runs: done with a nil error
// when the latest run succeeded, done with an error when it failed, and not done
// while no run exists yet or one is still running.
func imageRunsDone(out *progress.Printer, runs []github.WorkflowRun, tag string) (bool, error) {
	if len(runs) == 0 {
		out.Printf("deploy: waiting for image workflow to appear for %s\n", tag)
		return false, nil
	}
	latest, inProgress := latestRun(runs)
	if inProgress {
		out.Printf("deploy: waiting for image build %s\n", latest.HTMLURL)
		return false, nil
	}
	switch strings.ToLower(latest.Conclusion) {
	case conclusionSuccess:
		return true, nil
	case "failure", "cancelled", "timed_out", "action_required", "stale":
		return true, derrors.Wrap(fmt.Errorf("%w: %s (%s)", ErrImageBuildFailed, tag, latest.HTMLURL),
			derrors.CodeFailedPrecondition, "images.WaitForImages", "image build failed")
	default:
		return false, nil
	}
}

// latestRun returns the most recently created run in runs (which must not be
// empty), and whether any run is still in progress.
func latestRun(runs []github.WorkflowRun) (github.WorkflowRun, bool) {
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

// listMatchingRuns reads each workflow file's recent runs once; the client's
// outbound policy is the only retry.
func listMatchingRuns(ctx context.Context, gh *github.Client, workflowFiles []string, tag, version string) ([]github.WorkflowRun, error) {
	var matched []github.WorkflowRun
	for _, wf := range workflowFiles {
		page, err := gh.WorkflowRuns(ctx, wf, runsPerPage)
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

// runMatchesVersion prefers tag head_branch; otherwise title/name must contain the full vX.Y.Z tag.
func runMatchesVersion(r github.WorkflowRun, tag string, version string) bool {
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

func runInProgress(r github.WorkflowRun) bool {
	switch strings.ToLower(r.Status) {
	case "queued", "in_progress", "waiting", "requested", "pending":
		return true
	default:
		return false
	}
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
