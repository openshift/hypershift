package ntotuning

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func performanceProfileSelector(nodePoolName string) labels.Set {
	return labels.Set{
		PerformanceProfileConfigMapLabel: "true",
		hyperv1.NodePoolLabel:            nodePoolName,
	}
}

// DeleteTuningOutputs removes tuned and PerformanceProfile ConfigMaps for a node pool from the hosted control plane namespace.
func DeleteTuningOutputs(ctx context.Context, c client.Client, controlPlaneNamespace, nodePoolName string) error {
	tunedConfigMap := TunedConfigMap(controlPlaneNamespace, nodePoolName)
	if _, err := k8sutil.DeleteIfNeeded(ctx, c, tunedConfigMap); err != nil {
		return fmt.Errorf("failed to delete tuned ConfigMap: %w", err)
	}
	if err := DeleteConfigMapsByLabel(ctx, c, performanceProfileSelector(nodePoolName), controlPlaneNamespace); err != nil {
		return fmt.Errorf("failed to delete performance profile ConfigMap: %w", err)
	}
	return nil
}

// ReconcileTuningOutputs writes tuned and PerformanceProfile ConfigMaps in the hosted control plane namespace for NTO.
func ReconcileTuningOutputs(
	ctx context.Context,
	c client.Client,
	createOrUpdate upsert.CreateOrUpdateFN,
	controlPlaneNamespace string,
	nodePool *hyperv1.NodePool,
	tunedConfig string,
	performanceProfileConfig string,
	performanceProfileConfigMapName string,
) error {
	log, _ := logr.FromContext(ctx)

	tunedConfigMap := TunedConfigMap(controlPlaneNamespace, nodePool.Name)
	if tunedConfig == "" {
		if _, err := k8sutil.DeleteIfNeeded(ctx, c, tunedConfigMap); err != nil {
			return fmt.Errorf("failed to delete tuned ConfigMap: %w", err)
		}
	} else if result, err := createOrUpdate(ctx, c, tunedConfigMap, func() error {
		return ReconcileTunedConfigMap(tunedConfigMap, nodePool, tunedConfig)
	}); err != nil {
		return fmt.Errorf("failed to reconcile Tuned ConfigMap: %w", err)
	} else {
		log.Info("Reconciled Tuned ConfigMap", "result", result)
	}

	if performanceProfileConfig == "" {
		if err := DeleteConfigMapsByLabel(ctx, c, performanceProfileSelector(nodePool.Name), controlPlaneNamespace); err != nil {
			return fmt.Errorf("failed to delete performance profile ConfigMap: %w", err)
		}
		return nil
	}

	existingPerformanceProfileConfigMapList := &corev1.ConfigMapList{}
	if err := c.List(ctx, existingPerformanceProfileConfigMapList, &client.ListOptions{
		Namespace:     controlPlaneNamespace,
		LabelSelector: labels.SelectorFromValidatedSet(performanceProfileSelector(nodePool.Name)),
	}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	performanceProfileConfigMap := PerformanceProfileConfigMap(controlPlaneNamespace, performanceProfileConfigMapName, nodePool.Name)
	for i := range existingPerformanceProfileConfigMapList.Items {
		ppConfigMap := &existingPerformanceProfileConfigMapList.Items[i]
		if ppConfigMap.Name != performanceProfileConfigMap.Name {
			if _, err := k8sutil.DeleteIfNeeded(ctx, c, ppConfigMap); err != nil {
				return fmt.Errorf("failed to delete performance profile ConfigMap: %w", err)
			}
		}
	}

	result, err := createOrUpdate(ctx, c, performanceProfileConfigMap, func() error {
		return ReconcilePerformanceProfileConfigMap(performanceProfileConfigMap, nodePool, performanceProfileConfig)
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile PerformanceProfile ConfigMap: %w", err)
	}
	log.Info("Reconciled PerformanceProfile ConfigMap", "result", result)
	return nil
}
