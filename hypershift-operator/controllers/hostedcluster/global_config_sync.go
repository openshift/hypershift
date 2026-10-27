package hostedcluster

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/configrefs"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/support/k8sutil"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	globalConfigCopyLabel          = "hypershift.openshift.io/global-config-copy"
	globalConfigOwnerUIDAnnotation = "hypershift.openshift.io/global-config-owner-uid"
)

func validateGlobalConfigMapSource(source *corev1.ConfigMap, hc *hyperv1.HostedCluster) error {
	if _, exists := source.Labels[globalConfigCopyLabel]; exists {
		return fmt.Errorf("source must not contain reserved label %s", globalConfigCopyLabel)
	}
	if _, exists := source.Annotations[globalConfigOwnerUIDAnnotation]; exists {
		return fmt.Errorf("source must not contain reserved annotation %s", globalConfigOwnerUIDAnnotation)
	}
	if cluster, exists := source.Annotations[k8sutil.HostedClusterAnnotation]; exists && cluster != client.ObjectKeyFromObject(hc).String() {
		return fmt.Errorf("source has a conflicting HostedCluster annotation")
	}
	return nil
}

func globalConfigMapTracked(configMap *corev1.ConfigMap, hc *hyperv1.HostedCluster) bool {
	return hc.UID != "" && len(configMap.OwnerReferences) == 0 &&
		configMap.Labels[globalConfigCopyLabel] == "true" &&
		configMap.Annotations[globalConfigOwnerUIDAnnotation] == string(hc.UID) &&
		configMap.Annotations[k8sutil.HostedClusterAnnotation] == client.ObjectKeyFromObject(hc).String()
}

func (reconciler *HostedClusterReconciler) cleanupGlobalConfigMaps(ctx context.Context, hc *hyperv1.HostedCluster, controlPlaneNamespace string, desired sets.Set[string]) error {
	if hc.UID == "" || controlPlaneNamespace == "" || controlPlaneNamespace == hc.Namespace {
		return fmt.Errorf("global config cleanup requires a HostedCluster UID and a separate control plane namespace")
	}
	desired = desired.Clone()
	desired.Insert(controlplaneoperator.UserCABundle(controlPlaneNamespace).Name)
	hcp := &hyperv1.HostedControlPlane{}
	if err := reconciler.Get(ctx, client.ObjectKey{Namespace: controlPlaneNamespace, Name: hc.Name}, hcp); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get persisted HostedControlPlane configuration: %w", err)
		}
	} else if hcp.Spec.Configuration != nil {
		desired.Insert(configrefs.ConfigMapRefs(hcp.Spec.Configuration)...)
	}
	var copies corev1.ConfigMapList
	if err := reconciler.List(ctx, &copies, client.InNamespace(controlPlaneNamespace), client.MatchingLabels{globalConfigCopyLabel: "true"}); err != nil {
		return fmt.Errorf("failed to list global config copies in namespace %s: %w", controlPlaneNamespace, err)
	}
	for index := range copies.Items {
		candidate := &copies.Items[index]
		if candidate.Namespace != controlPlaneNamespace || desired.Has(candidate.Name) || !globalConfigMapTracked(candidate, hc) {
			continue
		}
		if candidate.UID == "" || candidate.ResourceVersion == "" {
			return fmt.Errorf("cannot safely delete global config copy %s/%s without UID and resourceVersion", candidate.Namespace, candidate.Name)
		}
		if err := reconciler.Delete(ctx, candidate, client.Preconditions{UID: &candidate.UID, ResourceVersion: &candidate.ResourceVersion}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete global config copy %s/%s: %w", candidate.Namespace, candidate.Name, err)
		}
	}
	return nil
}
