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

const globalConfigCopyLabel = "hypershift.openshift.io/global-config-copy"

func (r *HostedClusterReconciler) cleanupGlobalConfigMaps(ctx context.Context, hc *hyperv1.HostedCluster, controlPlaneNamespace string, desired sets.Set[string]) error {
	desired = desired.Clone()
	desired.Insert(controlplaneoperator.UserCABundle(controlPlaneNamespace).Name)
	hcp := &hyperv1.HostedControlPlane{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: controlPlaneNamespace, Name: hc.Name}, hcp); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get persisted HostedControlPlane configuration: %w", err)
		}
	} else if hcp.Spec.Configuration != nil {
		desired.Insert(configrefs.ConfigMapRefs(hcp.Spec.Configuration)...)
	}
	var copies corev1.ConfigMapList
	if err := r.List(ctx, &copies, client.InNamespace(controlPlaneNamespace), client.MatchingLabels{globalConfigCopyLabel: "true"}); err != nil {
		return fmt.Errorf("failed to list global config copies in namespace %s: %w", controlPlaneNamespace, err)
	}
	for index := range copies.Items {
		candidate := &copies.Items[index]
		if desired.Has(candidate.Name) || len(candidate.OwnerReferences) != 0 || candidate.Annotations[k8sutil.HostedClusterAnnotation] != client.ObjectKeyFromObject(hc).String() {
			continue
		}
		if _, err := k8sutil.DeleteIfNeeded(ctx, r.Client, candidate); err != nil {
			return fmt.Errorf("failed to delete global config copy %s/%s: %w", candidate.Namespace, candidate.Name, err)
		}
	}
	return nil
}
