#!/bin/bash

# Pre-push hook: Verify code quality before git push
# Reads JSON input from stdin and checks if the command contains "git push"

# Exit on pipeline failures
set -o pipefail

# Read JSON from stdin
input=$(cat)

# Extract the command using jq
command=$(echo "$input" | jq -r '.tool_input.command // empty')

# Check if the command contains "git push"
if [[ "$command" == *"git push"* ]]; then
    echo "🔍 Running make verify before git push..."

    # Run verification
    if make verify; then
        echo "✅ Verification passed! Proceeding with git push..."
        exit 0
    else
        echo "❌ Verification failed! Blocking git push."
        echo "Please fix the issues before pushing."
        exit 1
    fi
fi

# Not a git push command, allow it to proceed
exit 0
