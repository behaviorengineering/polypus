#!/usr/bin/env bash
# Decides whether the changes since the last v* tag are packaging-only (the image
# rebuild path). It writes skip=false when they are and skip=true otherwise to
# $GITHUB_OUTPUT (stdout outside Actions), and exits 0 either way: a code change is
# not an error, it just means the auto patch release builds the images instead.
set -euo pipefail

is_packaging_only_path() {
  local path="$1"
  case "${path}" in
    Dockerfile|Dockerfile.*|docker-bake.hcl) return 0 ;;
    scripts/docker-*) return 0 ;;
    scripts/ci/docker-*) return 0 ;;
    scripts/ci/switchyard-smoke-routes.toml) return 0 ;;
    .github/workflows/docker-release.yml|.github/workflows/packaging-rebuild.yml) return 0 ;;
  esac
  return 1
}

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

while IFS= read -r f; do
  [[ -z "${f}" ]] && continue
  if ! is_packaging_only_path "${f}"; then
    decide true "not packaging path: ${f}"
  fi
done <<< "${files}"

decide false "all paths since ${last_tag} are packaging-only"
