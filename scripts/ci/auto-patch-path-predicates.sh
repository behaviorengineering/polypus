# Sourced by auto-patch-decide.sh and auto-patch-decide_test.sh.
# Keep inline copies in consumer auto-patch-release CI in sync with this file.
#
# Optional env (space-separated shell globs, matched against repo-relative paths):
#   AUTO_PATCH_EXTRA_HARNESS_PATHS    e.g. "tools/agent-pack tools/agent-pack/*"
#   AUTO_PATCH_EXTRA_PACKAGING_PATHS  e.g. "deploy/helm/*"

_matches_extra_globs() {
  local path="$1"
  local globs="$2"
  local g
  local -a patterns
  [[ -z "${globs}" ]] && return 1
  # read -a splits on spaces without pathname expansion. An unquoted ${globs}
  # would expand ".agent-pack/*" against the checkout and stop matching.
  read -r -a patterns <<< "${globs}"
  for g in "${patterns[@]}"; do
    # shellcheck disable=SC2053
    [[ "${path}" == ${g} ]] && return 0
  done
  return 1
}

# Agent-harness paths: changes here never cut a product tag on their own.
is_harness_only_path() {
  local path="$1"
  case "${path}" in
    lefthook.yml|lefthook/*) return 0 ;;
    .claude/*) return 0 ;;
    CLAUDE.md|*/CLAUDE.md|AGENTS.md|*/AGENTS.md) return 0 ;;
  esac
  _matches_extra_globs "${path}" "${AUTO_PATCH_EXTRA_HARNESS_PATHS:-}" && return 0
  return 1
}

# Packaging paths: changes here publish an image revision X.Y.Z-rN, not a new Go tag.
is_packaging_only_path() {
  local path="$1"
  case "${path}" in
    Dockerfile|Dockerfile.*) return 0 ;;
    scripts/docker-*) return 0 ;;
    scripts/ci/docker-*) return 0 ;;
    .github/workflows/docker-release.yml|.github/workflows/packaging-rebuild.yml) return 0 ;;
    .gitlab/ci/docker-release.yml|.gitlab/ci/packaging-rebuild.yml) return 0 ;;
  esac
  _matches_extra_globs "${path}" "${AUTO_PATCH_EXTRA_PACKAGING_PATHS:-}" && return 0
  return 1
}
