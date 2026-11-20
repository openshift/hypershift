package capimanager

import (
	"fmt"

	"github.com/openshift/hypershift/support/backwardcompat"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/blang/semver"
)

// clientRateLimitArgs raise the client-side rate limits of the CAPI manager's
// management cluster client (--kube-api-*) and of the cluster cache client it
// uses to reach the hosted cluster (--clustercache-client-*), which otherwise
// throttle machine reconciliation on large clusters. In the CAPI managers
// inspected for OCP 4.19 to 5.1 and in the backward-compatible pinned image,
// --clustercache-client-* defaults to 20 QPS and a burst of 30, and so does
// --kube-api-* up to OCP 4.22; from OCP 4.23 (5.0) --kube-api-* already
// defaults to these values. They are set explicitly for uniform behavior across
// payload versions; revisit them if upstream raises its defaults above these
// values.
var clientRateLimitArgs = []string{
	"--kube-api-qps=100",
	"--kube-api-burst=200",
	"--clustercache-client-qps=100",
	"--clustercache-client-burst=200",
}

func (capi *CAPIManagerOptions) adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	versionStr := cpContext.ReleaseImageProvider.Version()
	version, err := semver.Parse(versionStr)
	if err != nil {
		return fmt.Errorf("failed to parse version (%s): %w", versionStr, err)
	}

	tlsArgs, err := config.TLSArgs(cpContext.HCP.Spec.Configuration.GetTLSSecurityProfile())
	if err != nil {
		return err
	}

	// The client rate limit flags first appear in the CAPI manager shipped with
	// OCP 4.19 (the OCP 4.18 manager only has the older
	// --clustercachetracker-client-* flags) and are registered by the
	// backward-compatible image pinned for older payloads. A user-provided image
	// override is assumed to match the payload version, as for the
	// version-gated flags below. An image that does not register these flags
	// fails to start with them, so if the override is not exactly the pinned
	// image, payloads below 4.19 keep their previous args.
	usesBackwardCompatibleImage := capi.imageOverride != "" &&
		capi.imageOverride == backwardcompat.GetBackwardCompatibleCAPIImage(version)
	supportsClientRateLimitArgs := version.GE(config.Version419) || usesBackwardCompatibleImage

	podspec.UpdateContainer("manager", deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		if version.GE(config.Version419) {
			c.Args = append(c.Args, "--feature-gates=MachineSetPreflightChecks=false")
		}
		if version.Major >= 5 || (version.Major == 4 && version.Minor >= 23) {
			if len(tlsArgs) > 0 {
				c.Args = append(c.Args, tlsArgs...)
			}
		}

		if version.GE(config.Version420) {
			c.Args = append(c.Args,
				"--skip-crd-migration-phases=StorageVersionMigration",
				"--skip-crd-migration-phases=CleanupManagedFields",
			)
		}

		if supportsClientRateLimitArgs {
			c.Args = append(c.Args, clientRateLimitArgs...)
		}

		if len(capi.imageOverride) > 0 {
			c.Image = capi.imageOverride
		}
	})

	if deployment.Annotations == nil {
		deployment.Annotations = make(map[string]string)
	}
	deployment.Annotations[k8sutil.HostedClusterAnnotation] = cpContext.HCP.Annotations[k8sutil.HostedClusterAnnotation]

	return nil
}
