# ARO-HCP Development Environment

This directory contains Taskfiles for setting up an AKS management cluster with ARO-HCP (Azure Red Hat OpenShift Hosted Control Plane).

## Prerequisites

- [Task](https://taskfile.dev/) - Install with `brew install go-task/tap/go-task`
- [Azure CLI](https://docs.microsoft.com/en-us/cli/azure/install-azure-cli)
- [ccoctl](https://github.com/openshift/cloud-credential-operator) - Cloud Credential Operator CLI
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [jq](https://stedolan.github.io/jq/)
- [gum](https://github.com/charmbracelet/gum) - For styled terminal output
- [hypershift CLI](https://hypershift-docs.netlify.app/) - Either in PATH or set `HYPERSHIFT_BINARY_PATH`
- An Azure subscription with appropriate permissions
- A pull secret from [console.redhat.com](https://console.redhat.com/openshift/install/pull-secret)

## Quick Start

1. **Create Azure credentials file:**
   ```bash
   cp azure-credentials.json.example azure-credentials.json
   # Edit azure-credentials.json with your SP credentials
   ```

2. **Configure environment:**
   ```bash
   cp config.example.env .envrc
   # Edit .envrc with your values (PREFIX, OIDC_ISSUER_NAME, RELEASE_IMAGE)
   direnv allow  # or source .envrc
   ```

3. **Login to Azure:**
   ```bash
   task prereq:login
   ```

4. **Create management cluster (first time):**
   ```bash
   task mgmt:create
   ```

5. **Create hosted cluster:**
   ```bash
   task cluster:create
   ```

6. **Destroy hosted cluster:**
   ```bash
   task cluster:destroy
   ```

7. **Destroy management cluster:**
   ```bash
   task mgmt:destroy
   ```

## Usage Pattern

The typical workflow is:

1. **Once every few months:** `task mgmt:create` - Creates a long-lived AKS management cluster
2. **Every few days:** `task cluster:create` / `task cluster:destroy` - Iterate on hosted clusters
3. **Rarely:** `task mgmt:destroy` - When done with the environment

## Primary Tasks

| Task | Description |
|------|-------------|
| `task mgmt:create` | Create management cluster (AKS) with all dependencies |
| `task mgmt:destroy` | Destroy management cluster |
| `task cluster:create` | Create hosted cluster (most frequent operation) |
| `task cluster:destroy` | Destroy hosted cluster |

## Utility Tasks

| Task | Description |
|------|-------------|
| `task prereq:login` | Login to Azure using azure-credentials.json |
| `task prereq:whoami` | Show current Azure identity vs credentials file |
| `task prereq:validate` | Validate all prerequisites including Azure identity |
| `task prereq:show-config` | Display current configuration |
| `task first-time` | One-time setup only (Key Vault, OIDC, identities) |
| `task teardown-all` | Complete teardown including one-time resources |
| `task status` | Show status of all components |

## Standard Workflow (Recommended)

For most users, these commands are all you need:

**First-time setup:**
```bash
task prereq:login     # Login to Azure
task mgmt:create      # Create everything (~20 min)
```

**Daily use:**
```bash
task cluster:create   # Create hosted cluster
task cluster:destroy  # Destroy hosted cluster
```

**Cleanup:**
```bash
task mgmt:destroy     # Destroy management cluster
task teardown-all     # Complete teardown including persistent resources
```

## Step-by-Step Workflow (For Debugging)

Use this when you need granular control for debugging or testing individual steps.

**Legend:**
| Symbol | Meaning |
|--------|---------|
| `○` | Aggregator - only orchestrates subtasks, can skip if you run all children manually |
| `●` | Does work - has actual commands/logic, must run this task |
| `⚠` | Has internal subtasks - CANNOT skip, must use this parent task |

```
● prereq:login              # Login using azure-credentials.json
● prereq:whoami             # Verify identity matches
● prereq:validate           # Validate all prerequisites

○ mgmt:create               # Aggregator - orchestrates all setup
├── ● prereq:validate
├── ○ keyvault:setup        # Aggregator
│   ├── ● keyvault:create
│   ├── ● keyvault:create-sps         ⚠ has internal create-sp
│   ├── ● keyvault:generate-sp-jsons
│   ├── ● keyvault:store-creds
│   └── ● keyvault:generate-cp-json
├── ● oidc:create                     ⚠ has internal create-issuer
│   └── ● oidc:create-keypair
├── ○ dataplane:create      # Aggregator
│   ├── ● dataplane:create-identities       ⚠ has internal
│   ├── ● dataplane:create-federated-creds  ⚠ has internal
│   └── ● dataplane:generate-dp-json
├── ● aks:create-identities
├── ○ aks:create            # Aggregator
│   ├── ● aks:create-rg
│   ├── ● aks:create-cluster
│   ├── ● aks:get-kubeconfig
│   └── ● aks:assign-kv-role
├── ○ dns:setup             # Aggregator
│   ├── ● dns:create-zone
│   ├── ● dns:delegate-zone
│   ├── ● dns:create-sp
│   └── ● dns:create-secret
└── ● operator:install
    └── ● operator:apply-crds

● operator:wait             # Wait for operator (standalone)
● operator:verify           # Verify operator status (standalone)
● operator:logs             # Show operator logs (standalone)

● status                    # Show status of all components

○ cluster:create            # Aggregator
├── ● cluster:create-rgs
├── ● cluster:create-network          ⚠ has internal create-nsg, create-vnet
└── ● cluster:create-hc

● cluster:wait              # Wait for cluster ready
● cluster:get-kubeconfig    # Get kubeconfig
● cluster:show              # Show cluster status

○ cluster:destroy           # Aggregator
├── ● cluster:destroy-hc
└── ● cluster:delete-rgs

○ mgmt:destroy              # Aggregator
├── ● operator:uninstall
├── ● dns:delete
└── ● aks:delete

○ teardown-all              # Aggregator
├── ○ cluster:destroy
├── ○ mgmt:destroy
├── ● dataplane:delete
├── ● oidc:delete
├── ● keyvault:delete
└── ● aks:delete-identities
```

**Important:**
- Tasks marked with `⚠` have internal subtasks that you CANNOT run directly
- Example: `oidc:create` calls both `create-keypair` (public) AND `create-issuer` (internal)
- Running only `oidc:create-keypair` will NOT create the OIDC issuer - you must run `oidc:create`

## Task Namespaces

### prereq: - Prerequisites and Azure Authentication
- `task prereq:login` - Login to Azure using azure-credentials.json
- `task prereq:whoami` - Show current Azure identity and verify it matches credentials file
- `task prereq:validate` - Validate tools, environment variables, and Azure identity
- `task prereq:show-config` - Display current configuration

### keyvault: - Key Vault and Control Plane SPs
- `task keyvault:setup` - Complete Key Vault setup (idempotent)
- `task keyvault:rotate-creds` - Rotate all SP credentials
- `task keyvault:delete` - Delete Key Vault and SPs

### oidc: - OIDC Provider
- `task oidc:create` - Create OIDC provider (idempotent)
- `task oidc:delete` - Delete OIDC issuer

### dataplane: - Data Plane Managed Identities
- `task dataplane:create` - Complete data plane setup (idempotent)
- `task dataplane:delete` - Delete data plane identities

### aks: - AKS Management Cluster
- `task aks:create` - Complete AKS setup
- `task aks:get-kubeconfig` - Get/restore AKS kubeconfig (re-run if file is lost)
- `task aks:delete` - Delete AKS cluster
- `task aks:show` - Show AKS status

### dns: - External DNS
- `task dns:setup` - Complete DNS setup (idempotent)
- `task dns:delete` - Delete DNS resources

### operator: - HyperShift Operator
- `task operator:install` - Install HyperShift operator (ARO-HCP mode)
- `task operator:verify` - Verify operator installation
- `task operator:uninstall` - Uninstall operator

### cluster: - Hosted Cluster
- `task cluster:create-hc` - Create hosted cluster
- `task cluster:destroy-hc` - Destroy hosted cluster
- `task cluster:get-kubeconfig` - Get hosted cluster kubeconfig
- `task cluster:show` - Show hosted cluster status
- `task cluster:wait` - Wait for cluster to be ready

## Required Configuration

| File/Variable | Description |
|---------------|-------------|
| `AZURE_CREDS` | Path to azure-credentials.json (contains subscriptionId, tenantId, clientId, clientSecret) |
| `PULL_SECRET` | Path to pull secret file |
| `PREFIX` | Unique prefix for all resources |
| `OIDC_ISSUER_NAME` | Unique name for OIDC storage account |
| `RELEASE_IMAGE` | OpenShift release image |

## Optional Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `LOCATION` | `eastus` | Azure region for resources |
| `PERSISTENT_RG_NAME` | `os4-common` | Shared resource group |
| `PARENT_DNS_ZONE` | `hypershift.azure.devcluster.openshift.com` | Parent DNS zone |
| `AKS_NODE_COUNT` | `3` | Number of AKS nodes |
| `AKS_NODE_VM_SIZE` | `Standard_D4s_v4` | VM size for AKS nodes |
| `NODE_POOL_REPLICAS` | `2` | Number of worker nodes |
| `HYPERSHIFT_IMAGE` | (none) | Override HyperShift operator image |
| `HYPERSHIFT_BINARY_PATH` | (none) | Path to hypershift binary |
| `KUBECONFIG` | `./mgmt-kubeconfig` | Path where mgmt cluster kubeconfig will be saved |

## Generated Files

The following files are generated during setup:

| File | Description |
|------|-------------|
| `mgmt-kubeconfig` | Management (AKS) cluster kubeconfig - created by `task aks:get-kubeconfig` |
| `cp-output.json` | Control plane managed identities |
| `dp-output.json` | Data plane managed identities |
| `serviceaccount-signer.public` | SA token issuer public key |
| `serviceaccount-signer.private` | SA token issuer private key |
| `external-dns-creds.json` | External DNS credentials |
| `kubeconfig-<cluster-name>` | Hosted cluster kubeconfig |

**Note:** The `KUBECONFIG` environment variable is set in `.envrc` to point to `mgmt-kubeconfig`. With direnv, all `kubectl`, `hypershift`, and `oc` commands automatically use this file. If the file is lost, run `task aks:get-kubeconfig` to restore it.

## Architecture

This setup uses the MIv3 (Managed Identity v3) pattern:

1. **Control Plane Components** use Service Principals with certificates stored in Azure Key Vault
2. **Data Plane Components** use Managed Identities with federated credentials
3. **AKS** uses the Key Vault Secrets Provider addon to mount certificates

## Migrating from Shell Scripts

If you were using the shell scripts in `contrib/managed-azure/`:

1. Install Task: `brew install go-task/tap/go-task`
2. Copy your `user-vars.sh` values to `.envrc`
3. Run `task mgmt:create` (equivalent to `setup_all.sh --first-time`)
4. Run `task cluster:create` (equivalent to `create_basic_hosted_cluster.sh`)

## Troubleshooting

### Azure identity mismatch
If you see "Identity mismatch" or "Forbidden" errors, your Azure CLI is logged in as a different service principal than the one in your credentials file:
```bash
# Check current identity
task prereq:whoami

# Login with correct credentials
task prereq:login

# Verify
task prereq:validate
```

### Clean up after failed setup
If a task fails partway through (e.g., Key Vault created but SPs failed):
```bash
# Clean up Key Vault resources
task keyvault:delete

# Fix the issue (e.g., login correctly)
task prereq:login

# Retry
task keyvault:setup
```

### Check operator logs
```bash
task operator:logs
```

### Check hosted cluster status
```bash
task cluster:show
```

### Verify all components
```bash
task status
```

### Re-run with verbose output
```bash
task -v mgmt:create
```
