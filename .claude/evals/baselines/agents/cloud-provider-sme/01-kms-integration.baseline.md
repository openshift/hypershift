

The exploration reveals that **HyperShift already has comprehensive customer-managed encryption key support for etcd data at rest across AWS, Azure, and IBM Cloud**. Let me provide the full architectural breakdown of the existing design and any gaps or improvements to consider.

---

# Customer-Managed Encryption Keys for etcd Data at Rest in HyperShift

## Current State: Already Implemented

HyperShift already supports customer-managed KMS encryption for etcd across **AWS KMS**, **Azure Key Vault**, and **IBM Cloud Key Protect**. Here's the complete design:

---

## 1. API Design

### Top-Level Entry Point (`HostedCluster.Spec.SecretEncryption`)

```go
// api/hypershift/v1beta1/hostedcluster_types.go

type SecretEncryptionSpec struct {
    // Type: AESCBC or KMS
    Type SecretEncryptionType `json:"type"`
    
    // AESCBC: customer provides raw AES keys in Secrets
    AESCBC *AESCBCSpec `json:"aescbc,omitempty"`
    
    // KMS: customer provides cloud KMS key references
    KMS *KMSSpec `json:"kms,omitempty"`
}

type KMSSpec struct {
    // Provider discriminator: IBMCloud, AWS, or Azure
    Provider KMSProvider `json:"provider"`
    
    IBMCloud *IBMCloudKMSSpec `json:"ibmcloud,omitempty"`
    AWS      *AWSKMSSpec      `json:"aws,omitempty"`
    Azure    *AzureKMSSpec    `json:"azure,omitempty"`
}
```

### AWS KMS API (`api/hypershift/v1beta1/aws.go`)

```go
type AWSKMSSpec struct {
    Region    string          `json:"region"`
    ActiveKey AWSKMSKeyEntry  `json:"activeKey"`
    BackupKey *AWSKMSKeyEntry `json:"backupKey,omitempty"`
    Auth      AWSKMSAuthSpec  `json:"auth"`
}

type AWSKMSKeyEntry struct {
    // ARN of the KMS key (pattern: ^arn:)
    ARN string `json:"arn"`
}

type AWSKMSAuthSpec struct {
    // IAM role ARN with kms:Encrypt, kms:Decrypt, kms:ReEncrypt*,
    // kms:GenerateDataKey*, kms:DescribeKey permissions
    AWSKMSRoleARN string `json:"awsKMSRoleARN"`
}
```

### Azure KMS API (`api/hypershift/v1beta1/azure.go`)

```go
type AzureKMSSpec struct {
    ActiveKey AzureKMSKey     `json:"activeKey"`
    BackupKey *AzureKMSKey    `json:"backupKey,omitempty"`
    KMS       ManagedIdentity `json:"kms"` // Azure managed identity for auth
}

type AzureKMSKey struct {
    KeyVaultName string `json:"keyVaultName"`
    KeyName      string `json:"keyName"`
    KeyVersion   string `json:"keyVersion"`
}
```

---

## 2. Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                     Management Cluster                              │
│                                                                     │
│  ┌──────────────────────┐      ┌──────────────────────────────┐    │
│  │  hypershift-operator │      │   HostedCluster CR           │    │
│  │                      │─────▶│   .spec.secretEncryption:    │    │
│  │  Validates KMS spec  │      │     type: KMS                │    │
│  │  Propagates to HCP   │      │     kms:                     │    │
│  └──────────────────────┘      │       provider: AWS          │    │
│            │                   │       aws:                    │    │
│            ▼                   │         activeKey:            │    │
│  ┌──────────────────────────┐  │           arn: arn:aws:kms:...│    │
│  │  HostedControlPlane CR   │  │         auth:                │    │
│  │  (in HCP namespace)      │  │           awsKMSRoleARN: ... │    │
│  └──────────────────────────┘  └──────────────────────────────┘    │
│            │                                                        │
│            ▼                                                        │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  control-plane-operator (CPO)                                │   │
│  │                                                              │   │
│  │  1. Reads HCP.Spec.SecretEncryption                          │   │
│  │  2. Calls getKMSProvider() → platform-specific provider      │   │
│  │  3. Generates EncryptionConfiguration YAML                   │   │
│  │  4. Injects KMS sidecar containers into KAS pod              │   │
│  └──────────────────────────────────────────────────────────────┘   │
│            │                                                        │
│            ▼                                                        │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │  kube-apiserver Pod                                          │   │
│  │  ┌────────────────┐  ┌─────────────────┐  ┌──────────────┐  │   │
│  │  │ kube-apiserver │  │ aws-kms-active  │  │aws-kms-backup│  │   │
│  │  │                │  │ (sidecar)       │  │(sidecar)     │  │   │
│  │  │ --encryption-  │  │                 │  │              │  │   │
│  │  │  provider-     │  │ Listens on:     │  │ Listens on:  │  │   │
│  │  │  config=...    │  │ unix:///var/run/ │  │ unix:///var/ │  │   │
│  │  │                │  │ awskmsactive    │  │ run/awskms   │  │   │
│  │  │ Calls KMS via  │  │ .sock           │  │ backup.sock  │  │   │
│  │  │ Unix socket ──▶│  │                 │  │              │  │   │
│  │  └────────────────┘  │  Calls AWS ─────┼──┼──────────┐   │  │   │
│  │                      │  KMS API        │  │          │   │  │   │
│  │                      └─────────────────┘  └──────────┼───┘  │   │
│  └──────────────────────────────────────────────────────┼──────┘   │
│                                                         │          │
└─────────────────────────────────────────────────────────┼──────────┘
                                                          │
                                              ┌───────────▼──────────┐
                                              │  Cloud KMS Service   │
                                              │  (AWS KMS / Azure    │
                                              │   Key Vault)         │
                                              │                      │
                                              │  Customer-managed    │
                                              │  CMK key             │
                                              └──────────────────────┘
```

---

## 3. Controller Logic Flow

### Step 1: HostedCluster Controller (hypershift-operator)

**File:** `hypershift-operator/controllers/hostedcluster/hostedcluster_controller.go`

- Validates `SecretEncryptionSpec` on the HostedCluster CR
- Copies the encryption spec onto the `HostedControlPlane` CR in the HCP namespace
- No cloud-side operations — all cloud interaction happens in the CPO

### Step 2: Control-Plane-Operator KAS Reconciliation

**File:** `control-plane-operator/controllers/hostedcontrolplane/v2/kas/secretencryption.go`

```
adaptSecretEncryptionConfig(cpContext, deployment, hcp)
  │
  ├── if type == AESCBC:
  │     → Fetch AES key secrets, build aescbc EncryptionConfig
  │
  └── if type == KMS:
        → getKMSProvider(hcp) → returns platform-specific KMSProvider
        → provider.GenerateKMSEncryptionConfig()  → EncryptionConfiguration YAML
        → provider.GenerateKMSPodConfig()          → sidecar containers + volumes
        → Mount EncryptionConfig into KAS container
        → Inject sidecars into KAS Deployment
```

### Step 3: Platform-Specific KMS Provider Interface

**File:** `control-plane-operator/controllers/hostedcontrolplane/v2/kas/kms/`

```go
// Implicit interface implemented by all KMS providers:
type KMSProvider interface {
    GenerateKMSEncryptionConfig(apiVersion string) ([]byte, error)
    GenerateKMSPodConfig() (containers, volumes, volumeMounts)
}
```

Each provider generates:

| Component | AWS | Azure |
|-----------|-----|-------|
| **Sidecar containers** | `aws-kms-active`, `aws-kms-backup`, `aws-kms-token-minter` | `azure-kms-provider-active`, `azure-kms-provider-backup` |
| **Socket path** | `/var/run/awskmsactive.sock` | `/opt/azurekmsactive.socket` |
| **Auth mechanism** | OIDC token minting → STS AssumeRoleWithWebIdentity | Azure Managed Identity + Secret Store CSI Driver |
| **Key rotation** | Active + optional backup ARN | Active + optional backup Key Vault key |
| **KMS API version** | v1 or v2 (preserves existing) | v1 or v2 |

---

## 4. Kubernetes EncryptionConfiguration Generated

All providers produce a standard Kubernetes `EncryptionConfiguration`:

```yaml
apiVersion: apiserver.config.k8s.io/v1
kind: EncryptionConfiguration
resources:
- resources:
  - secrets
  - configmaps
  # ... other sensitive resources from config.KMSEncryptedObjects()
  providers:
  - kms:
      apiVersion: v2
      name: <hash-of-active-key>
      endpoint: unix:///var/run/awskmsactive.sock
      timeout: 35s
  - kms:                          # Optional backup for key rotation
      apiVersion: v2
      name: <hash-of-backup-key>
      endpoint: unix:///var/run/awskmsbackup.sock
      timeout: 35s
  - identity: {}                  # Fallback: unencrypted reads
```

---

## 5. Key Rotation Mechanism

The active/backup key pattern enables zero-downtime key rotation:

1. **Initial state:** Only `activeKey` is set. All writes use this key.
2. **Rotation step 1:** Customer sets `backupKey` to the *old* key, changes `activeKey` to the *new* key.
3. **KAS restarts** with new EncryptionConfiguration listing both providers.
4. **New writes** use the new active key; **old reads** decrypt with the backup key.
5. **Re-encryption:** Admin runs `kubectl get secrets --all-namespaces -o json | kubectl replace -f -` to re-encrypt all secrets with the new key.
6. **Rotation step 2:** Customer removes `backupKey` once all data is re-encrypted.

---

## 6. Authentication Flow (per platform)

### AWS Authentication
```
┌──────────────┐     ┌───────────────────┐     ┌──────────┐
│ token-minter │────▶│ CPO token endpoint │────▶│ OIDC     │
│ (sidecar)    │     │ (mints OIDC token) │     │ Provider │
└──────┬───────┘     └───────────────────┘     └──────────┘
       │ writes token to shared volume
       ▼
┌──────────────┐     ┌──────────────────┐
│ aws-kms-     │────▶│ AWS STS           │
│ active       │     │ AssumeRoleWith    │
│ (sidecar)    │     │ WebIdentity       │
└──────────────┘     └────────┬─────────┘
                              ▼
                     ┌──────────────────┐
                     │ AWS KMS API      │
                     │ (Encrypt/Decrypt)│
                     └──────────────────┘
```

### Azure Authentication
```
┌──────────────────────┐     ┌──────────────────────┐
│ azure-kms-provider   │────▶│ Azure Key Vault       │
│ (sidecar)            │     │                       │
│                      │     │ Authenticates via:    │
│ --config-file-path   │     │ - Managed Identity    │
│ (Azure creds secret) │     │ - Secret Store CSI    │
└──────────────────────┘     └───────────────────────┘
```

---

## 7. Example HostedCluster Specs

### AWS with KMS

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: my-cluster
spec:
  secretEncryption:
    type: KMS
    kms:
      provider: AWS
      aws:
        region: us-east-1
        activeKey:
          arn: "arn:aws:kms:us-east-1:123456789012:key/mrk-abcdef1234567890"
        auth:
          awsKMSRoleARN: "arn:aws:iam::123456789012:role/my-kms-role"
```

### Azure with Key Vault

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: my-cluster
spec:
  secretEncryption:
    type: KMS
    kms:
      provider: Azure
      azure:
        activeKey:
          keyVaultName: "my-keyvault"
          keyName: "my-etcd-key"
          keyVersion: "1234567890abcdef"
        kms:
          # Azure managed identity for KMS access
          clientID: "..."
          tenantID: "..."
          certificateName: "..."
```

---

## 8. Gaps and Recommendations for Improvement

While the feature is already implemented, here are areas that could be enhanced:

### 8.1 Validation Improvements

**Current gap:** Limited API-level validation of KMS key references before the control plane starts.

**Recommendation:** Add a validating webhook or controller-side pre-flight check that:
- Verifies the KMS key ARN is accessible with the provided role (AWS)
- Verifies the Key Vault key exists and the managed identity has `wrapKey`/`unwrapKey` permissions (Azure)
- Sets a `KMSKeyAccessible` condition on the HostedCluster status

```go
// In hypershift-operator HostedCluster controller:
func (r *reconciler) validateKMSAccess(ctx context.Context, hc *v1beta1.HostedCluster) error {
    switch hc.Spec.SecretEncryption.KMS.Provider {
    case v1beta1.AWS:
        return r.validateAWSKMSAccess(ctx, hc)
    case v1beta1.Azure:
        return r.validateAzureKMSAccess(ctx, hc)
    }
}
```

### 8.2 Status Reporting

**Current gap:** No dedicated status conditions for encryption health.

**Recommendation:** Add conditions:
```go
const (
    SecretEncryptionConfigured   = "SecretEncryptionConfigured"
    SecretEncryptionHealthy      = "SecretEncryptionHealthy"
    KMSKeyRotationInProgress     = "KMSKeyRotationInProgress"
)
```

### 8.3 Automated Key Rotation Support

**Current gap:** Key rotation is manual — the customer must update `activeKey`/`backupKey` and trigger re-encryption.

**Recommendation:** Add an optional `autoRotation` field:
```go
type AWSKMSSpec struct {
    // ... existing fields ...
    
    // AutoRotation enables automatic key rotation via AWS KMS
    // automatic key rotation feature (rotates backing key annually)
    // +optional
    AutoRotation *KMSAutoRotation `json:"autoRotation,omitempty"`
}
```

### 8.4 GCP KMS Support

**Current gap:** GCP is listed as a supported platform but has no KMS provider implementation.

**Recommendation:** If GCP hosted control planes are planned:
```go
type KMSSpec struct {
    Provider KMSProvider `json:"provider"`
    // ... existing fields ...
    GCP *GCPKMSSpec `json:"gcp,omitempty"`
}

type GCPKMSSpec struct {
    KeyName   string         `json:"keyName"`   // projects/*/locations/*/keyRings/*/cryptoKeys/*
    ActiveKey GCPKMSKeyEntry `json:"activeKey"`
    BackupKey *GCPKMSKeyEntry `json:"backupKey,omitempty"`
    Auth      GCPKMSAuthSpec  `json:"auth"`
}
```

### 8.5 Metrics and Observability

**Recommendation:** Expose Prometheus metrics from KMS sidecars:
- `kms_encrypt_requests_total` / `kms_decrypt_requests_total`
- `kms_request_duration_seconds`
- `kms_errors_total` (with error type labels)
- Alert on high error rates or latency (KMS throttling, key deletion, etc.)

---

## 9. Security Considerations

| Concern | How It's Addressed |
|---------|-------------------|
| **Key material never on disk** | KMS envelope encryption — only DEKs in etcd, KEK stays in cloud KMS |
| **Least-privilege IAM** | AWS: Dedicated IAM role with only KMS permissions. Azure: Managed Identity scoped to Key Vault |
| **No IMDS dependency** | AWS: `AWS_EC2_METADATA_DISABLED=true`, uses OIDC federation instead |
| **Key compromise blast radius** | Each hosted cluster uses its own KMS key — no cross-cluster key sharing |
| **Network isolation** | KMS sidecar communicates via Unix socket (no network exposure) |
| **Key deletion protection** | Customer responsibility — recommend enabling AWS KMS deletion protection / Azure soft-delete |

---

## Summary

HyperShift's customer-managed etcd encryption is **fully implemented** across AWS and Azure with:

- **API types** in `api/hypershift/v1beta1/` defining per-platform KMS specs
- **Provider interface** pattern in `control-plane-operator/.../kas/kms/` with AWS, Azure, and IBM Cloud implementations
- **Sidecar injection** into the KAS pod for KMS communication over Unix sockets
- **Active/backup key** model for zero-downtime key rotation
- **Platform-native authentication** (OIDC for AWS, Managed Identity for Azure)

The main areas for improvement are **pre-flight KMS key validation**, **status condition reporting**, **observability/metrics**, and **GCP KMS support**.
