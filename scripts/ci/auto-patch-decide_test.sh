#!/usr/bin/env bash
# Smoke tests for auto-patch-path-predicates.sh and the skip marker in
# auto-patch-decide.sh (the marker cases build a throwaway git repo).
set -euo pipefail

_script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=auto-patch-path-predicates.sh
source "${_script_dir}/auto-patch-path-predicates.sh"

failures=0

check() {
  local fn="$1" want="$2" path="$3"
  local got="false"
  if "${fn}" "${path}"; then got="true"; fi
  if [[ "${got}" != "${want}" ]]; then
    echo "FAIL: ${fn} ${path}: want ${want}, got ${got}" >&2
    failures=$((failures + 1))
  fi
}

check is_harness_only_path true  "lefthook.yml"
check is_harness_only_path true  "lefthook/lefthook-go-standalone.yml"
check is_harness_only_path true  ".claude/skills/foo/SKILL.md"
check is_harness_only_path true  "CLAUDE.md"
check is_harness_only_path true  "pkg/engine/AGENTS.md"
check is_harness_only_path false "engine/foo.go"
check is_harness_only_path false ".gitlab/ci/auto-patch-release.yml"
check is_harness_only_path false "tools/agent-pack"

AUTO_PATCH_EXTRA_HARNESS_PATHS="tools/agent-pack tools/agent-pack/*"
check is_harness_only_path true  "tools/agent-pack"
check is_harness_only_path true  "tools/agent-pack/skills/x.md"
unset AUTO_PATCH_EXTRA_HARNESS_PATHS

check is_packaging_only_path true  "Dockerfile"
check is_packaging_only_path true  "Dockerfile.prod"
check is_packaging_only_path true  "scripts/docker-build.sh"
check is_packaging_only_path true  ".github/workflows/docker-release.yml"
check is_packaging_only_path true  ".gitlab/ci/docker-release.yml"
check is_packaging_only_path false "pkg/foo/foo.go"
check is_packaging_only_path false "deploy/helm/values.yaml"

AUTO_PATCH_EXTRA_PACKAGING_PATHS="deploy/helm/*"
check is_packaging_only_path true  "deploy/helm/values.yaml"
unset AUTO_PATCH_EXTRA_PACKAGING_PATHS

_tmp="$(mktemp -d)"
trap 'rm -rf "${_tmp}"' EXIT

# A glob must still match when a file in the working directory also matches it.
mkdir -p "${_tmp}/glob/.agent-pack"
: > "${_tmp}/glob/.agent-pack/present.md"
cd "${_tmp}/glob"
AUTO_PATCH_EXTRA_HARNESS_PATHS=".agent-pack/*"
check is_harness_only_path true ".agent-pack/skills/x.md"
unset AUTO_PATCH_EXTRA_HARNESS_PATHS
cd - >/dev/null

# The skip marker counts in the subject line only.
_repo="${_tmp}/repo"
git init -q "${_repo}"
: > "${_repo}/main.go"
git -C "${_repo}" add main.go
git -C "${_repo}" -c user.name=test -c user.email=test@example.com commit -q -m "feat: add main"

decide_reason() {
  local msg="$1" out="${_tmp}/decide.out"
  : > "${out}"
  (cd "${_repo}" && OUTPUT_FILE="${out}" HEAD_MSG="${msg}" bash "${_script_dir}/auto-patch-decide.sh")
  sed -n 's/^reason=//p' "${out}"
}

check_marker() {
  local want="$1" msg="$2" got="false"
  if [[ "$(decide_reason "${msg}")" == *"[skip release]"* ]]; then got="true"; fi
  if [[ "${got}" != "${want}" ]]; then
    echo "FAIL: skip marker for $(printf '%q' "${msg}"): want ${want}, got ${got}" >&2
    failures=$((failures + 1))
  fi
}

check_marker true  "feat: add main [skip release]"
check_marker false $'feat: add main\n\nThe [skip release] marker is documented here.'

if [[ "${failures}" -gt 0 ]]; then
  echo "${failures} auto-patch test(s) failed" >&2
  exit 1
fi

echo "auto-patch-decide_test: ok"
