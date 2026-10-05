package configuration

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func reconcileClusterVersion(ctx context.Context, hostedClusterClient client.Client, createOrUpdate upsert.CreateOrUpdateFN, params ReconcileParams) error {
	clusterVersion := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}}
	if _, err := createOrUpdate(ctx, hostedClusterClient, clusterVersion, func() error {
		clusterVersion.Spec.ClusterID = configv1.ClusterID(params.ClusterID)
		desiredCaps := capabilities.CalculateEnabledCapabilities(params.Capabilities)
		desiredCaps = capabilities.FilterByKnownCapabilities(desiredCaps, clusterVersion.Status.Capabilities.KnownCapabilities)
		clusterVersion.Spec.Capabilities = &configv1.ClusterVersionCapabilitiesSpec{
			BaselineCapabilitySet:         configv1.ClusterVersionCapabilitySetNone,
			AdditionalEnabledCapabilities: desiredCaps,
		}
		clusterVersion.Spec.Upstream = params.UpdateService
		clusterVersion.Spec.Channel = params.Channel
		clusterVersion.Spec.DesiredUpdate = nil
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile clusterVersion: %w", err)
	}

	return nil
}
