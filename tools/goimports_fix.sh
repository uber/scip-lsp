#!/bin/bash
set -euo pipefail

# Fix Go import formatting

echo "Fixing Go imports formatting..."

# Change to workspace root if we're in a Bazel execroot
if [[ "$PWD" == *"execroot"* ]]; then
    if [ -n "${BUILD_WORKSPACE_DIRECTORY:-}" ]; then
        cd "$BUILD_WORKSPACE_DIRECTORY"
        echo "Changed to workspace directory: $BUILD_WORKSPACE_DIRECTORY"
    fi
fi

# Ensure goimports is available
if ! command -v goimports &> /dev/null; then
    echo "Installing goimports..."
    go install golang.org/x/tools/cmd/goimports@latest
fi

# Apply goimports to all files
goimports -w src

echo "Go imports formatting applied ✓"
