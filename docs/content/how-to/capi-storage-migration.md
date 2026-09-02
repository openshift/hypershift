# CAPI CRD Storage Version Migration

## Overview

HyperShift uses Cluster API (CAPI) Custom Resource Definitions (CRDs) to manage hosted cluster infrastructure. These CRDs are transitioning their storage version from `v1beta1` to `v1beta2`. The storage version determines how Kubernetes persists resources in etcd.

Migrating the storage version ensures all existing CAPI resources are re-stored using the new `v1beta2` schema. This is required before the `v1beta1` API version can be removed in a future CAPI release.

The migration happens automatically on `hypershift install` — no special flags are needed. To opt out, use the `--disable-capi-migration` flag.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--disable-capi-migration` | `false` | Disables automatic CAPI CRD storage version migration. When set, CRDs are installed without overriding their storage version and the migrator controller is not started. |

### How it works

By default, `hypershift install`:

1. Applies the CAPI CRDs with `v1beta2` as the storage version.
2. The HyperShift Operator starts a CRD migrator controller that performs a no-op server-side apply on every existing CAPI custom resource, forcing the API server to re-store each object in `v1beta2`.
3. Once all resources are re-stored, the migrator updates each CRD's `status.storedVersions` to `["v1beta2"]`, removing `v1beta1`.
4. The migrator also disables CAPI's built-in migrator in the CAPI manager deployment to avoid conflicts and keep migration control centralised in the hypershift operator.

## Scenarios

### 1. Standard install (migration enabled by default)

On both fresh clusters and existing clusters with `v1beta1` resources, migration happens automatically:

```bash
hypershift install \
  [... other flags ...]
```

On a fresh cluster, the CRDs are created directly with `v1beta2` as the storage version. The migrator controller starts but has no work to do since `storedVersions` is already `["v1beta2"]`.

On an existing cluster, the migrator re-stores all CAPI resources at `v1beta2`.

### 2. Verifying migration completed

Wait for the CRD migrator controller to finish. Check that all CAPI CRDs have `storedVersions: ["v1beta2"]`:

```bash
for crd in clusters.cluster.x-k8s.io clusterclasses.cluster.x-k8s.io machinedeployments.cluster.x-k8s.io machines.cluster.x-k8s.io machinesets.cluster.x-k8s.io machinepools.cluster.x-k8s.io machinehealthchecks.cluster.x-k8s.io machinedrainrules.cluster.x-k8s.io ipaddressclaims.ipam.cluster.x-k8s.io ipaddresses.ipam.cluster.x-k8s.io clusterresourcesets.addons.cluster.x-k8s.io clusterresourcesetbindings.addons.cluster.x-k8s.io; do
  echo "$crd: $(kubectl get crd $crd -o jsonpath='{.status.storedVersions}')"
done
```

Expected output after migration:

```text
clusters.cluster.x-k8s.io: ["v1beta2"]
clusterclasses.cluster.x-k8s.io: ["v1beta2"]
...
```

You can also verify the migration annotation is set on each CRD:

```bash
kubectl get crd clusters.cluster.x-k8s.io -o jsonpath='{.metadata.annotations.crd-migration\.cluster\.x-k8s\.io/observed-generation}'
```

Finally, the migrator will keep an up to date status at all times on a ConfigMap in the operator namespace. To check the status:
```bash
kubectl get cm -n hypershift capi-migration-status -o jsonpath='{.data.status}' | jq .
```

By running the above command on a completed migration you will get an output such as:
```json
{
  "totalCRDs": 12,
  "migratedCRDs": 12,
  "conditions": [
    {
      "type": "MigrationComplete",
      "status": "True",
      "lastTransitionTime": "2026-07-20T19:05:37Z",
      "reason": "MigrationComplete",
      "message": "All 12 CRDs have been migrated"
    },
    {
      "type": "Progressing",
      "status": "False",
      "lastTransitionTime": "2026-07-20T19:05:37Z",
      "reason": "MigrationComplete",
      "message": "Migration has completed"
    },
    {
      "type": "Degraded",
      "status": "False",
      "lastTransitionTime": "2026-07-20T19:05:37Z",
      "reason": "NoErrors",
      "message": "No migration errors"
    }
  ]
}
```


### 3. Re-install on an already migrated cluster

If the cluster has already completed migration (`storedVersions: ["v1beta2"]`), running `hypershift install` again is safe. The migrator controller starts but skips all CRDs since their `storedVersions` already equals `["v1beta2"]`.

### 4. Disabling migration

To install without triggering the storage version migration, use the `--disable-capi-migration` flag:

```bash
hypershift install \
  --disable-capi-migration \
  [... other flags ...]
```

When this flag is set, CRDs are installed without overriding their storage version and the CRD migrator controller is not started. The behavior depends on the cluster state:

- **Existing cluster with `v1beta1` CRDs**: The migrator does not start and CRDs remain untouched at `v1beta1`. Everything stays as-is.
- **Already-migrated cluster (migrator was running)**: The migrator is not started on the new operator pods, effectively stopping it. CRDs remain at `v1beta2` — no downgrade occurs.
- **Fresh install (empty cluster)**: CRDs are installed with their default embedded storage version (`v1beta1`). No migration is performed.

## E2E Testing

The `TestCAPIStorageVersionMigration` e2e test validates the full migration flow on a live cluster. It creates a hosted cluster, verifies pre-migration state, reinstalls the HyperShift Operator (which triggers migration by default), waits for migration to complete, and checks cluster health.

### Running the test

Build the e2e binary:

```bash
make e2e
```

Run the migration test:

```bash
./bin/test-e2e \
  -e2e.platform AWS \
  -e2e.base-domain $BASE_DOMAIN \
  -e2e.pull-secret-file $PULL_SECRET \
  -e2e.aws-credentials-file $AWS_CREDS \
  -e2e.aws-private-credentials-file $AWS_CREDS \
  -e2e.external-dns-credentials $AWS_CREDS \
  -e2e.aws-region $REGION \
  -e2e.availability-zones "${REGION}a,${REGION}b,${REGION}c" \
  -e2e.aws-oidc-s3-bucket-name $BUCKET_NAME \
  -e2e.aws-oidc-s3-credentials $AWS_CREDS \
  -e2e.hypershift-operator-latest-image $HO_IMAGE \
  -capi-migration.run-tests \
  -test.run TestCAPIStorageVersionMigration \
  -test.timeout 0 \
  -test.v
```

The `-e2e.hypershift-operator-latest-image` flag must point to an image that includes the CRD migrator controller code. In CI, this is the image built from the PR branch. For local testing, build and push your own image.

The `-capi-migration.run-tests` flag enables the migration test. Without it, the test is skipped. This allows the test to be run as a separate CI job.
