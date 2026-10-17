package configuration

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/globalconfig"
)

func ReconcileImagePolicy(ctx context.Context, hosted HostedCluster, sources []hyperv1.ImageContentSource) error {
	icsp := globalconfig.ImageContentSourcePolicy()

	_, err := hosted.deleteIfNeeded(ctx, icsp)
	if err != nil {
		return fmt.Errorf("failed to delete image content source policy configuration configmap: %w", err)
	}

	idms := globalconfig.ImageDigestMirrorSet()
	if _, err = hosted.upsert(ctx, idms, func() error {
		return globalconfig.ReconcileImageDigestMirrors(idms, &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{ImageContentSources: sources}})
	}); err != nil {
		return fmt.Errorf("failed to reconcile image digest mirror set: %w", err)
	}

	return nil
}
