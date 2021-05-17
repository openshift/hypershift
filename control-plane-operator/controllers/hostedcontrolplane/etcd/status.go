package etcd

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	hcputil "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
	etcdv1 "github.com/openshift/hypershift/thirdparty/etcd/v1beta2"
)

const (
	etcdClusterLabel            = "etcd_cluster"
	etcdClusterBootstrapTimeout = 5 * time.Minute

	EtcdReasonNotCreated = "EtcdNotCreated"
	EtcdReasonFailed     = "EtcdFailed"
	EtcdReasonRunning    = "EtcdRunning"
	EtcdReasonScaling    = "EtcdScalingUp"
)

func etcdClusterConditionByType(conditions []etcdv1.ClusterCondition, t etcdv1.ClusterConditionType) *etcdv1.ClusterCondition {
	for i, cond := range conditions {
		if cond.Type == t {
			return &conditions[i]
		}
	}
	return nil
}

func ReconcileEtcdClusterStatus(ctx context.Context, c client.Client, hcpStatus *hyperv1.HostedControlPlaneStatus, cluster *etcdv1.EtcdCluster) error {
	log := ctrl.LoggerFrom(ctx)
	if cluster == nil {
		// etcd cluster doesn't yet exist, nothing to do yet
		log.Info("Etcd cluster doesn't exist yet")
		hcputil.SetConditionByType(&hcpStatus.Conditions, hyperv1.EtcdAvailable, hyperv1.ConditionFalse, EtcdReasonNotCreated, "Etcd cluster has not been created yet")
		return nil
	}
	availableCondition := etcdClusterConditionByType(cluster.Status.Conditions, etcdv1.ClusterConditionAvailable)

	switch {
	case availableCondition != nil && availableCondition.Status == corev1.ConditionTrue:
		// Etcd cluster is available
		hcputil.SetConditionByType(&hcpStatus.Conditions, hyperv1.EtcdAvailable, hyperv1.ConditionTrue, EtcdReasonRunning, "Etcd cluster is running and available")
	case len(cluster.Status.Members.Ready) == 0 && time.Since(cluster.CreationTimestamp.Time) > etcdClusterBootstrapTimeout:
		// The etcd cluster failed to bootstrap, will delete
		hcputil.SetConditionByType(&hcpStatus.Conditions, hyperv1.EtcdAvailable, hyperv1.ConditionFalse, EtcdReasonFailed, "Etcd cluster failed to bootstrap within timeout, recreating")
	case cluster.Spec.Size > 1 && len(cluster.Status.Members.Ready) <= 1:
		hasTerminatedPods, err := etcdClusterHasTerminatedPods(ctx, c, cluster)
		if err != nil {
			return err
		}
		if hasTerminatedPods {
			hcputil.SetConditionByType(&hcpStatus.Conditions, hyperv1.EtcdAvailable, hyperv1.ConditionFalse, EtcdReasonFailed, "Etcd has failed to achieve quorum after bootstrap, recreating")
		} else {
			hcputil.SetConditionByType(&hcpStatus.Conditions, hyperv1.EtcdAvailable, hyperv1.ConditionFalse, EtcdReasonScaling, "Etcd cluster is scaling up")
		}
	default:
		hcputil.SetConditionByType(&hcpStatus.Conditions, hyperv1.EtcdAvailable, hyperv1.ConditionFalse, EtcdReasonScaling, "Etcd cluster is scaling up")
	}
	return nil
}

func etcdClusterHasTerminatedPods(ctx context.Context, c client.Client, cluster *etcdv1.EtcdCluster) (bool, error) {
	// If only one member ready and waiting for another to come up, check pod status
	etcdPods := &corev1.PodList{}
	err := c.List(ctx, etcdPods, client.MatchingLabels{etcdClusterLabel: cluster.Name})
	if err != nil {
		return false, fmt.Errorf("cannot list etcd cluster pods: %w", err)
	}
	// Check for any pods in error
	for _, pod := range etcdPods.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Terminated != nil {
				return true, nil
			}
		}
	}
	return false, nil
}
