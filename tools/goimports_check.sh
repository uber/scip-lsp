#!/bin/bash
set -euo pipefail

# Check if goimports would make changes to Go files
# Exit with error if formatting is needed

echo "Checking Go imports formatting..."

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

# Check if goimports would make changes
unformatted=$(goimports -l src)

if [ -n "$unformatted" ]; then
    echo "The following Go files need import formatting:"
    echo "$unformatted"
    echo ""
    echo "Run 'bazel run //tools:goimports_fix' to fix formatting"
    exit 1
fi

echo "All Go imports are properly formatted ✓"
