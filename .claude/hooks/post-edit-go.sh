#!/bin/bash
# PostToolUse hook for Go file edits
# Receives JSON input via stdin with tool_input.file_path

# Exit on pipeline failures
set -o pipefail

# Read JSON from stdin
input=$(cat)

# Extract file_path from JSON (handles both tool_input and tool_response)
file_path=$(echo "$input" | grep -o '"file_path"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"file_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
if [ -z "$file_path" ]; then
  file_path=$(echo "$input" | grep -o '"filePath"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"filePath"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
fi

# Exit if no file_path found
if [ -z "$file_path" ]; then
  exit 0
fi

# Run UPDATE=true go test for cmd files
if [[ "$file_path" == */cmd/*.go ]] && [[ "$file_path" != */vendor/* ]]; then
  echo "Running UPDATE=true go test ./..."
  if ! UPDATE=true go test ./... 2>&1; then
    echo "ERROR: go test failed" >&2
    exit 2
  fi
fi

# Regenerate API if file is in api/
if [[ "$file_path" == */api/* ]] && [[ "$file_path" == *.go ]]; then
  echo "Regenerating API resources..."
  if ! make api 2>&1; then
    echo "ERROR: make api failed" >&2
    exit 2
  fi
fi

# Run make lint-fix for all Go files outside vendor
if [[ "$file_path" == *.go ]] && [[ "$file_path" != */vendor/* ]]; then
  echo "Running make lint-fix for: $file_path"
  if ! make lint-fix 2>&1; then
    echo "ERROR: make lint-fix failed" >&2
    exit 2
  fi
fi

# Run tests for test files
if [[ "$file_path" == *_test.go ]] && [[ "$file_path" != */vendor/* ]]; then
  echo "Running tests..."
  if ! make test 2>&1; then
    echo "ERROR: make test failed" >&2
    exit 2
  fi
fi
