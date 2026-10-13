---
title: Dual Stack
---

This network configuration is currently designated as disconnected. The primary reason for this designation is because remote registries do not function with IPv6. Consequently, this aspect has been incorporated into the documentation.

All the scripts provided contain partial or complete automation to replicate the environment. For this purpose, you can refer to the repository containing all the scripts for [Dual Stack environments](https://github.com/jparrill/hypershift-disconnected/tree/main/assets/dual).

Please note that this documentation is designed to be followed in a specific sequence:

- [Hypervisor](hypervisor/)
- [DNS](dns.md)
- [Registry](registry.md)
- [Management Cluster](mgmt-cluster/)
- [Webserver](webserver.md)
- [Mirroring](mirror/)
- [Multicluster Engine](mce/)
- [TLS Certificates](tls-certificates.md)
- [HostedCluster](hostedcluster/)
- [Watching Deployment progress](watching/)