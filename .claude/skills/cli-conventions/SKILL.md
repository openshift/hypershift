---
name: CLI Conventions
description: "Apply HyperShift CLI conventions when working on hypershift or hcp CLI code. Ensures proper cobra patterns, flag naming, and correct CLI placement (hypershift vs hcp)."
---

# HyperShift CLI Conventions

## Two CLIs — Know the Difference

HyperShift maintains two distinct CLIs:

1. **`hypershift` CLI** (`cmd/` directory, entry point `main.go`) — Development, testing, and CI tool. May expose internal, experimental, and debug flags.
2. **`hcp` CLI** (`product-cli/` directory, entry point `product-cli/main.go`) — Customer-facing product CLI. Only stable, supported flags and commands.

**Key pattern**: The `hypershift` CLI uses `BindDeveloperOptions()` to expose developer-only flags; the `hcp` CLI uses `BindOptions()` for customer-facing flags only.

## Flag Exposure Rules

**Only in `hypershift` CLI** (NOT customer-facing):
- Internal image override flags (`--control-plane-operator-image`, `--hypershift-operator-image`)
- Debug and development flags
- CI-specific configuration
- Flags that expose implementation details or bypass safety checks

**In `hcp` CLI** (customer-facing):
- Platform configuration (region, instance type)
- Cluster/nodepool lifecycle parameters
- Networking, auth, scaling, version/release selection

**When in doubt**: A flag should NOT be in the `hcp` CLI until there is a clear product decision.

## Cobra Best Practices

- Always use `RunE` (not `Run`) to properly propagate errors
- Use `cobra.ExactArgs()`, `cobra.NoArgs()`, etc. for argument validation
- Use persistent flags for flags shared across subcommands
- Prefer validation in `PreRunE` over `MarkFlagRequired` for better error messages
- Always provide `Short` and `Long` descriptions, plus `Example` fields
- Use `SilenceUsage: true` and `SilenceErrors: true` on root commands

## HyperShift CLI Patterns

- Use the `Options` struct pattern for collecting flag values
- Implement `Validate()` and `Complete()` methods on options structs
- Use `Run(ctx context.Context)` pattern for command execution
- Ensure proper context propagation through the command chain

## Flag Naming

- Use **kebab-case**: `--node-pool-name` not `--nodePoolName`
- Be descriptive but concise: `--release-image` not `--ri`
- Use consistent prefixes for related flags: `--aws-region`, `--aws-instance-type`
- Use `--no-` prefix for boolean negation flags

## Quick Checklist

Before submitting CLI changes:
- [ ] Flags are in the correct CLI (hypershift vs hcp)
- [ ] `RunE` used instead of `Run`
- [ ] Flag names follow kebab-case convention
- [ ] Options struct has `Validate()` and `Complete()` methods
- [ ] Help text and examples are provided
- [ ] No internal/dev flags leaking into `hcp` CLI
