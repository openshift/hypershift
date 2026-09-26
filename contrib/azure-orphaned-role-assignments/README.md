# azure-orphaned-role-assignments

A tool to find (and optionally delete) orphaned Azure RBAC role assignments left
behind by deleted HyperShift/ARO HCP clusters.

## Problem

Azure enforces a limit on the number of role assignments **per subscription**
(4000 by default, raisable to 5000 via a support request). That counter includes
every role assignment in the subscription hierarchy — at the subscription **and**
at every resource group and resource beneath it.

When HyperShift/ARO HCP clusters are torn down in CI, the managed identities
(service principals) created for their control-plane components are deleted, but
role assignments granted to those identities are **not** always cleaned up. The
most common offenders are `Key Vault Secrets User` grants scoped to the shared
`os4-common` resource group (which holds the CI Key Vault): each ephemeral cluster
adds several, and they accumulate as thousands of orphaned assignments pointing at
principals that no longer exist.

These orphans:
- Silently consume the per-subscription role-assignment quota, eventually blocking
  new clusters from being provisioned.
- Are invisible in the subscription's **Access control (IAM)** blade, which only
  lists assignments at (and inherited above) the subscription scope — the "All (N)"
  list. The bulk of orphans live at resource-group/resource scope and never appear
  there.

## How It Works

1. Lists **all** role assignments in the subscription, including those scoped to
   resource groups and individual resources (`NewListForSubscriptionPager`).
2. Resolves every distinct principal against **Microsoft Graph**
   (`directoryObjects/getByIds`). Any principal ID that Graph does not return has
   been deleted from the directory — its assignments are orphaned.
3. Reports the totals, a breakdown by scope level (showing how few are visible in
   the portal IAM list), and the orphans grouped by role and resource group.
4. In dry-run (the default) it only prints. With `-dry-run=false` it deletes the
   selected orphaned assignments (`DeleteByID`).

### Safety guardrails

- **Only deletes within the target subscription.** Assignments inherited from
  management-group or root (tenant) scope are reported but never passed to
  `DeleteByID`, since other subscriptions may rely on them.
- **Min-age guard.** Assignments created within `-min-age` (default 24h) are
  skipped, so a grant for a freshly-created principal that Microsoft Graph has not
  yet propagated is not mistaken for an orphan and deleted.
- **Non-zero exit on failure.** If any deletion fails, the remaining candidates
  are still attempted and the command exits with an error.
- **Minimal logging by default.** Raw principal IDs, the subscription ID, and full
  ARM scopes are only logged under `-verbose`; the default output is aggregate
  counts plus the assignment ID and role name being deleted.

## Usage

```bash
# Build
go build -o azure-orphaned-role-assignments .

# Dry run (default) - see what would be deleted
./azure-orphaned-role-assignments -subscription-id <subscription-id>

# Dry run with per-assignment detail
./azure-orphaned-role-assignments -subscription-id <subscription-id> -verbose

# Restrict cleanup to the shared Key Vault resource group and the Key Vault role
./azure-orphaned-role-assignments \
  -subscription-id <subscription-id> \
  -scope-filter os4-common \
  -role-filter "Key Vault Secrets User"

# Actually delete the orphaned assignments
./azure-orphaned-role-assignments -subscription-id <subscription-id> -dry-run=false
```

### Flags

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `-subscription-id` | Yes | | Azure subscription ID |
| `-dry-run` | No | `true` | Preview changes without deleting |
| `-verbose` | No | `false` | Show individual assignments and Graph batch progress |
| `-scope-filter` | No | | Only consider assignments whose scope contains this substring (e.g. a resource group name) |
| `-role-filter` | No | | Comma-separated role names to restrict to (substring, case-insensitive) |
| `-principal-types` | No | `ServicePrincipal` | Comma-separated principal types to consider (e.g. `ServicePrincipal,User,Group`) |
| `-min-age` | No | `24h` | Only consider assignments created at least this long ago (guards against Graph propagation lag). Set to `0` to disable. |

## Authentication

Uses `DefaultAzureCredential`, which supports:
- Environment variables (`AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET`, `AZURE_TENANT_ID`)
- Azure CLI (`az login`)
- Managed Identity

The identity used needs, at minimum:
- **Read** on role assignments and **delete** (e.g. `Microsoft.Authorization/roleAssignments/*`,
  via `User Access Administrator` or `Owner`) on the target scopes to remove orphans.
- **Directory read** in Microsoft Graph (e.g. `Directory.Read.All`, or the `Directory Readers`
  role) so deleted principals can be detected. The Azure CLI's own `az role assignment list`
  uses the same Graph access to resolve principal names.

## Example

```bash
./azure-orphaned-role-assignments -subscription-id 5f99720c-6823-4792-8a28-69efb0719eea

# Output:
# Listing all role assignments in subscription 5f99720c-... (this includes resource group and resource scopes)
# Total role assignments in subscription (all scopes): 3555
# Breakdown by scope level (only subscription/management-group/root show in the portal IAM list):
#     3346  resource group
#      114  resource
#       78  subscription
#        9  management group
#        8  root (tenant)
# Resolving 2845 distinct principals against Microsoft Graph to detect deleted identities...
# Principals still present in directory: 155; deleted (orphaned): 2690
# Orphaned role assignments selected for cleanup: 2701
# Orphaned assignments by role:
#     2631  Key Vault Secrets User
#       ...
# Orphaned assignments by resource group (top 25):
#     2675  os4-common
#       ...
# DRY RUN: would delete 2701 orphaned role assignments
# To actually delete them, run with -dry-run=false
```

This illustrates the portal discrepancy: the subscription IAM blade shows only the
~95 assignments at subscription/management-group/root scope, while the quota counter
reflects the full total (thousands), the vast majority orphaned and scoped to
resource groups.

## Disclaimer

Always run with the default dry-run first and review the breakdown. Deletion is
driven by principal existence in the directory, so a principal that is temporarily
unresolvable (e.g. cross-tenant, or insufficient Graph permissions) could be
misclassified — verify your identity has directory read access before deleting.
The `-min-age` guard mitigates propagation lag for freshly-created principals, but
it is not a substitute for confirming Graph access. Use `-scope-filter` /
`-role-filter` to constrain cleanup to known-safe targets (such as the
`os4-common` Key Vault grants) when in doubt.
