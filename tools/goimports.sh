#!/bin/bash
set -euo pipefail

# Unified goimports tool. Usage:
#   goimports_common.sh check   # verify formatting (default)
#   goimports_common.sh fix     # apply formatting
# Environment variables:
#   GOIMPORTS_PATHS   Space separated list of directories / files to process (default: src)

MODE="${1:-check}"
TARGET_PATHS=${GOIMPORTS_PATHS:-src}

echo "goimports mode: $MODE"

go_root_workspace() {
  if [[ "$PWD" == *"execroot"* ]]; then
    if [ -n "${BUILD_WORKSPACE_DIRECTORY:-}" ]; then
      cd "$BUILD_WORKSPACE_DIRECTORY"
      echo "Changed to workspace directory: $BUILD_WORKSPACE_DIRECTORY"
    fi
  fi
}

setup_path() {
  GOBIN_DIR="$(go env GOBIN || true)"
  if [ -z "${GOBIN_DIR}" ]; then
    GOBIN_DIR="$(go env GOPATH)/bin"
  fi
  export PATH="$GOBIN_DIR:$PATH"
}

ensure_goimports() {
  if ! command -v goimports >/dev/null 2>&1; then
    echo "Installing goimports..."
    go install golang.org/x/tools/cmd/goimports@latest
  fi
  if ! command -v goimports >/dev/null 2>&1; then
    echo "goimports not found after installation" >&2
    exit 2
  fi
}

run_check() {
  # We run once for listing; goimports does not accept multiple dirs with -l reliably when globs missing
  unformatted=""
  for p in $TARGET_PATHS; do
    # Accumulate outputs; ignore errors for non-existing paths
    if [ -e "$p" ]; then
      out=$(goimports -l "$p" || true)
      if [ -n "$out" ]; then
        unformatted+="$out\n"
      fi
    fi
  done
  if [ -n "$unformatted" ]; then
    echo "The following Go files need import formatting:" | sed 's/\\n$//'
    # De-duplicate & sort
    printf "%b" "$unformatted" | sed '/^$/d' | sort -u
    echo ""
    echo "Run 'bazel run //tools:goimports_fix' to fix formatting"
    exit 1
  fi
  echo "All Go imports are properly formatted"
}

run_fix() {
  for p in $TARGET_PATHS; do
    if [ -e "$p" ]; then
      goimports -w "$p"
    fi
  done
  echo "Go imports formatting applied"
}

main() {
  go_root_workspace
  setup_path
  ensure_goimports
  case "$MODE" in
    check) run_check ;;
    fix) run_fix ;;
    *) echo "Unknown mode: $MODE (expected 'check' or 'fix')" >&2; exit 3 ;;
  esac
}

main "$@"
