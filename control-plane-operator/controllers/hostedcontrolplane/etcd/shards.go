package etcd

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/upsert"

	"k8s.io/apimachinery/pkg/util/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReconcileEtcdShards reconciles all etcd shards for a HostedControlPlane
// This is the main entry point for etcd reconciliation outside the v2 framework
func ReconcileEtcdShards(
	ctx context.Context,
	hcp *hyperv1.HostedControlPlane,
	params *ShardParams,
	client client.Client,
	createOrUpdate upsert.CreateOrUpdateFN,
	metricsSet metrics.MetricsSet,
) error {
	shards := hcp.Spec.Etcd.Managed.EffectiveShards(hcp)
	if len(shards) == 0 {
		return fmt.Errorf("no etcd shards configured")
	}

	var errs []error
	for _, shard := range shards {
		if err := reconcileShard(ctx, hcp, shard, client, createOrUpdate, params, metricsSet); err != nil {
			errs = append(errs, fmt.Errorf("shard %s: %w", shard.Name, err))
			// Continue with other shards even if one fails
		}
	}

	return errors.NewAggregate(errs)
}

// reconcileShard reconciles a single etcd shard's resources
// This creates/updates all resources for one shard: StatefulSet, Services, ServiceMonitor, PDB
func reconcileShard(
	ctx context.Context,
	hcp *hyperv1.HostedControlPlane,
	shard hyperv1.ManagedEtcdShardSpec,
	client client.Client,
	createOrUpdate upsert.CreateOrUpdateFN,
	params *ShardParams,
	metricsSet metrics.MetricsSet,
) error {
	// StatefulSet
	sts := manifests.EtcdStatefulSetForShard(hcp.Namespace, shard.Name)
	if _, err := createOrUpdate(ctx, client, sts, func() error {
		return ReconcileStatefulSet(sts, hcp, shard, params)
	}); err != nil {
		return fmt.Errorf("failed to reconcile statefulset: %w", err)
	}

	// Client Service
	clientSvc := manifests.EtcdClientServiceForShard(hcp.Namespace, shard.Name)
	if _, err := createOrUpdate(ctx, client, clientSvc, func() error {
		return ReconcileClientService(clientSvc, hcp, shard)
	}); err != nil {
		return fmt.Errorf("failed to reconcile client service: %w", err)
	}

	// Discovery Service
	discoverySvc := manifests.EtcdDiscoveryServiceForShard(hcp.Namespace, shard.Name)
	if _, err := createOrUpdate(ctx, client, discoverySvc, func() error {
		return ReconcileDiscoveryService(discoverySvc, hcp, shard)
	}); err != nil {
		return fmt.Errorf("failed to reconcile discovery service: %w", err)
	}

	// ServiceMonitor
	sm := manifests.EtcdServiceMonitorForShard(hcp.Namespace, shard.Name)
	if _, err := createOrUpdate(ctx, client, sm, func() error {
		return ReconcileServiceMonitor(sm, hcp, shard, metricsSet)
	}); err != nil {
		return fmt.Errorf("failed to reconcile service monitor: %w", err)
	}

	// PodDisruptionBudget (only in HA mode)
	if hcp.Spec.ControllerAvailabilityPolicy == hyperv1.HighlyAvailable {
		pdb := manifests.EtcdPodDisruptionBudgetForShard(hcp.Namespace, shard.Name)
		if _, err := createOrUpdate(ctx, client, pdb, func() error {
			return ReconcilePodDisruptionBudget(pdb, hcp, shard)
		}); err != nil {
			return fmt.Errorf("failed to reconcile pdb: %w", err)
		}
	}

	return nil
}
