package configuration

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/namespaces"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func reconcileNamespaces(ctx context.Context, hostedClusterClient client.Client, createOrUpdate upsert.CreateOrUpdateFN, params ReconcileParams) error {
	namespaceManifests := []struct {
		manifest  func() *corev1.Namespace
		reconcile func(*corev1.Namespace) error
	}{
		{manifest: manifests.NamespaceOpenShiftAPIServer},
		{manifest: manifests.NamespaceOpenShiftInfra, reconcile: namespaces.ReconcileOpenShiftInfraNamespace},
		{manifest: manifests.NamespaceOpenshiftCloudControllerManager},
		{manifest: manifests.NamespaceOpenShiftControllerManager},
		{manifest: manifests.NamespaceKubeAPIServer, reconcile: namespaces.ReconcileKubeAPIServerNamespace},
		{manifest: manifests.NamespaceKubeControllerManager},
		{manifest: manifests.NamespaceKubeScheduler},
		{manifest: manifests.NamespaceEtcd},
		{manifest: manifests.NamespaceIngress, reconcile: namespaces.ReconcileOpenShiftIngressNamespace},
		{manifest: manifests.NamespaceAuthentication},
		{manifest: manifests.NamespaceRouteControllerManager},
	}

	var errs []error
	for _, m := range namespaceManifests {
		ns := m.manifest()
		if ns.Name == "openshift-ingress" && !capabilities.IsIngressCapabilityEnabled(params.Capabilities) {
			continue
		}
		if _, err := createOrUpdate(ctx, hostedClusterClient, ns, func() error {
			if m.reconcile != nil {
				return m.reconcile(ns)
			}
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to reconcile namespace %s: %w", ns.Name, err))
		}
	}

	return utilerrors.NewAggregate(errs)
}
