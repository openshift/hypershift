# cleanzones-azure

A tool to clean up orphaned ExternalDNS records in Azure DNS zones left behind by deleted HyperShift/ARO HCP clusters.

## Problem

When HyperShift clusters are deleted, ExternalDNS records in Azure DNS zones are not automatically cleaned up. This leads to:
- Accumulation of orphaned A and TXT records
- Potential conflicts with new clusters
- DNS zone bloat

## How It Works

1. Scans all resource groups in the subscription to find active cluster infraIDs
2. Lists all DNS records in the specified zone
3. Identifies records belonging to clusters (by infraID pattern)
4. Deletes only records where the infraID no longer has a corresponding resource group

## Usage

```bash
# Build
go build -o cleanzones-azure .

# Dry run (default) - see what would be deleted
./cleanzones-azure \
  -subscription-id <subscription-id> \
  -dns-zone-rg <dns-zone-resource-group> \
  -dns-zone-name <dns-zone-name>

# Dry run with verbose output (shows each record)
./cleanzones-azure \
  -subscription-id <subscription-id> \
  -dns-zone-rg <dns-zone-resource-group> \
  -dns-zone-name <dns-zone-name> \
  -verbose

# Actually delete orphaned records
./cleanzones-azure \
  -subscription-id <subscription-id> \
  -dns-zone-rg <dns-zone-resource-group> \
  -dns-zone-name <dns-zone-name> \
  -dry-run=false
```

### Flags

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `-subscription-id` | Yes | | Azure subscription ID |
| `-dns-zone-rg` | Yes | | Resource group containing the DNS zone |
| `-dns-zone-name` | Yes | | DNS zone name (base domain) |
| `-dry-run` | No | `true` | Preview changes without deleting |
| `-verbose` | No | `false` | Show individual records in dry run |
| `-infra-rg` | No | | Filter resource groups by prefix |

## Authentication

Uses `DefaultAzureCredential` which supports:
- Environment variables (`AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET`, `AZURE_TENANT_ID`)
- Azure CLI (`az login`)
- Managed Identity

## Example

```bash
# Check for orphaned records in AKS e2e test zone
./cleanzones-azure \
  -subscription-id 5f99720c-6823-4792-8a28-69efb0719eea \
  -dns-zone-rg os4-common \
  -dns-zone-name aks-e2e.hypershift.azure.devcluster.openshift.com

# Output:
# 2025/12/01 09:30:00 Found 15 active cluster infrastructure IDs
# 2025/12/01 09:30:01 Listing DNS records in zone aks-e2e.hypershift.azure.devcluster.openshift.com
# 2025/12/01 09:30:02 Total records in zone: 5000
# 2025/12/01 09:30:02 Records to delete: 4200
#
# DRY RUN: Would delete 4200 records
# To actually delete records, run with -dry-run=false
```

## Record Patterns

The tool identifies cluster records by extracting the 5-character infraID from record names:

| Record Pattern | InfraID |
|----------------|---------|
| `api-autoscaling-7589p` | `7589p` |
| `oauth-node-pool-xht64` | `xht64` |
| `a-api-create-cluster-abc12-external-dns` | `abc12` |

Records are only deleted if no resource group exists with that infraID.

## Disclaimer

This tool has only been tested on the `aks-e2e.hypershift.azure.devcluster.openshift.com` DNS zone. Use with caution on other zones and always run with dry-run first.
