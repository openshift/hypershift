---
name: AWS Hardening
description: "Harden Claude Code's AWS access for safe AI-assisted cloud work. Sets up a read-only IAM role, configures an AWS CLI profile that assumes it via temporary STS credentials, enforces the profile as default through shell-env, wrapper, or SessionStart hook strategies, and locks Claude Code out of ~/.aws with deny rules. Use when the user says 'harden aws', 'aws hardening', 'lock down aws', 'read-only aws', 'aws-hardening', or wants to secure Claude's AWS access."
---

# AWS Hardening

You are guiding the user through hardening Claude Code's AWS access. Have a conversation — discover their environment, explain what you're doing and why, and adapt to their setup. Confirm before making any changes.

## Goal

Ensure Claude Code operates under a read-only IAM role and cannot directly access `~/.aws` (credentials, config, SSO cache). The `aws` CLI still works — it reads `~/.aws` internally.

## Step 0: Onboarding walkthrough

Before doing anything, present the user with a full walkthrough of what this skill does and why. Cover all of the following in a single, detailed explanation:

**The problem**: AI coding agents like Claude Code can run `aws` CLI commands. By default they inherit whatever AWS credentials the user has configured — often long-lived access keys with broad permissions. This means Claude could accidentally (or through prompt injection) run destructive commands like deleting EC2 instances, S3 buckets, or IAM resources.

**The solution**: This skill sets up defense in depth with up to six layers:

1. **A read-only IAM role** with the AWS managed `ReadOnlyAccess` policy (the primary security control — it only grants read actions) and a deny guardrail inline policy as defense-in-depth against policy drift that blocks common destructive actions (delete, terminate, IAM mutations, audit log tampering). Claude assumes this role via STS, getting temporary 1-hour credentials instead of long-lived keys.

2. **An AWS CLI profile** in `~/.aws/config` that automatically assumes the read-only role. Any `aws` command using this profile gets scoped to read-only access.

3. **A default profile enforcement strategy** so Claude always uses the read-only profile. Three options available — shell environment variable, a wrapper script, or a Claude Code session hook — each with different tradeoffs explained in detail during setup.

4. **Claude Code deny rules** that block Claude from directly reading `~/.aws` files (credentials, config, SSO cache). Claude can still run `aws` CLI commands — the CLI reads those files internally — but Claude itself cannot exfiltrate or inspect the raw credentials.

5. **A CLAUDE.md instruction** added to the user's global `~/.claude/CLAUDE.md` that tells Claude to always pass `--profile <name>` on every `aws` CLI command. This is a behavioral guardrail — it shapes what Claude tries to do, ensuring the read-only profile is explicitly specified even if the environment variable is missing or overridden.

6. **(Optional) Fine-grained AWS CLI command permissions** — Claude Code allow/deny rules that whitelist read-only AWS CLI patterns (describe, list, get) and blacklist destructive ones (create, delete, terminate, put, update). Fragile by nature (Bash patterns can be bypassed), but adds another speed bump on top of the IAM role.

**What this changes**: After setup, Claude operates under temporary, read-only AWS credentials. It can describe and list resources across all AWS services but cannot create, modify, or delete anything. The deny rules mean Claude cannot read your `~/.aws/credentials` or `~/.aws/config` files directly.

**What this does NOT change**: Your own AWS access is unaffected. You keep your existing profiles and permissions. Only Claude Code's behavior is constrained.

After presenting this walkthrough, ask the user if they'd like to proceed. Do NOT start any setup steps until they confirm.

## What to do

### 1. Ensure a read-only IAM role exists

Check if a dedicated read-only role exists in the user's account. The typical setup is:
- An IAM role (e.g. `AIAgentReadOnly`) with the AWS managed `ReadOnlyAccess` policy attached
- A deny guardrail inline policy that blocks destructive actions (ec2:Delete*, ec2:Terminate*, iam:Create*, iam:Delete*, cloudtrail:StopLogging, etc.)
- A trust policy scoped to the account (any IAM identity in the account can assume it)

Ask the user what their role is called. If it doesn't exist, explain what it should look like and point them to `${CLAUDE_SKILL_DIR}/references/create-readonly-role.sh` bundled with this skill — read it to show them the exact commands. Do NOT silently create IAM resources.

### 2. Add an AWS CLI profile that assumes the role

The user needs a named profile in `~/.aws/config` that uses `role_arn` + `source_profile` to assume the read-only role. Ask them:
- What they want the profile named (e.g. `ai-agent`)
- What region to default to
- What their source profile is (usually `default`)

Example of what the profile block looks like:
```ini
[profile ai-agent]
role_arn = arn:aws:iam::123456789012:role/AIAgentReadOnly
source_profile = default
duration_seconds = 3600
region = us-east-1
```

If deny rules are already in place blocking `~/.aws`, give the user the exact text and ask them to add it themselves.

### 3. Make the profile the default for Claude Code sessions

Explain all three strategies to the user in depth — what each one does, how it works, where it applies, and what the downsides are. Only after you've walked through all three should you ask the user which one they prefer.

**shell-env** (recommended): Export `AWS_PROFILE=<profile>` in the user's shell rc. Simple, standard. Only applies in new shells. Detect their shell and adapt (fish uses `set -gx`, bash/zsh use `export`).

**wrapper**: A small script at `~/.local/bin/aws` that sets `AWS_PROFILE` then delegates to the real `aws` binary. Works everywhere including subprocesses. But — it shadows the real binary, can break package upgrades, and is an attack surface. Be upfront about this.

**hook** (recommended for Claude Code-only): A Claude Code `SessionStart` hook that writes `export AWS_PROFILE=<profile>` to the `CLAUDE_ENV_FILE`. This env file is sourced by every subsequent Bash command Claude runs in the session, so all `aws` calls automatically use the read-only profile. Add it to `~/.claude/settings.json` under `hooks.SessionStart` with `"matcher": "startup"`. Only affects Claude Code sessions — zero impact on the user's normal shell or other tools.

The hook script should look like:
```bash
#!/bin/bash
if [ -n "$CLAUDE_ENV_FILE" ]; then
  echo 'export AWS_PROFILE=<PROFILE_NAME>' >> "$CLAUDE_ENV_FILE"
fi
exit 0
```

And the settings.json entry:
```json
"SessionStart": [
  {
    "matcher": "startup",
    "hooks": [
      {
        "type": "command",
        "command": "/home/<user>/.claude/hooks/aws-profile-env.sh",
        "args": []
      }
    ]
  }
]
```

This is the cleanest option — every `aws` command inherits the profile automatically without needing to detect or rewrite individual commands.

### 4. Add Claude Code deny rules

Add deny rules to `~/.claude/settings.json` that prevent Claude from directly accessing `~/.aws`. The rules should cover:

- **File tools**: `Read(~/.aws/**)`, `Edit(~/.aws/**)`, `Write(~/.aws/**)`
- **Bash access**: deny patterns for common file-reading commands targeting `~/.aws` (cat, less, head, tail, grep)

If settings already exist, merge carefully (jq or equivalent). If not, create the file. Skip any rules already present.

Explain to the user:
- These rules are **permanent** — they lock Claude Code out of `~/.aws`
- To modify `~/.aws` through Claude later, the user relaxes the rules themselves via `/permissions`
- The `aws` CLI still works fine — it reads credentials internally
- The Bash deny rules are a speed bump, not a complete barrier — commands like `sed`, `awk`, `python3 -c`, or shell builtins can still read files. The primary security layer is the IAM role itself (ReadOnlyAccess + deny guardrail), not the deny rules. The deny rules reduce the surface area of accidental credential exposure

### 5. Add CLAUDE.md instruction

Add a line to the user's global `~/.claude/CLAUDE.md` instructing Claude to always use the `--profile` flag on every `aws` CLI command. The language MUST follow RFC 2119 keywords (MUST, MUST NOT, SHALL, SHOULD, etc.). For example:

```text
- You MUST include `--profile ai-agent` in every `aws` CLI invocation. You MUST NOT run `aws` commands without an explicit `--profile` flag.
```

This is a behavioral layer — it doesn't enforce anything technically, but it shapes what Claude generates. Combined with the other layers (role, profile, enforcement strategy, deny rules), it ensures the read-only profile is used even if the environment variable is unset or a new shell doesn't have it.

Ask the user what profile name to use. Read their existing `~/.claude/CLAUDE.md` first and append — do not overwrite.

### 6. (Optional) Fine-grained AWS CLI command permissions

Offer this as an additional hardening layer. Explain that this is belt-and-suspenders on top of the IAM role — the role already enforces read-only at the API level, but these permission rules control what Claude even *attempts* to run. Also explain that Bash permission patterns are fragile (argument reordering, variables, compound commands can bypass them), so the IAM role remains the real enforcement.

If the user opts in, add both allow AND deny rules to `~/.claude/settings.json`:

**Allow** — read-only AWS CLI patterns:
```text
Bash(aws * describe-*)
Bash(aws * list-*)
Bash(aws * get-*)
Bash(aws sts get-caller-identity)
Bash(aws s3 ls *)
Bash(aws s3 cp s3://* *)
```

**Deny** — write/destructive AWS CLI patterns:
```text
Bash(aws * create-*)
Bash(aws * delete-*)
Bash(aws * terminate-*)
Bash(aws * put-*)
Bash(aws * update-*)
Bash(aws * modify-*)
Bash(aws * run-*)
Bash(aws * start-*)
Bash(aws * stop-*)
Bash(aws * reboot-*)
Bash(aws * attach-*)
Bash(aws * detach-*)
Bash(aws * remove-*)
Bash(aws s3 rm *)
Bash(aws s3 mv *)
```

Ask the user if there are specific AWS commands they need that don't match the allow patterns (e.g. `aws logs filter-log-events`, `aws ce get-cost-and-usage`) and add those. The allow and deny lists should be tailored to how the user actually uses AWS with Claude.

### 7. Status and switching

If the user comes back and asks for status, check: current `AWS_PROFILE`, caller identity, whether deny rules are in place, whether a wrapper is installed.

If they want to switch profiles, help them update whichever strategy they chose. If deny rules block the change, give them the exact edit to make themselves.
