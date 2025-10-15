# Self-Managed Azure HyperShift Setup

This directory contains scripts to set up a self-managed Azure HyperShift hosted cluster.

## Prerequisites

1. **Azure CLI**: Install and configure the Azure CLI
2. **kubectl**: Kubernetes command-line tool
3. **jq**: JSON processor for parsing Azure CLI output
4. **HyperShift binary**: Built from this repository (`make build`)
5. **ccoctl binary**: Cloud Credential Operator CLI tool
6. **Azure subscription**: With appropriate permissions
7. **Kubernetes cluster**: For running the HyperShift operator (management cluster)

## Required Files

Before running the setup, ensure you have these files:

1. **Azure credentials file**: Usually at `~/.azure/credentials`

2. **Pull secret**: OpenShift pull secret (JSON format)

Note: The service account signing keys and workload identities file will be generated automatically by the setup scripts.

## Setup Steps

1. **Configure variables**:
   ```bash
   cp user-vars.sh.example user-vars.sh
   # Edit user-vars.sh with your values
   ```

2. **Add ccoctl to PATH** (required for scripts):
   ```bash
   export PATH="${CCOCTL_BINARY_PATH}:$PATH"
   # Or if using the default path:
   export PATH="${HOME}/cloud-credential-operator:$PATH"
   ```

3. **First-time setup** (run once):
   ```bash
   chmod +x *.sh
   ./setup_all.sh --first-time
   ```

4. **Per-cluster setup** (run for each cluster):
   ```bash
   ./setup_all.sh
   ```

## What the Setup Does

### First-time setup (--first-time flag):

1. **OIDC Provider Setup** (uses `../managed-azure/setup_oidc_provider.sh`):
   - Creates service account key pair using ccoctl
   - Creates managed resource group
   - Sets up OIDC issuer for workload identity authentication

2. **Infrastructure Setup** (`setup_infra.sh`):
   - Creates Azure infrastructure using `hypershift create infra azure`
   - Creates and federates managed identities for all workload components
   - Generates workload identities configuration file

### Per-cluster setup:

3. **External DNS Setup** (uses `../managed-azure/setup_external_dns.sh`):
   - Creates DNS delegation records
   - Creates Azure service principal for External DNS
   - Sets up Kubernetes secret for External DNS

4. **HyperShift Operator Installation** (`setup_hypershift_operator.sh`):
   - Installs HyperShift operator with Azure External DNS provider
   - Configures operator for self-managed Azure

5. **Hosted Cluster Creation** (`create_hosted_cluster.sh`):
   - Creates the hosted cluster using self-managed Azure infrastructure
   - No pre-existing networking required (cluster creates its own networking)

## Monitoring Progress

Monitor the hosted cluster creation:
```bash
kubectl get hostedcluster <cluster-name> -n clusters
```

Get the cluster kubeconfig when ready:
```bash
./bin/hypershift create kubeconfig --name <cluster-name> --namespace clusters > cluster-kubeconfig
export KUBECONFIG=cluster-kubeconfig
kubectl get nodes
```

## Cleanup

To delete the hosted cluster:
```bash
./bin/hypershift destroy cluster azure --name <cluster-name> --namespace clusters
```

## Configuration Variables

Key variables in `user-vars.sh`:

- `PREFIX`: Unique prefix for all resources
- `DNS_RECORD_NAME`: Your subdomain name
- `RELEASE_IMAGE`: OpenShift release image
- `LOCATION`: Azure region (default: eastus)

## Troubleshooting

1. **Azure login issues**: Ensure you're logged into the correct Azure subscription
2. **DNS issues**: Verify DNS zone exists and delegation is set up correctly
3. **Permission issues**: Ensure your Azure account has sufficient permissions
4. **Binary paths**: Verify all required binaries are available and executable