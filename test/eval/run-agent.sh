#!/bin/bash
# Wrapper for promptfoo exec: provider
# $1 = prompt, $2 = options JSON, $3 = context JSON
set -euo pipefail

PROMPT="$1"
CONTEXT="${3:-"{}"}"

# Extract agent, tools, and worktree path from context vars
AGENT=$(echo "$CONTEXT" | python3 -c "import sys,json; v=json.load(sys.stdin).get('vars',{}); print(v.get('agent',''))" 2>/dev/null)
TOOLS=$(echo "$CONTEXT" | python3 -c "import sys,json; v=json.load(sys.stdin).get('vars',{}); print(v.get('tools','Read,Grep,Glob'))" 2>/dev/null)
WORKDIR=$(echo "$CONTEXT" | python3 -c "import sys,json; v=json.load(sys.stdin).get('vars',{}); print(v.get('worktreePath',''))" 2>/dev/null)

ARGS=(
  --print
  --model "${EVAL_MODEL:-claude-opus-4-6}"
  --allowed-tools "$TOOLS"
  --no-session-persistence
  --output-format text
)

if [ -n "$AGENT" ]; then
  ARGS+=(--agent "$AGENT")
fi

# Use worktree if available, otherwise repo root
if [ -n "$WORKDIR" ] && [ -d "$WORKDIR" ]; then
  cd "$WORKDIR"
else
  cd "$(dirname "$0")/../.." || exit 1
fi

printf '%s' "$PROMPT" | exec claude "${ARGS[@]}"
