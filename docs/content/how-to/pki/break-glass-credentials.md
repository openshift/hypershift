# Break-Glass Credentials

This document describes break-glass credential management in HyperShift, including automatic rotation and revocation using the CertificateRevocationRequest API.

## Overview

The `control-plane-pki-operator` runs in each hosted control plane namespace and manages the lifecycle of **break-glass credentials**. These credentials provide emergency access to the hosted cluster's API server for both customers and Site Reliability Engineers (SREs).

Break-glass credentials are separate from [internal control plane certificates](control-plane-certificates.md), which are managed by the control-plane-operator (CPO).

The control-plane-pki-operator handles:

- **Automatic certificate rotation** - Continuous rotation of break-glass signing CAs and client certificates
- **Certificate signing request (CSR) approval** - Processing requests for new break-glass credentials
- **Certificate revocation** - Revoking compromised break-glass certificates through a safe, multi-step workflow

## Architecture

### Control Plane PKI Operator Components

The control-plane-pki-operator consists of several controllers:

| Controller | Purpose |
|------------|---------|
| Certificate Rotation Controller | Automatically rotates signing CAs and client certificates |
| Certificate Loading Controller | Loads signing CA certificates for use by the signing controllers |
| CSR Approval Controller | Approves CertificateSigningRequests when a matching CertificateSigningRequestApproval exists |
| Certificate Signing Controller | Signs approved CSRs to generate client certificates |
| Certificate Revocation Controller | Processes CertificateRevocationRequests to revoke certificates |

### Signer Classes

HyperShift defines two signer classes for break-glass credentials:

| Signer Class | Purpose |
|--------------|---------|
| `customer-break-glass` | Emergency access credentials for customers |
| `sre-break-glass` | Emergency access credentials for SREs |

Each signer class has its own:

- Signing CA secret
- CA trust bundle ConfigMap
- Client certificate secret

### Certificate Resources

For each signer class, the following resources are created in the hosted control plane namespace:

**Customer Break-Glass:**

| Resource | Type | Description |
|----------|------|-------------|
| `customer-system-admin-signer` | Secret | Root signing CA for customer credentials |
| `customer-system-admin-signer-ca` | ConfigMap | Trust bundle containing the CA certificate |
| `customer-system-admin-client-cert-key` | Secret | Client certificate for customer break-glass access |

**SRE Break-Glass:**

| Resource | Type | Description |
|----------|------|-------------|
| `sre-system-admin-signer` | Secret | Root signing CA for SRE credentials |
| `sre-system-admin-signer-ca` | ConfigMap | Trust bundle containing the CA certificate |
| `sre-system-admin-client-cert-key` | Secret | Client certificate for SRE break-glass access |

**Combined Trust Bundle:**

The individual CA bundles from both signer classes are combined into a `total-client-ca` ConfigMap. This combined trust bundle is used by the hosted cluster's kube-apiserver for client certificate authentication.

## Automatic Certificate Rotation

The control-plane-pki-operator automatically rotates break-glass signing CAs and client certificates based on configurable timing parameters.

### Default Rotation Schedule

The rotation timing for break-glass certificates is based on multiples of the rotation scale (default: 24 hours):

| Certificate Type | Validity Period | Refresh Interval |
|------------------|-----------------|------------------|
| Signing CA | 7 × scale (7 days) | 2 × scale (every 2 days) |
| Client Certificate | 1.5 × scale (36 hours) | 0.25 × scale (every 6 hours) |

### How It Works

1. The Certificate Rotation Controller monitors the validity of signing CAs and client certificates
2. When a certificate approaches its refresh interval, the controller generates a new certificate
3. New certificates are signed by the current CA and placed in the appropriate secrets
4. The CA trust bundle is updated to include both old and new CAs during the transition period
5. Old certificates are eventually removed from the trust bundle after all clients have transitioned

### Configuration

The rotation timing can be scaled using the `CERT_ROTATION_SCALE` environment variable on the control-plane-pki-operator. The default scale is 24 hours.

!!! warning

    Modifying `CERT_ROTATION_SCALE` is unsupported and should only be used for testing purposes.

## Requesting Break-Glass Credentials

Break-glass credentials provide emergency access to a hosted cluster when normal authentication methods are unavailable.

To request new break-glass credentials, create a `CertificateSigningRequest` with the appropriate signer name:

```yaml
apiVersion: certificates.k8s.io/v1
kind: CertificateSigningRequest
metadata:
  name: my-break-glass-request
spec:
  request: <base64-encoded-csr>
  signerName: hypershift.openshift.io/<hcp-namespace>.customer-break-glass
  usages:
  - client auth
```

The signer name format is: `hypershift.openshift.io/<hosted-control-plane-namespace>.<signer-class>`

### Approving Requests

CSRs are not automatically approved. To approve a CSR, create a matching `CertificateSigningRequestApproval` resource:

```yaml
apiVersion: certificates.hypershift.openshift.io/v1alpha1
kind: CertificateSigningRequestApproval
metadata:
  name: my-break-glass-request
  namespace: <hosted-control-plane-namespace>
spec: {}
```

!!! note

    The CertificateSigningRequestApproval must have the same name as the CertificateSigningRequest.

Once the approval resource exists, the CSR Approval Controller will approve the CSR, and the Certificate Signing Controller will sign it.

### Viewing CSR Status

Check the status of a CertificateSigningRequest:

```bash
oc get csr my-break-glass-request -o yaml
```

The signed certificate will be available in the `.status.certificate` field once approved and signed.

## Certificate Revocation

When certificates are compromised or need to be invalidated, use the `CertificateRevocationRequest` API to trigger a safe revocation workflow.

### Creating a Revocation Request

To revoke all certificates for a signer class:

```yaml
apiVersion: certificates.hypershift.openshift.io/v1alpha1
kind: CertificateRevocationRequest
metadata:
  name: revoke-customer-certs
  namespace: <hosted-control-plane-namespace>
spec:
  signerClass: customer-break-glass
```

Apply the revocation request:

```bash
oc apply -f revocation-request.yaml
```

### Revocation Workflow

The Certificate Revocation Controller processes revocation requests through the following steps:

1. **Commit Revocation Timestamp** - Records the point in time after which certificates must be regenerated
2. **Generate New Signer Certificate** - Creates a new signing CA valid only after the revocation timestamp
3. **Ensure New Signer Propagated** - Verifies the new CA is trusted by the hosted cluster's API server
4. **Regenerate Leaf Certificates** - Re-signs all client certificates with the new CA
5. **Prune Previous Signer** - Removes the old CA from the trust bundle
6. **Ensure Old Signer Revoked** - Verifies that certificates signed by the old CA are rejected

### Monitoring Revocation Progress

Check the status of a CertificateRevocationRequest:

```bash
oc get certificaterevocationrequest -n <hosted-control-plane-namespace> revoke-customer-certs -o yaml
```

The status includes conditions that indicate progress through each step:

| Condition Type | Description |
|----------------|-------------|
| `SignerClassValid` | The specified signer class is recognized |
| `RootCertificatesRegenerated` | New signing CA has been generated |
| `NewCertificatesTrusted` | New CA is trusted by the API server |
| `LeafCertificatesRegenerated` | All client certificates have been re-signed |
| `PreviousCertificatesRevoked` | Old certificates are no longer accepted |

Example output:

```yaml
status:
  revocationTimestamp: "2024-01-15T10:30:00Z"
  previousSigner:
    name: abc123def456
  conditions:
  - type: SignerClassValid
    status: "True"
    reason: AsExpected
    message: Signer class "customer-break-glass" known.
  - type: RootCertificatesRegenerated
    status: "True"
    reason: AsExpected
    message: Signer certificate clusters-example/customer-system-admin-signer regenerated.
  - type: NewCertificatesTrusted
    status: "True"
    reason: AsExpected
    message: New signer certificate clusters-example/customer-system-admin-signer trusted.
  - type: LeafCertificatesRegenerated
    status: "True"
    reason: AsExpected
    message: All leaf certificates are re-generated.
  - type: PreviousCertificatesRevoked
    status: "True"
    reason: AsExpected
    message: Previous signer certificate revoked.
```

### Revocation Events

The controller emits Kubernetes events during revocation. View them with:

```bash
oc get events -n <hosted-control-plane-namespace> --field-selector reason=CertificateRevocationProgressing
```

## Troubleshooting

### Checking Certificate Status

View the signing CA secret:

```bash
oc get secret customer-system-admin-signer -n <hosted-control-plane-namespace> -o yaml
```

View the CA trust bundle:

```bash
oc get configmap customer-system-admin-signer-ca -n <hosted-control-plane-namespace> -o yaml
```

### Viewing Operator Logs

Check the control-plane-pki-operator logs for certificate rotation activity:

```bash
oc logs -n <hosted-control-plane-namespace> deployment/control-plane-pki-operator
```

### Common Issues

**CSR not being approved:**

- Verify a CertificateSigningRequestApproval with the same name exists in the hosted control plane namespace
- Check that the signer name in the CSR matches the expected format

**Revocation stuck:**

- Check the conditions on the CertificateRevocationRequest for the current step
- Review operator logs for errors
- Ensure the hosted cluster's API server is accessible

**Certificate validation errors:**

- Verify the CA trust bundle ConfigMap contains the expected certificates
- Check that the client certificate was signed by a CA in the trust bundle
