#!/usr/bin/env bash
# Decide whether an auto-patch release should run.
#
# Writes key=value lines to OUTPUT_FILE or GITHUB_OUTPUT (GitHub Actions).
# Requires: full git history (fetch-depth 0 / GIT_DEPTH 0).
#
# Env:
#   OUTPUT_FILE   GitLab dotenv or local test output (preferred when set)
#   GITHUB_OUTPUT GitHub Actions step output (fallback)
#   EVENT_NAME    push (default), workflow_dispatch, or web (manual bump)
#   MANUAL_BUMP   patch|minor|major when manual
#   HEAD_MSG      tip commit message; only its subject line is checked for
#                 [skip release], so a body may mention the marker safely
#   AUTO_PATCH_EXTRA_HARNESS_PATHS / AUTO_PATCH_EXTRA_PACKAGING_PATHS
#                 extra globs, see auto-patch-path-predicates.sh
#
# Edge cases:
#   - Gitlink-only bumps of a harness submodule listed in
#     AUTO_PATCH_EXTRA_HARNESS_PATHS are harness-only (no product tag).
#   - Mixed harness + product paths are releasable.
#   - Mixed harness + packaging paths are not: they publish an image revision -rN
#     (packaging-only-since-tag.sh treats harness paths as neutral the same way).
#   - Manual (workflow_dispatch / web) skips subject/path filters; still refuses existing tags.
#   - Empty file list does not path-skip (falls through to bump).
set -euo pipefail

_script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=auto-patch-path-predicates.sh
source "${_script_dir}/auto-patch-path-predicates.sh"

decide_output="${OUTPUT_FILE:-${GITHUB_OUTPUT:-}}"
if [[ -z "${decide_output}" ]]; then
  echo "auto-patch-decide: OUTPUT_FILE or GITHUB_OUTPUT is required" >&2
  exit 1
fi

write_out() {
  printf '%s\n' "$1" >> "${decide_output}"
}

is_manual_event() {
  case "${EVENT_NAME:-push}" in
    workflow_dispatch|web) return 0 ;;
    *) return 1 ;;
  esac
}

# Only the subject line counts: GitHub passes the full message, and a body that
# explains the skip marker must not trigger it.
head_subject="${HEAD_MSG:-}"
head_subject="${head_subject%%$'\n'*}"
if [[ "${head_subject}" == *"[skip release]"* ]]; then
  write_out "skip=true"
  write_out "reason=commit contains [skip release]"
  exit 0
fi

last_tag="$(git describe --tags --abbrev=0 --match 'v*' 2>/dev/null || true)"
if [[ -z "${last_tag}" ]]; then
  last_tag="v0.0.0"
fi
write_out "last_tag=${last_tag}"

bump="patch"
if is_manual_event; then
  bump="${MANUAL_BUMP:-patch}"
else
  range="${last_tag}..HEAD"
  if [[ "${last_tag}" == "v0.0.0" ]]; then
    range="HEAD"
    subjects="$(git log -1 --pretty=%s)"
    files="$(git diff-tree --no-commit-id --name-only -r HEAD)"
  else
    subjects="$(git log --pretty=%s "${range}")"
    files="$(git diff --name-only "${last_tag}" HEAD)"
  fi

  if [[ -z "${subjects}" ]]; then
    write_out "skip=true"
    write_out "reason=no commits since ${last_tag}"
    exit 0
  fi

  releasable="false"
  while IFS= read -r subject; do
    [[ -z "${subject}" ]] && continue
    lower="$(printf '%s' "${subject}" | tr '[:upper:]' '[:lower:]')"
    case "${lower}" in
      docs:*|docs\(*|chore:*|chore\(*|ci:*|ci\(*)
        ;;
      merge\ *)
        ;;
      *)
        releasable="true"
        ;;
    esac
  done <<< "${subjects}"

  if [[ "${releasable}" != "true" ]]; then
    write_out "skip=true"
    write_out "reason=only docs/chore/ci commits since ${last_tag}"
    exit 0
  fi

  if [[ -n "${files}" ]]; then
    harness_only="true"
    while IFS= read -r f; do
      [[ -z "${f}" ]] && continue
      if ! is_harness_only_path "${f}"; then
        harness_only="false"
        break
      fi
    done <<< "${files}"
    if [[ "${harness_only}" == "true" ]]; then
      write_out "skip=true"
      write_out "reason=only agent-harness paths since ${last_tag} (.claude/, CLAUDE.md, AGENTS.md, lefthook)"
      exit 0
    fi

    # Harness paths are neutral here: packaging plus harness ships no new binary.
    packaging_or_harness="true"
    while IFS= read -r f; do
      [[ -z "${f}" ]] && continue
      if ! is_packaging_only_path "${f}" && ! is_harness_only_path "${f}"; then
        packaging_or_harness="false"
        break
      fi
    done <<< "${files}"
    if [[ "${packaging_or_harness}" == "true" ]]; then
      write_out "skip=true"
      write_out "reason=packaging and harness only (use Docker release -rN)"
      exit 0
    fi
  fi
fi

ver="${last_tag#v}"
IFS='.' read -r maj min pat <<< "${ver}"
maj="${maj:-0}"
min="${min:-0}"
pat="${pat:-0}"
case "${bump}" in
  major) maj=$((maj + 1)); min=0; pat=0 ;;
  minor) min=$((min + 1)); pat=0 ;;
  patch) pat=$((pat + 1)) ;;
  *)
    echo "unknown bump: ${bump}" >&2
    exit 1
    ;;
esac
new_tag="v${maj}.${min}.${pat}"

if git rev-parse "${new_tag}" >/dev/null 2>&1; then
  write_out "skip=true"
  write_out "reason=tag ${new_tag} already exists"
  exit 0
fi

write_out "skip=false"
write_out "bump=${bump}"
write_out "new_tag=${new_tag}"
