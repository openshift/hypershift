package recovery

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func RecoverMonitoringStack(ctx context.Context, hcp *hyperv1.HostedControlPlane, c client.Client) error {
	log := ctrl.LoggerFrom(ctx)
	monitoringStackNS := "openshift-monitoring"
	stsName := "prometheus-k8s"

	// Check deployment of monitoring stack
	prometheusSts := &appsv1.StatefulSet{}
	err := c.Get(ctx, types.NamespacedName{Namespace: monitoringStackNS, Name: stsName}, prometheusSts)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get prometheus statefulSet %s in namespace %s: %w", stsName, monitoringStackNS, err)
		}

		return fmt.Errorf("prometheus statefulSet is still starting, rescheduling reconciliation: %w", err)
	}

	if prometheusSts.Status.ReadyReplicas < prometheusSts.Status.Replicas {
		log.Info("Prometheus statefulSet not ready, deleting pods")
		stsPods := &corev1.PodList{}
		if err := c.List(ctx, stsPods, client.InNamespace(monitoringStackNS), client.MatchingLabels(prometheusSts.Spec.Selector.MatchLabels)); err != nil {
			return fmt.Errorf("failed to list prometheus pods in namespace %s with labels %v: %w", monitoringStackNS, prometheusSts.Spec.Selector.MatchLabels, err)
		}

		// Delete pods associated to the statefulSet
		for _, pod := range stsPods.Items {
			if err := c.Delete(ctx, &pod); err != nil {
				return fmt.Errorf("failed to delete pod %s: %w", pod.Name, err)
			}
		}

		return fmt.Errorf("prometheus statefulSet pods were restarted, rescheduling reconciliation")
	}

	return nil
}
