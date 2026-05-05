# Agent Evals

Eval framework using [promptfoo](https://github.com/promptfoo/promptfoo)
for testing SME agent definitions and AGENTS.md conventions.

## Prerequisites

- `claude` CLI installed and authenticated
- Node.js (npx)
- python3

## Usage

```bash
# Run all scenarios
make eval-agents

# Run a specific test
make eval-agents EVAL_FILTER=api-sme

# View results in browser
cd test/eval && npx promptfoo@0.121.9 view

# Output JUnit XML for CI
make eval-agents EVAL_OUTPUT=results.xml
```

## How It Works

- **Test scenarios** are defined inline in `promptfooconfig.yaml` with prompts and `llm-rubric` assertions
- **Patch-based tests** use `beforeEach`/`afterEach` hooks to create a
  temporary git worktree, apply the patch there, and clean up after the test
- **Per-assertion judging**: each expected issue is a separate `llm-rubric`
  assertion graded by an LLM judge
- **Parallel execution**: configurable via `maxConcurrency` in the config
- **Web UI**: `npx promptfoo@0.121.9 view` shows results in a browser with
  side-by-side comparison for iterating on prompts

## Configuration

| Env Var | Default | Description |
|---------|---------|-------------|
| `EVAL_MODEL` | `claude-opus-4-6` | Model for agent invocation |
| `EVAL_FILTER` | (all) | Filter tests by description pattern |
| `EVAL_OUTPUT` | (none) | Output file (.json, .xml, .html) |
| `ANTHROPIC_VERTEX_PROJECT_ID` | - | GCP project for Vertex AI auth |
