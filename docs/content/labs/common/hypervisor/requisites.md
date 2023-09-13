---
title: Hypervisor Prerequisites
---

- **CPU**: The number of CPUs provided determines how many HostedClusters can run concurrently.
  - **Recommended**: 16 CPUs per Node for 3 nodes.
  - **Minimal Dev**: In a development environment, you may manage with 12 CPUs per Node for 3 nodes.

- **Memory**: The amount of RAM impacts how many HostedClusters can be hosted.
  - **Recommended**: 48 GB of RAM per Node.
  - **Minimal Dev**: For minimal development, 18 GB of RAM per Node may suffice.

- **Storage**: Using SSD storage for MCE is crucial.
  - **Management Cluster**: 250 GB.
  - **Registry**: Depends on the number of releases, operators, and images hosted. An acceptable number could be 500 GB, preferably separated from the disk where the HostedCluster is hosted.
  - **Webserver**: The required storage depends on the number of ISOs and images hosted. An acceptable number could be 500 GB.

- **Production**: For a production environment, it's advisable to keep these three components separated on different disks. A recommended configuration for production is as follows:
  - **Registry**: 2 TB.
  - **Management Cluster**: 500 GB.
  - **WebServer**: 2 TB.
