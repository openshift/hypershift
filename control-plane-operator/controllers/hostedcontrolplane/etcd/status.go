package etcd

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	etcdv1 "github.com/openshift/hypershift/thirdparty/etcd/v1beta2"
)

const (
	etcdClusterLabel            = "etcd_cluster"
	etcdClusterBootstrapTimeout = 5 * time.Minute

	EtcdReasonFailed  = "EtcdFailed"
	EtcdReasonRunning = "EtcdRunning"
	EtcdReasonScaling = "EtcdScalingUp"
)

func etcdClusterConditionByType(conditions []etcdv1.ClusterCondition, t etcdv1.ClusterConditionType) *etcdv1.ClusterCondition {
	for i, cond := range conditions {
		if cond.Type == t {
			return &conditions[i]
		}
	}
	return nil
}

func ComputeEtcdClusterStatus(ctx context.Context, c client.Client, cluster *etcdv1.EtcdCluster) (metav1.Condition, error) {
	availableCondition := etcdClusterConditionByType(cluster.Status.Conditions, etcdv1.ClusterConditionAvailable)

	var cond metav1.Condition
	switch {
	case availableCondition != nil && availableCondition.Status == corev1.ConditionTrue:
		// Etcd cluster is available
		cond = metav1.Condition{
			Type:    string(hyperv1.EtcdAvailable),
			Status:  metav1.ConditionTrue,
			Reason:  EtcdReasonRunning,
			Message: "Etcd cluster is running and available",
		}
	case len(cluster.Status.Members.Ready) == 0 && time.Since(cluster.CreationTimestamp.Time) > etcdClusterBootstrapTimeout:
		// The etcd cluster failed to bootstrap, will delete
		cond = metav1.Condition{
			Type:    string(hyperv1.EtcdAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  EtcdReasonFailed,
			Message: "Etcd cluster failed to bootstrap within timeout, recreating",
		}
	case cluster.Spec.Size > 1 && len(cluster.Status.Members.Ready) <= 1:
		hasTerminatedPods, err := etcdClusterHasTerminatedPods(ctx, c, cluster)
		if err != nil {
			return cond, err
		}
		if hasTerminatedPods {
			cond = metav1.Condition{
				Type:    string(hyperv1.EtcdAvailable),
				Status:  metav1.ConditionFalse,
				Reason:  EtcdReasonFailed,
				Message: "Etcd has failed to achieve quorum after bootstrap, recreating",
			}
		} else {
			cond = metav1.Condition{
				Type:    string(hyperv1.EtcdAvailable),
				Status:  metav1.ConditionFalse,
				Reason:  EtcdReasonScaling,
				Message: "Etcd cluster is scaling up",
			}
		}
	default:
		cond = metav1.Condition{
			Type:    string(hyperv1.EtcdAvailable),
			Status:  metav1.ConditionFalse,
			Reason:  EtcdReasonScaling,
			Message: "Etcd cluster is scaling up",
		}
	}
	return cond, nil
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
