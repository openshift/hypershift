# HostedCluster, NodePool Version Compatibility Check

## Overview
HyperShift supports version compatibility checks between HostedCluster and NodePool. This feature ensures that NodePool versions are compatible with the HostedCluster version.

## Version Compatibility Rules

### Supporting Version Rules
1. NodePool version cannot be higher than the HostedCluster version.
2. For 4.y versions:
    - For 4.even versions (e.g. 4.18), allows up to 2 minor version differences (4.17, 4.16);
    - For 4.odd versions (e.g. 4.17), allows up to 1 minor version difference (4.16).

### Examples
- If HostedCluster version is 4.18.5:
    - Allowed NodePool versions: 4.18.5, 4.18.4, 4.17.z, 4.16.z
    - Disallowed NodePool versions: 4.15.z and below, 4.19.z and above

- If HostedCluster version is 4.17.5:
    - Allowed NodePool versions: 4.17.5, 4.17.4, 4.16.z
    - Disallowed NodePool versions: 4.15.z and below, 4.18.z and above

## Status Checks

### NodePool Status
The NodePool controller sets the `SupportedVersionSkew` condition to indicate version compatibility status:
- Status is `True` when the NodePool version is compatible with the HostedCluster version
- Status is `False` when versions are incompatible, with detailed error messages

## Error Handling
When version incompatibility is detected, the system will:
1. Set appropriate error conditions in both NodePool status
2. Provide detailed error messages explaining the specific version incompatibility reason

## End user Attention
1. Ensure version compatibility when creating new NodePools
2. Check version compatibility of all NodePools when upgrading HostedCluster
3. If version incompatibility is detected, upgrade or downgrade NodePool to a compatible version first 