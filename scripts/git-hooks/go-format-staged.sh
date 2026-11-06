#!/usr/bin/env bash
set -euo pipefail

if [[ $# -eq 0 ]]; then
  exit 0
fi

root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$root"

run_fmt() {
  local f="$1"
  if grep -q 'mvdan.cc/gofumpt' go.mod 2>/dev/null; then
    go tool gofumpt -w "$f"
  elif command -v gofumpt >/dev/null 2>&1; then
    gofumpt -w "$f"
  elif command -v gofmt >/dev/null 2>&1; then
    gofmt -w "$f"
  else
    echo "lefthook: go tool gofumpt, gofumpt, or gofmt required to format staged Go files"
    exit 1
  fi
}

for f in "$@"; do
  if [[ ! -f "$f" ]]; then
    continue
  fi
  run_fmt "$f"
done

exit 0
