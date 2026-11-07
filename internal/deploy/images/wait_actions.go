package images

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/clients/github"
	"github.com/behaviorengineering/polypus/internal/deploy/progress"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// runConclusion is a GitHub Actions run or job conclusion.
type runConclusion string

// Run conclusions GitHub reports. A run with no conclusion is still running.
const (
	conclusionNone           runConclusion = ""
	conclusionSuccess        runConclusion = "success"
	conclusionFailure        runConclusion = "failure"
	conclusionCancelled      runConclusion = "cancelled"
	conclusionTimedOut       runConclusion = "timed_out"
	conclusionActionRequired runConclusion = "action_required"
	conclusionStale          runConclusion = "stale"
	conclusionNeutral        runConclusion = "neutral"
	conclusionSkipped        runConclusion = "skipped"
	conclusionStartupFailure runConclusion = "startup_failure"
)

// succeeded reports whether conclusion (as GitHub spells it) is success.
func succeeded(conclusion string) bool {
	return runConclusion(strings.ToLower(strings.TrimSpace(conclusion))) == conclusionSuccess
}

// runsPerPage is how many recent runs of each workflow file a poll reads.
const runsPerPage = 20

// DefaultWaitTimeout is how long a deploy waits for a release's image build.
const DefaultWaitTimeout = 20 * time.Minute

// WaitOptions configures GitHub Actions polling when registry tags are missing.
// The wait's budget is its context's deadline.
type WaitOptions struct {
	OwnerRepo     string
	WorkflowFiles []string
	PollInterval  time.Duration
	// GitHub reads the workflow runs; nil builds one from Token through github.Config.
	GitHub *github.Client
	Token  string
	// Out receives progress lines; nil writes to os.Stdout.
	Out io.Writer
}

// DefaultWaitOptions returns deploy defaults (30s poll).
func DefaultWaitOptions() WaitOptions {
	return WaitOptions{
		OwnerRepo: "behaviorengineering/polypus",
		WorkflowFiles: []string{
			"docker-release.yml",
			"packaging-rebuild.yml",
			"auto-patch-release.yml",
		},
		PollInterval: 30 * time.Second,
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
	return opts
}

// githubClient returns opts.GitHub, or a client built from the token (else
// GHCR_TOKEN, GITHUB_TOKEN or GH_TOKEN).
func (o WaitOptions) githubClient() (*github.Client, error) {
	if o.GitHub != nil {
		return o.GitHub, nil
	}
	return github.Config{Token: tokenOr(o.Token), OwnerRepo: o.OwnerRepo}.CreateClient()
}

// ErrImageBuildFailed means the latest matching Actions run did not succeed.
var ErrImageBuildFailed = errors.New("image build workflow failed")

// waitForImages polls GitHub Actions until the release's image workflow succeeds,
// fails, or ctx's deadline (the wait budget, required) passes. It waits while no
// matching run exists yet.
func waitForImages(ctx context.Context, version string, opts WaitOptions) error {
	const op = "images.waitForImages"
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return derrors.New(derrors.CodeFailedPrecondition, op, "context needs a deadline (the image wait budget)")
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
	parsed, err := parseImageTag(version)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInvalid, op, "parse version")
	}
	tag := appGitTag(parsed.App)
	pinTag := version
	if parsed.Rebuild == 0 {
		pinTag = parsed.App
	}
	out := progress.New(opts.Out)
	for {
		if err := ctx.Err(); err != nil {
			return waitEnded(err, op, tag)
		}
		runs, err := opts.matchingRuns(ctx, gh, tag, pinTag)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return waitEnded(ctxErr, op, tag)
			}
			return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "list workflow runs")
		}
		if done, err := imageRunsDone(out, runs, tag); done || err != nil {
			if err == nil {
				err = out.Err()
			}
			return err
		}
		if err := sleepCtx(ctx, opts.PollInterval); err != nil {
			return waitEnded(err, op, tag)
		}
	}
}

// waitEnded is the error for a wait whose context ended: a timeout when its
// deadline passed, a cancel otherwise.
func waitEnded(err error, op, tag string) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return derrors.Wrap(err, derrors.CodeTimeout, op, "timed out waiting for image tags").With("tag", tag)
	}
	return derrors.Wrap(err, derrors.CodeCanceled, op, "wait for "+tag)
}

// imageRunsDone reads one poll of the matching workflow runs: done with a nil error
// when the newest run succeeded, done with an error when it ended any other way,
// and not done while no run exists yet or the newest is still running.
func imageRunsDone(out *progress.Printer, runs []github.WorkflowRun, tag string) (bool, error) {
	if len(runs) == 0 {
		out.Printf("deploy: waiting for image workflow to appear for %s\n", tag)
		return false, nil
	}
	latest := latestRun(runs)
	if runInProgress(latest) {
		out.Printf("deploy: waiting for image build %s\n", latest.HTMLURL)
		return false, nil
	}
	switch c := runConclusion(strings.ToLower(strings.TrimSpace(latest.Conclusion))); c {
	case conclusionSuccess:
		return true, nil
	case conclusionFailure, conclusionCancelled, conclusionTimedOut, conclusionActionRequired,
		conclusionStale, conclusionNeutral, conclusionSkipped, conclusionStartupFailure:
		return true, imageBuildFailed(tag, latest)
	case conclusionNone:
		out.Printf("deploy: waiting for image build %s\n", latest.HTMLURL)
		return false, nil
	default:
		return true, derrors.New(derrors.CodeFailedPrecondition, "images.waitForImages",
			"unknown run conclusion "+strconv.Quote(latest.Conclusion)+" for "+tag+" ("+latest.HTMLURL+")")
	}
}

func imageBuildFailed(tag string, run github.WorkflowRun) error {
	return derrors.Wrap(fmt.Errorf("%w: %s %s (%s)", ErrImageBuildFailed, tag, run.Conclusion, run.HTMLURL),
		derrors.CodeFailedPrecondition, "images.waitForImages", "image build failed")
}

// latestRun returns the most recently created run in runs (which must not be
// empty). Only that run is judged: an older run, of this workflow or another,
// does not hold the wait.
func latestRun(runs []github.WorkflowRun) github.WorkflowRun {
	latest := runs[0]
	for _, r := range runs[1:] {
		if r.CreatedAt.After(latest.CreatedAt) {
			latest = r
		}
	}
	return latest
}

// matchingRuns reads each of o.WorkflowFiles' recent runs once and keeps those of
// the release; the client's outbound policy is the only retry.
func (o WaitOptions) matchingRuns(ctx context.Context, gh *github.Client, tag, version string) ([]github.WorkflowRun, error) {
	var matched []github.WorkflowRun
	for _, wf := range o.WorkflowFiles {
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
