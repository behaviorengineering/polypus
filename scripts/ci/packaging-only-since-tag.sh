#!/usr/bin/env bash
# Decides whether the changes since the last v* tag are packaging-only (the image
# rebuild path). It writes skip=false when they are and skip=true otherwise to
# $GITHUB_OUTPUT (stdout outside Actions), and exits 0 either way: a code change is
# not an error, it just means the auto patch release builds the images instead.
#
# The path predicates are shared with auto-patch-decide.sh, so set the same
# AUTO_PATCH_EXTRA_HARNESS_PATHS and AUTO_PATCH_EXTRA_PACKAGING_PATHS as the release
# workflow. Harness paths are neutral: packaging plus harness still rebuilds, which is
# the case auto-patch-decide.sh skips.
set -euo pipefail

_script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=auto-patch-path-predicates.sh
source "${_script_dir}/auto-patch-path-predicates.sh"

decide() {
  local skip="$1" reason="$2"
  echo "packaging-only: ${reason}"
  echo "skip=${skip}" >> "${GITHUB_OUTPUT:-/dev/stdout}"
  exit 0
}

last_tag="$(git describe --tags --abbrev=0 --match 'v*' 2>/dev/null || true)"
if [[ -z "${last_tag}" ]]; then
  decide true "no v* tag found"
fi

files="$(git diff --name-only "${last_tag}" HEAD)"
if [[ -z "${files}" ]]; then
  decide true "no files since ${last_tag}"
fi

packaging="false"
while IFS= read -r f; do
  [[ -z "${f}" ]] && continue
  if is_packaging_only_path "${f}"; then
    packaging="true"
  elif ! is_harness_only_path "${f}"; then
    decide true "not packaging path: ${f}"
  fi
done <<< "${files}"

if [[ "${packaging}" != "true" ]]; then
  decide true "only harness paths since ${last_tag}"
fi
decide false "all paths since ${last_tag} are packaging or harness"
