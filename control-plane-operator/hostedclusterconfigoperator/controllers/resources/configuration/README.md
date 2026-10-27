# Cluster foundation configuration

`Reconcile` owns CRD, ClusterVersion, ClusterOperator, and namespace policy.
Its inputs are read-only policy facts plus the existing guest client and upsert function.
Shared manifest constructors, embedded CRDs, and namespace label helpers stay in their existing packages.

The root invokes four stages at their original positions: CRDs before endpoints,
admission policies, install config and alerts; ClusterVersion then ClusterOperators;
global configuration; namespaces; RBAC. A single combined call would reorder
nonadjacent phases. The staged API preserves root logging, contextual error wrappers,
shared input loading, and orchestration. A failed stage does not prevent later attempts.
