package etcd

import (
	"context"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReconcileControlPlaneComponent creates/updates the ControlPlaneComponent resource for etcd
// This is needed for other v2 components that depend on etcd
func ReconcileControlPlaneComponent(
	ctx context.Context,
	hcp *hyperv1.HostedControlPlane,
	shards []hyperv1.ManagedEtcdShardSpec,
	client client.Client,
	version string,
) error {
	cpc := &hyperv1.ControlPlaneComponent{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd",
			Namespace: hcp.Namespace,
		},
	}

	// Get aggregate status
	condition, err := AggregateShardStatus(ctx, hcp, shards, client)
	if err != nil {
		return err
	}

	// Build resource list from all shards
	var resources []hyperv1.ComponentResource
	for _, shard := range shards {
		stsName := manifests.EtcdStatefulSetForShard(hcp.Namespace, shard.Name).Name
		resources = append(resources, hyperv1.ComponentResource{
			Group: "apps",
			Kind:  "StatefulSet",
			Name:  stsName,
		})
	}

	// Check if ControlPlaneComponent exists
	err = client.Get(ctx, types.NamespacedName{Namespace: hcp.Namespace, Name: "etcd"}, cpc)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		// Create new ControlPlaneComponent
		cpc.OwnerReferences = []metav1.OwnerReference{
			{
				APIVersion: hyperv1.GroupVersion.String(),
				Kind:       "HostedControlPlane",
				Name:       hcp.Name,
				UID:        hcp.UID,
			},
		}
		if err := client.Create(ctx, cpc); err != nil {
			return err
		}
		// Need to Get it again to update status
		if err := client.Get(ctx, types.NamespacedName{Namespace: hcp.Namespace, Name: "etcd"}, cpc); err != nil {
			return err
		}
	}

	// Update status (works for both newly created and existing resources)
	now := metav1.Now()
	cpc.Status.Version = version
	cpc.Status.Resources = resources
	cpc.Status.Conditions = []metav1.Condition{
		{
			Type:               "Available",
			Status:             condition.Status,
			Reason:             condition.Reason,
			Message:            condition.Message,
			LastTransitionTime: now,
			ObservedGeneration: hcp.Generation,
		},
		{
			Type:               "RolloutComplete",
			Status:             condition.Status,
			Reason:             condition.Reason,
			Message:            condition.Message,
			LastTransitionTime: now,
			ObservedGeneration: hcp.Generation,
		},
	}
	return client.Status().Update(ctx, cpc)
}

// AggregateShardStatus aggregates status from all etcd shards into a single condition
// This provides overall etcd health status based on the health of individual shards
func AggregateShardStatus(
	ctx context.Context,
	hcp *hyperv1.HostedControlPlane,
	shards []hyperv1.ManagedEtcdShardSpec,
	client client.Client,
) (*metav1.Condition, error) {
	var criticalReady, nonCriticalReady int
	var criticalTotal, nonCriticalTotal int
	var messages []string

	for _, shard := range shards {
		sts := manifests.EtcdStatefulSetForShard(hcp.Namespace, shard.Name)
		if err := client.Get(ctx, types.NamespacedName{Namespace: sts.Namespace, Name: sts.Name}, sts); err != nil {
			if apierrors.IsNotFound(err) {
				messages = append(messages, fmt.Sprintf("shard %s StatefulSet not found", shard.Name))
				continue
			}
			return nil, err
		}

		requiredReplicas := *sts.Spec.Replicas
		readyReplicas := sts.Status.ReadyReplicas

		// Critical shards need quorum
		if shard.Priority == hyperv1.EtcdShardPriorityCritical {
			criticalTotal += int(requiredReplicas)
			if readyReplicas >= requiredReplicas/2+1 {
				criticalReady += int(readyReplicas)
			}
		} else {
			nonCriticalTotal += int(requiredReplicas)
			if readyReplicas == requiredReplicas {
				nonCriticalReady += int(readyReplicas)
			}
		}

		if readyReplicas < requiredReplicas {
			messages = append(messages, fmt.Sprintf("shard %s: %d/%d replicas ready", shard.Name, readyReplicas, requiredReplicas))
		}
	}

	// All CRITICAL shards must have quorum
	if criticalTotal > 0 && criticalReady < criticalTotal/2+1 {
		return &metav1.Condition{
			Type:    string(hyperv1.EtcdAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  hyperv1.EtcdWaitingForQuorumReason,
			Message: fmt.Sprintf("Critical shards not ready: %s", strings.Join(messages, "; ")),
		}, nil
	}

	// All shards fully ready
	if len(messages) == 0 {
		return &metav1.Condition{
			Type:    string(hyperv1.EtcdAvailable),
			Status:  metav1.ConditionTrue,
			Reason:  hyperv1.EtcdQuorumAvailableReason,
			Message: "All etcd shards available",
		}, nil
	}

	// Some non-critical shards degraded
	return &metav1.Condition{
		Type:    string(hyperv1.EtcdAvailable),
		Status:  metav1.ConditionTrue,
		Reason:  "EtcdPartiallyAvailable",
		Message: fmt.Sprintf("Critical shards ready, degraded shards: %s", strings.Join(messages, "; ")),
	}, nil
}
