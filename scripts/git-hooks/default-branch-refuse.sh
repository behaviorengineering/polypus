#!/usr/bin/env bash
set -euo pipefail

if [[ -n "${HOOK_TEST_BRANCH:-}" ]]; then
  branch="$HOOK_TEST_BRANCH"
else
  branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo HEAD)"
fi

if [[ "$branch" == "HEAD" ]]; then
  exit 0
fi

case "$branch" in
  main|master|develop|trunk)
    echo "lefthook: refuse commit on default branch '$branch'. Use a feature branch or sibling worktree."
    exit 1
    ;;
esac

exit 0
