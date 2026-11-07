package images

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/behaviorengineering/polypus/internal/clients/github"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// ErrPublishIncomplete means upstream auto-patch did not finish green through verify for the tag.
var ErrPublishIncomplete = errors.New("upstream auto-patch publish incomplete")

// autoPatchWorkflow is the run name of the auto-patch release workflow.
const autoPatchWorkflow = "Auto patch release"

// verifyJob is the auto-patch job that marks a release published.
const verifyJob = "verify"

// rebuildRunsPerPage is how many recent runs of each rebuild workflow are checked.
const rebuildRunsPerPage = 30

// VerifyPublishComplete checks that behaviorengineering/polypus Auto patch release succeeded
// through the verify job for the commit tagged vX.Y.Z (publish-complete gate).
func VerifyPublishComplete(ctx context.Context, version string, opts WaitOptions) error {
	const op = "images.VerifyPublishComplete"
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return derrors.New(derrors.CodeInvalid, op, "empty version for publish-complete check")
	}
	parsed, err := parseImageTag(version)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInvalid, op, "parse version")
	}
	opts = applyWaitDefaults(opts)
	gh, err := opts.githubClient()
	if err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeFailedPrecondition), op, "publish-complete check")
	}
	if parsed.Rebuild >= 1 {
		return verifyDockerReleasePublishComplete(ctx, gh, version)
	}
	tag := appGitTag(parsed.App)
	sha, err := gh.TagCommit(ctx, tag)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "resolve tag "+tag).
			With("owner_repo", opts.OwnerRepo)
	}
	runs, err := gh.RunsForSHA(ctx, sha)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "runs for "+sha)
	}
	for _, run := range runs {
		if !strings.EqualFold(run.Name, autoPatchWorkflow) || !succeeded(run.Conclusion) {
			continue
		}
		ok, err := runHasSuccessfulJob(ctx, gh, run.ID, verifyJob)
		if err != nil {
			return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "jobs of run "+run.HTMLURL)
		}
		if ok {
			return nil
		}
	}
	return derrors.Wrap(fmt.Errorf("%w: no successful %s with %s for %s (%s)", ErrPublishIncomplete, autoPatchWorkflow, verifyJob, tag, sha),
		derrors.CodeFailedPrecondition, op, "publish incomplete")
}

// verifyDockerReleasePublishComplete gates -rN pins on a successful Docker release workflow run.
func verifyDockerReleasePublishComplete(ctx context.Context, gh *github.Client, imageTag string) error {
	const op = "images.VerifyPublishComplete"
	want := strings.ToLower(strings.TrimSpace(imageTag))
	for _, wf := range []string{"docker-release.yml", "packaging-rebuild.yml"} {
		runs, err := gh.WorkflowRuns(ctx, wf, rebuildRunsPerPage)
		if err != nil {
			return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "runs of "+wf)
		}
		for _, wr := range runs {
			if !succeeded(wr.Conclusion) {
				continue
			}
			if strings.Contains(strings.ToLower(wr.Name+"\n"+wr.DisplayTitle), want) {
				return nil
			}
		}
	}
	return derrors.Wrap(fmt.Errorf("%w: no successful Docker release for image tag %s", ErrPublishIncomplete, imageTag),
		derrors.CodeFailedPrecondition, op, "publish incomplete")
}

func runHasSuccessfulJob(ctx context.Context, gh *github.Client, runID int64, jobName string) (bool, error) {
	jobs, err := gh.RunJobs(ctx, runID)
	if err != nil {
		return false, err
	}
	want := strings.ToLower(strings.TrimSpace(jobName))
	for _, j := range jobs {
		if strings.ToLower(strings.TrimSpace(j.Name)) == want && succeeded(j.Conclusion) {
			return true, nil
		}
	}
	return false, nil
}
