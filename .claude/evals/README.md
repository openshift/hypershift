# Agent Eval Framework

Lightweight smoke tests for validating Claude Code agent definitions before landing changes. Presents agents with known problems, checks acceptance criteria, and compares outputs against baselines.

## Quick Start

```bash
# Run all scenarios
make eval-agents

# Capture baselines (run once with known-good agent definitions)
make eval-agents-baseline

# Run specific agent scenarios
.claude/evals/run-eval.sh agents/api-sme

# Dry run (show commands without invoking claude)
.claude/evals/run-eval.sh --dry-run

# Override model
.claude/evals/run-eval.sh --model opus
```

## Dependencies

- `yq` v4+ (`brew install yq`)
- `claude` CLI

## How It Works

1. Each scenario YAML defines a **prompt** (the known problem) and **criteria** (acceptance checks)
2. The runner invokes `claude -p` with the scenario prompt, targeting the specified agent
3. Output is checked against criteria using pattern matching or LLM-as-judge
4. If a baseline exists, output is compared against it for regression detection

## Scenario Format

```yaml
name: "agent-name: short description"
type: agent
target: api-sme                    # matches .claude/agents/<name>.md

invocation:
  model: sonnet                    # model to use
  allowed_tools: "Read,Glob,Grep"  # tools the agent can use
  max_budget_usd: 1.00             # cost cap

prompt: |
  Your known problem here...

criteria:
  - id: criterion-name
    description: "Human-readable description"
    check_type: llm_judge          # pattern | contains | not_contains | llm_judge
    judge_prompt: |
      Evaluation question for the judge.
      Answer PASS or FAIL.
```

### Check Types

| Type | Description | Parameters |
|------|-------------|------------|
| `pattern` | Regex match on output | `pattern`, `min_matches` |
| `contains` | Substring check (all must match) | `values[]` |
| `not_contains` | Substring check (none should match) | `values[]` |
| `llm_judge` | Semantic evaluation by a second Claude call | `judge_prompt` |

## Adding a Scenario

1. Create a YAML file in `scenarios/agents/<agent-name>/`
2. Define the prompt and criteria
3. Test it: `.claude/evals/run-eval.sh agents/<agent-name>/<file>.yaml`
4. Capture baseline: `.claude/evals/run-eval.sh --baseline agents/<agent-name>/<file>.yaml`
5. Commit the scenario and baseline together

## Workflow for Agent Changes

```
1. Run eval to verify current state     →  make eval-agents
2. Make changes to agent definitions    →  edit .claude/agents/*.md
3. Run eval again                       →  make eval-agents
4. Check for regressions                →  compare results + baseline status
5. If satisfied, update baselines       →  make eval-agents-baseline
6. Commit changes + updated baselines
```

## Directory Structure

```
.claude/evals/
  run-eval.sh          # Runner script
  judge-prompt.md      # System prompt for LLM-as-judge
  scenarios/           # Scenario definitions (committed)
    agents/
      api-sme/
        01-api-design-review.yaml
      ...
  baselines/           # Known-good outputs (committed)
  results/             # Per-run outputs (gitignored)
```

## Cost

Each full eval run (6 agent scenarios with sonnet + haiku judge) costs approximately $3-8 depending on output length. Individual scenarios cost ~$0.50-1.50.
