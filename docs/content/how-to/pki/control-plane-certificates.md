# Internal Control Plane Certificates

This document describes how internal control plane certificates are managed in HyperShift hosted control planes.

## Overview

The control-plane-operator (CPO) manages all internal PKI for the hosted control plane. These certificates secure communication between control plane components and are separate from [break-glass credentials](break-glass-credentials.md), which are managed by the control-plane-pki-operator.

## Certificates Managed

The CPO manages certificates for:

- Kube API server serving certificates (internal and external)
- Client certificates for kubelet, scheduler, controller-manager
- etcd peer and client certificates
- Aggregator proxy certificates
- Service account signing keys

## CA Certificate Lifecycle

Root CA certificates managed by the CPO have a **10-year validity period** and are generated once. They are not automatically rotated unless the certificate or key data is missing from the secret.

## Leaf Certificate Rotation

Leaf certificates (server and client certificates signed by a CA) are automatically rotated by the CPO:

| Setting | Default Value | Description |
|---------|---------------|-------------|
| Validity Period | 1 year | How long the certificate is valid |
| Renewal Threshold | 30 days | Certificate is renewed when less than this time remains |

Rotation is **reconciliation-driven**: during each reconciliation loop, the CPO validates existing certificates and regenerates them if they are approaching expiration or if their configuration has changed.

## Test Configuration Options

Certificate validity and renewal can be customized using environment variables on the CPO:

| Environment Variable | Description |
|---------------------|-------------|
| `CERTIFICATE_VALIDITY` | Custom certificate validity duration (e.g., `8760h` for 1 year) |
| `CERTIFICATE_RENEWAL_PERCENTAGE` | Fraction of validity period at which to renew (e.g., `0.30` renews when 30% of validity remains) |

!!! warning

    Modifying certificate validity settings is advanced configuration. Incorrect values may cause control plane instability or security issues.
