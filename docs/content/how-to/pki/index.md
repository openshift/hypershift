# PKI and Certificate Management

This section covers Public Key Infrastructure (PKI) and certificate management in HyperShift hosted control planes.

HyperShift manages two distinct categories of certificates:

- **Internal Control Plane Certificates** - Managed by the control-plane-operator (CPO), these certificates secure communication between control plane components
- **Break-Glass Credentials** - Managed by the control-plane-pki-operator, these certificates provide emergency access to the hosted cluster's API server

## Topics

- [Internal Control Plane Certificates](control-plane-certificates.md) - How the CPO manages certificates for control plane components
- [Break-Glass Credentials](break-glass-credentials.md) - Managing emergency access credentials, including rotation and revocation
