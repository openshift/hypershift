# Azure AKS E2E Credential Rotation

This directory contains a Taskfile for rotating Azure managed identity credentials used in AKS E2E testing.

## Prerequisites

- [Azure CLI](https://docs.microsoft.com/en-us/cli/azure/install-azure-cli) (`az`) - authenticated with appropriate permissions
- [Task](https://taskfile.dev/installation/) - task runner
- `jq` - JSON processor
- `openssl` - for certificate conversion

## Azure Authentication

If you have an Azure credentials file (`credentials.json`):

```json
{
  "subscriptionId": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
  "tenantId": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
  "clientId": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
  "clientSecret": "<SECRET>"
}
```

Sign in using:

```bash
az login --service-principal \
  -u $(jq -r .clientId credentials.json) \
  -p $(jq -r .clientSecret credentials.json) \
  --tenant $(jq -r .tenantId credentials.json)
```

## Configuration

Create a `managed-identities.json` file with your managed identity configuration:

```json
{
  "managedIdentitiesKeyVault": {
    "name": "your-keyvault-name"
  },
  "firstComponentName": {
    "clientID": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    "credentialsSecretName": "secret-name-in-vault",
    "certificateName": "certificate-display-name"
  },
  "secondComponentName": {
    "clientID": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    "credentialsSecretName": "secret-name-in-vault",
    "certificateName": "certificate-display-name"
  },
  ...
}
```

Each component (other than `managedIdentitiesKeyVault`) represents a managed identity whose credentials will be rotated.

### Field Relationships

For each component, the fields map to Azure resources as follows:

| Field | Description | Azure CLI Command |
|-------|-------------|-------------------|
| `clientID` | App Registration ID | `az ad app credential list --id <clientID> --cert` |
| `credentialsSecretName` | Key Vault secret name | `az keyvault secret show --vault-name <vault> --name <credentialsSecretName>` |
| `certificateName` | Display name for the certificate | Shown in `displayName` field of credential list |

**Example:** For a component with:
```json
{
  "clientID": "cabb490b-b6cc-4d6c-ae25-4205f0b1c6af",
  "credentialsSecretName": "ciro-miv3-aks-e2e",
  "certificateName": "ciro-aks-e2e"
}
```

View the app's registered certificates:
```bash
az ad app credential list --id cabb490b-b6cc-4d6c-ae25-4205f0b1c6af --cert
```

Output shows the certificate with `displayName: "ciro-aks-e2e"`:
```json
[
  {
    "displayName": "ciro-aks-e2e",
    "endDateTime": "2026-11-28T17:47:08Z",
    "startDateTime": "2025-11-28T17:47:08Z",
    "type": "AsymmetricX509Cert"
  }
]
```

View the corresponding vault secret:
```bash
az keyvault secret show --vault-name aks-e2e --name ciro-miv3-aks-e2e --query "value" -o table
```

The vault secret contains the JSON credentials (with `client_secret` being the base64-encoded PKCS12 certificate) that applications use to authenticate as this managed identity.

## Usage

### Workflow

The typical credential rotation workflow is:

1. **Generate credentials locally** (does NOT modify vault):
   ```bash
   task rotate-all
   ```
   This creates new certificates for all components and saves them to `./creds-tmp/`.

2. **Store credentials to vault**:
   ```bash
   task store-all
   ```
   Uploads the generated credentials to Azure Key Vault.

3. **Verify credentials**:
   ```bash
   task verify-all
   ```
   Confirms that vault contents match local files.

4. **Cleanup local files**:
   ```bash
   task cleanup-creds
   ```
   Removes the temporary credential files from `./creds-tmp/`.

### Available Tasks

| Task | Description |
|------|-------------|
| `rotate-all` | Generate all credentials locally (does NOT store to vault) |
| `store-all` | Store all local credentials to vault |
| `verify-all` | Verify all vault credentials match local files |
| `cleanup-creds` | Remove all local credential files |

Run `task --list` to see all available tasks.

## Output Format

Generated credentials are stored in JSON format compatible with Azure SDK:

```json
{
  "authentication_endpoint": "https://login.microsoftonline.com/",
  "client_id": "...",
  "client_secret": "<base64-encoded-pkcs12>",
  "tenant_id": "...",
  "not_before": "2025-01-01T00:00:00Z",
  "not_after": "2026-01-01T00:00:00Z"
}
```

## Troubleshooting

### PKCS12 Compatibility Error

If the operator fails with:
```
pkcs12: unknown digest algorithm: 2.16.840.1.101.3.4.2.1
```

This means the PKCS12 certificate was created with modern algorithms (SHA-256) that Go's `crypto/pkcs12` library doesn't support. The Taskfile uses `-legacy` flag to generate compatible certificates, but if that doesn't work on your system, edit the `convert-pem-to-json` task to use explicit legacy algorithms:

```bash
# Replace this:
openssl pkcs12 -export -legacy -in "$PEM_FILE" ...

# With this:
openssl pkcs12 -export -certpbe PBE-SHA1-3DES -keypbe PBE-SHA1-3DES -macalg sha1 -in "$PEM_FILE" ...
```

## Security Notes

- Credentials are temporarily stored in `./creds-tmp/` during rotation
- The `creds-tmp/` directory is in `.gitignore` to prevent accidental commits
- Always run `task cleanup-creds` after successful rotation
- Ensure you have appropriate Azure RBAC permissions before rotating credentials
