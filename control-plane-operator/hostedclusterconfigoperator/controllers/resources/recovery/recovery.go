package recovery

import (
	"context"
	"fmt"
	"sync/atomic"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	pvcsDeleted = atomic.Bool{}
	podsDeleted = atomic.Bool{}
)

func RecoverMonitoringStack(ctx context.Context, hcp *hyperv1.HostedControlPlane, c client.Client) (bool, error) {
	log := ctrl.LoggerFrom(ctx)
	monitoringStackNS := "openshift-monitoring"
	stsName := "prometheus-k8s"

	// Check deployment of monitoring stack
	prometheusSts := &appsv1.StatefulSet{}
	err := c.Get(ctx, types.NamespacedName{Namespace: monitoringStackNS, Name: stsName}, prometheusSts)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("failed to get prometheus statefulSet %s in namespace %s: %w", stsName, monitoringStackNS, err)
		}

		return false, fmt.Errorf("prometheus statefulSet is still starting, rescheduling reconciliation: %w", err)
	}

	if prometheusSts.Status.AvailableReplicas < prometheusSts.Status.Replicas {
		log.Info("Prometheus statefulSet not ready")

		if !pvcsDeleted.Load() {
			// Delete all PVCs in the namespace only once
			log.Info("Deleting PVCs")
			pvcList := &corev1.PersistentVolumeClaimList{}
			if err := c.List(ctx, pvcList, client.InNamespace(monitoringStackNS)); err != nil {
				return false, fmt.Errorf("failed to list PVCs in namespace %s: %w", monitoringStackNS, err)
			}

			// Delete all PVCs
			for _, pvc := range pvcList.Items {
				log.Info("deleting PVC", "pvc", pvc.Name)
				if err := c.Delete(ctx, &pvc); err != nil {
					return false, fmt.Errorf("failed to delete PVC %s: %w", pvc.Name, err)
				}
			}
			pvcsDeleted.Store(true)
		} else {
			log.Info("PVCs already deleted, skipping")
		}

		if !podsDeleted.Load() {
			// Delete pods associated to the statefulSet
			log.Info("Deleting pods")
			stsPods := &corev1.PodList{}
			if err := c.List(ctx, stsPods, client.InNamespace(monitoringStackNS), client.MatchingLabels(prometheusSts.Spec.Selector.MatchLabels)); err != nil {
				return false, fmt.Errorf("failed to list prometheus pods in namespace %s with labels %v: %w", monitoringStackNS, prometheusSts.Spec.Selector.MatchLabels, err)
			}

			// Delete pods associated to the statefulSet
			for _, pod := range stsPods.Items {
				log.Info("Deleting pod", "pod", pod.Name)
				tmpPod := &corev1.Pod{}
				if err := c.Get(ctx, types.NamespacedName{Namespace: monitoringStackNS, Name: pod.Name}, tmpPod); err != nil {
					return false, fmt.Errorf("failed to get pod %s: %w", pod.Name, err)
				}

				if err := c.Delete(ctx, tmpPod); err != nil {
					return false, fmt.Errorf("failed to delete pod %s: %w", pod.Name, err)
				}
			}

			podsDeleted.Store(true)
		} else {
			log.Info("Pods already deleted, skipping")
		}

		return false, nil
	}

	return true, nil
}
