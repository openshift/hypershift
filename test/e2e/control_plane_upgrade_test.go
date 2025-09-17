//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestUpgradeControlPlane(t *testing.T) {
	t.Parallel()

	if globalOpts.Platform == hyperv1.AzurePlatform && e2eutil.IsLessThan(e2eutil.Version420) {
		t.Skip("TODO: Enable this test for Azure in 4.19. Skipping for now to let the 4.20 suite be covered.")
	}

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	t.Logf("Starting control plane upgrade test. FromImage: %s, toImage: %s", globalOpts.PreviousReleaseImage, globalOpts.LatestReleaseImage)

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ReleaseImage = globalOpts.PreviousReleaseImage
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Sanity check the cluster by waiting for the nodes to report ready
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		// Set the semantic version to the latest release image for version gating tests
		err := e2eutil.SetReleaseImageVersion(testContext, globalOpts.LatestReleaseImage, globalOpts.ConfigurableClusterOptions.PullSecretFile)
		if err != nil {
			g.Expect(err).NotTo(HaveOccurred(), "failed to set latest release image version")
		}

		// Update the cluster image
		t.Logf("Updating cluster image. Image: %s", globalOpts.LatestReleaseImage)
		err = e2eutil.UpdateObject(t, ctx, mgtClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.Release.Image = globalOpts.LatestReleaseImage
			if obj.Annotations == nil {
				obj.Annotations = make(map[string]string)
			}
			obj.Annotations[hyperv1.ForceUpgradeToAnnotation] = globalOpts.LatestReleaseImage
			if globalOpts.DisablePKIReconciliation {
				obj.Annotations[hyperv1.DisablePKIReconciliationAnnotation] = "true"
			}
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed update hostedcluster image")

		// Wait for the new rollout to be complete
		e2eutil.WaitForImageRollout(t, ctx, mgtClient, hostedCluster)
		err = mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

		t.Run("Verifying featureGate status has entries for the same versions as clusterVersion", func(t *testing.T) {
			e2eutil.AtLeast(t, e2eutil.Version419)

			g := NewWithT(t)

			clusterVersion := &configv1.ClusterVersion{}
			err = guestClient.Get(ctx, crclient.ObjectKey{Name: "version"}, clusterVersion)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get ClusterVersion resource")

			featureGate := &configv1.FeatureGate{}
			err = guestClient.Get(ctx, crclient.ObjectKey{Name: "cluster"}, featureGate)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get FeatureGate resource")

			clusterVersions := make(map[string]bool)
			for _, history := range clusterVersion.Status.History {
				clusterVersions[history.Version] = true
			}

			// check that each version in the ClusterVersion history has a corresponding entry in FeatureGate status.
			for version := range clusterVersions {
				found := false
				for _, details := range featureGate.Status.FeatureGates {
					if details.Version == version {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("version %s found in ClusterVersion history but missing in FeatureGate status", version)
				}
			}
			g.Expect(len(featureGate.Status.FeatureGates)).To(Equal(len(clusterVersion.Status.History)),
				"Expected the same number of entries in FeatureGate status (%d) as in ClusterVersion history (%d)",
				len(featureGate.Status.FeatureGates), len(clusterVersion.Status.History))

			t.Log("Validation passed")
		})

		e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, ctx, mgtClient, guestClient, hostedCluster.Spec.Platform.Type, hostedCluster.Namespace)
		e2eutil.EnsureNoCrashingPods(t, ctx, mgtClient, hostedCluster)
		e2eutil.EnsureMachineDeploymentGeneration(t, ctx, mgtClient, hostedCluster, 1)
		// TODO (cewong): enable this test once the fix for KAS->Kubelet communication has merged
		// e2eutil.EnsureNodeCommunication(t, ctx, client, hostedCluster)
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "control-plane-upgrade", globalOpts.ServiceAccountSigningKey)
}
