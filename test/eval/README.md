# Agent & Convention Evals

Evaluation framework for testing Claude Code agent definitions and
AGENTS.md conventions. Each scenario sends a prompt to an agent (or
base Claude), then uses an LLM judge to check the output against
expected issues.

## Prerequisites

- `claude` CLI installed and authenticated
- Go 1.25+

## Quick Start

```bash
# Run all scenarios in parallel
make eval-agents

# Run a single agent
make eval-api-sme

# Run with verbose output
make eval-agents EVAL_FOCUS=api-sme EVAL_VERBOSE=1

# Multiple runs with pass-rate threshold
make eval-agents EVAL_RUNS=5 EVAL_THRESHOLD=0.6
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `EVAL_MODEL` | `claude-opus-4-6` | Model for agent invocation |
| `EVAL_JUDGE_MODEL` | `claude-opus-4-6` | Model for judging |
| `EVAL_RUNS` | `1` | Number of trials per scenario |
| `EVAL_THRESHOLD` | `0.8` | Minimum pass rate (0.0-1.0) |
| `EVAL_FOCUS` | | Ginkgo focus filter (substring match) |
| `EVAL_VERBOSE` | | Set to `1` for verbose agent output |

## Directory Structure

```
test/eval/
  eval_test.go                         # Test harness
  testdata/
    sme-agents/                        # Agent scenarios (uses --agent flag)
      <agent-name>/
        <scenario>/
          prompt.txt                   # Input prompt
          expected.txt                 # Expected issues, one per line
          patch.diff                   # Optional: applied before run
    conventions/                       # Convention tests (no agent)
      <scenario>/
        prompt.txt
        expected.txt
```

## Adding a Scenario

1. Create a directory under `sme-agents/<agent>/` or `conventions/`
2. Add `prompt.txt` with the input prompt
3. Add `expected.txt` with expected issues, one per line
4. Optionally add `patch.diff` to apply code changes before the run
5. Run it: `make eval-agents EVAL_FOCUS=<scenario-name> EVAL_VERBOSE=1`
6. Iterate on `expected.txt` until the pass rate is stable

The make target is auto-discovered — no Makefile changes needed.

## How It Works

1. **Discovery**: scans `testdata/` for scenarios with `prompt.txt`
   and `expected.txt`
2. **Patch** (optional): applies `patch.diff` to the repo so agents
   can run tools against real code (e.g., `make api-lint-fix`)
3. **Agent invocation**: runs `claude --agent <name> -p <prompt>`
   with tools enabled if a patch is present, disabled otherwise
4. **Judge**: a separate Claude call checks the agent output against
   expected issues using semantic matching
5. **Pass rate**: runs N trials (`EVAL_RUNS`), asserts the pass rate
   meets the threshold (`EVAL_THRESHOLD`)
6. **Cleanup**: reverts any patches applied

## Scenario Types

### SME Agent Scenarios (`sme-agents/`)

Test specific agent definitions (`.claude/agents/<name>.md`). The
agent is invoked with `--agent <name>`. Use `patch.diff` to place
code in the repo for the agent to review with its tools.

### Convention Scenarios (`conventions/`)

Test that base Claude (no agent) follows AGENTS.md conventions.
Useful for validating that code style rules, naming conventions,
and other repo-wide policies are applied.

## Cost

Each scenario costs ~$0.50-2.00 (agent + judge). A full run of all
6 scenarios costs ~$5-15 depending on how much the agent reads.
