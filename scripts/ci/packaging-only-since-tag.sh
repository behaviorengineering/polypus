#!/usr/bin/env bash
# Exit 0 when every file changed since the last v* tag is packaging-only (image rebuild path).
set -euo pipefail

is_packaging_only_path() {
  local path="$1"
  case "${path}" in
    Dockerfile|Dockerfile.*) return 0 ;;
    scripts/docker-*) return 0 ;;
    scripts/ci/docker-*) return 0 ;;
    scripts/ci/switchyard-smoke-routes.toml) return 0 ;;
    .github/workflows/docker-release.yml|.github/workflows/packaging-rebuild.yml) return 0 ;;
  esac
  return 1
}

last_tag="$(git describe --tags --abbrev=0 --match 'v*' 2>/dev/null || true)"
if [[ -z "${last_tag}" ]]; then
  echo "packaging-only: no v* tag found" >&2
  exit 1
fi

files="$(git diff --name-only "${last_tag}" HEAD)"
if [[ -z "${files}" ]]; then
  echo "packaging-only: no files since ${last_tag}" >&2
  exit 1
fi

while IFS= read -r f; do
  [[ -z "${f}" ]] && continue
  if ! is_packaging_only_path "${f}"; then
    echo "packaging-only: not packaging path: ${f}" >&2
    exit 1
  fi
done <<< "${files}"

echo "packaging-only: all paths since ${last_tag} are packaging-only"
