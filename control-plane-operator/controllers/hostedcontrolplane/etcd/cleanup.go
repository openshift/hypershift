package etcd

import (
	"context"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CleanupOrphanedShards removes resources for shards that are no longer in the spec
// This is important when shards are removed from the configuration to prevent resource leaks
func CleanupOrphanedShards(
	ctx context.Context,
	hcp *hyperv1.HostedControlPlane,
	activeShards []hyperv1.ManagedEtcdShardSpec,
	client client.Client,
) error {
	// Get all etcd StatefulSets in namespace
	stsList := &appsv1.StatefulSetList{}
	if err := client.List(ctx, stsList); err != nil {
		return err
	}

	// Filter for etcd StatefulSets in the HCP namespace
	// Check both metadata labels and name prefix since old v2 etcd may not have the label
	var filtered []appsv1.StatefulSet
	for i := range stsList.Items {
		sts := &stsList.Items[i]
		if sts.Namespace == hcp.Namespace {
			// Match either by label or by name prefix
			hasLabel := sts.Labels != nil && sts.Labels["app"] == "etcd"
			hasName := strings.HasPrefix(sts.Name, "etcd")
			if hasLabel || hasName {
				filtered = append(filtered, *sts)
			}
		}
	}
	stsList.Items = filtered

	// Build set of active shard names
	activeNames := sets.NewString()
	for _, shard := range activeShards {
		activeNames.Insert(resourceNameForShard("etcd", shard.Name))
	}

	// Delete orphaned resources
	for i := range stsList.Items {
		sts := &stsList.Items[i]
		if !activeNames.Has(sts.Name) {
			shardName := extractShardName(sts.Name)
			if err := deleteShardResources(ctx, hcp.Namespace, shardName, client); err != nil {
				return fmt.Errorf("failed to delete orphaned shard %s: %w", sts.Name, err)
			}
		}
	}
	return nil
}

// deleteShardResources deletes all resources for a specific shard
func deleteShardResources(ctx context.Context, namespace, shardName string, c client.Client) error {
	resources := []client.Object{
		manifests.EtcdStatefulSetForShard(namespace, shardName),
		manifests.EtcdClientServiceForShard(namespace, shardName),
		manifests.EtcdDiscoveryServiceForShard(namespace, shardName),
		manifests.EtcdServiceMonitorForShard(namespace, shardName),
		manifests.EtcdPodDisruptionBudgetForShard(namespace, shardName),
	}

	for _, resource := range resources {
		if err := c.Delete(ctx, resource); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// extractShardName extracts the shard name from a resource name
func extractShardName(resourceName string) string {
	if resourceName == "etcd" {
		return "default"
	}
	return strings.TrimPrefix(resourceName, "etcd-")
}

// resourceNameForShard generates resource names for etcd shards
// For backward compatibility, the default shard uses the base name without suffix
// Named shards use the pattern: baseName-shardName
func resourceNameForShard(baseName, shardName string) string {
	if shardName == "default" {
		return baseName
	}
	return fmt.Sprintf("%s-%s", baseName, shardName)
}
