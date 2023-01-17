# SLOs
This project is committed to satisfy a number of internal SLOs. 
These SLOs can be taken as reference by consumers to aggregate them and help define their own SLOs.

These SLOs/SLIs are currently just referential and monitored as part of our CI runs.
They are valid under the following circumstances:

- Running raw Hypershift operator in an OCP management cluster with bare HostedCluster and NodePool resources.
- Management cluster has 2 m6i.4xlarge instances with 16 cores to allocate HostedClusters.
- No more than 20 concurrent HostedClusters.

## SLOs/SLIs
- Time for cluster to become available should be < 5min.
- Time for cluster to complete rollout should be < 15min.
- Cluster Memory consumption per single replica HostedCluster should be < 7GB.
- Cluster Memory consumption per high available HostedCluster should be < 18GB.
- Time to NodePool availability should be < 8min.
- Time to cloud resources deletion in a raw cluster should be < 3min.
- Time to cluster deletion in a raw cluster should be < 5min.

TBD:

- Time to instance creation
- Time to node joining cluster
- Time for node to become active after joining

Dashboards with these metrics are currently stored in a temporary Grafana instance:

- [Deletion SLIs](https://hypershift-monitoring.homelab.sjennings.me:3000/d/xI8D5654z/deletion-slis?orgId=1)
- [Creation/Running SLIs](https://hypershift-monitoring.homelab.sjennings.me:3000/d/BGbA-pD7k/hypershift-ci?orgId=1)