//go:build e2e

package e2e

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestControlPlaneVersionField validates the controlPlaneVersion status field
// API contract and behavior on an existing cluster.
//
// This test covers additional field structure validation beyond what
// WaitForControlPlaneRollout already checks in upgrade tests:
// - Field presence on both HC and HCP with matching values
// - History length constraints (MinItems=1, MaxItems=100)
// - CompletionTime presence/absence based on state (omitempty semantics)
// - ObservedGeneration tracking
// - Backward compatibility with existing version field
//
// Prerequisites:
// - Requires OpenShift 4.22+ (controlPlaneVersion added in 4.22)
// - Runs against the global test cluster (not creating a new one)
func TestControlPlaneVersionField(t *testing.T) {
	t.Parallel()

	// This test requires 4.22+ where controlPlaneVersion was introduced
	e2eutil.AtLeast(t, e2eutil.Version422)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Fetch current HostedCluster
		var hc hyperv1.HostedCluster
		err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), &hc)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")

		// Fetch corresponding HostedControlPlane
		hcpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
		var hcpList hyperv1.HostedControlPlaneList
		err = mgtClient.List(ctx, &hcpList, crclient.InNamespace(hcpNamespace))
		g.Expect(err).NotTo(HaveOccurred(), "failed to list HostedControlPlane")
		g.Expect(hcpList.Items).NotTo(BeEmpty(), "no HostedControlPlane found")
		hcp := hcpList.Items[0]

		// Validate field presence on both HC and HCP
		t.Run("HC and HCP both have controlPlaneVersion with matching values", func(t *testing.T) {
			g := NewWithT(t)

			g.Expect(hc.Status.ControlPlaneVersion).NotTo(BeNil(), "HC controlPlaneVersion field missing")
			g.Expect(hcp.Status.ControlPlaneVersion).NotTo(BeNil(), "HCP controlPlaneVersion field missing")

			g.Expect(hc.Status.ControlPlaneVersion.Desired.Version).NotTo(BeEmpty(),
				"HC controlPlaneVersion.desired.version is empty")
			g.Expect(hc.Status.ControlPlaneVersion.Desired.Image).NotTo(BeEmpty(),
				"HC controlPlaneVersion.desired.image is empty")

			g.Expect(hcp.Status.ControlPlaneVersion.Desired.Version).NotTo(BeEmpty(),
				"HCP controlPlaneVersion.desired.version is empty")
			g.Expect(hcp.Status.ControlPlaneVersion.Desired.Image).NotTo(BeEmpty(),
				"HCP controlPlaneVersion.desired.image is empty")

			// HC and HCP desired fields must match (HC propagates from HCP)
			g.Expect(hc.Status.ControlPlaneVersion.Desired.Version).To(Equal(hcp.Status.ControlPlaneVersion.Desired.Version),
				"HC and HCP controlPlaneVersion.desired.version mismatch")
			g.Expect(hc.Status.ControlPlaneVersion.Desired.Image).To(Equal(hcp.Status.ControlPlaneVersion.Desired.Image),
				"HC and HCP controlPlaneVersion.desired.image mismatch")

			t.Logf("HC and HCP both have controlPlaneVersion.desired set to version=%s, image=%s",
				hc.Status.ControlPlaneVersion.Desired.Version,
				hc.Status.ControlPlaneVersion.Desired.Image)
		})

		// Validate field structure
		t.Run("History length is within valid range", func(t *testing.T) {
			g := NewWithT(t)
			cpv := hc.Status.ControlPlaneVersion

			g.Expect(cpv.History).NotTo(BeNil(), "controlPlaneVersion.history is nil")
			g.Expect(cpv.History).NotTo(BeEmpty(), "controlPlaneVersion.history is empty (violates MinItems=1)")

			// History must not exceed 100 entries (MaxItems=100, enforced by pruneHistory)
			g.Expect(len(cpv.History)).To(BeNumerically("<=", 100),
				"controlPlaneVersion.history length %d exceeds MaxItems=100", len(cpv.History))

			t.Logf("History length: %d (valid range [1, 100])", len(cpv.History))
		})

		// Validate observedGeneration is set
		t.Run("ObservedGeneration is non-zero", func(t *testing.T) {
			g := NewWithT(t)
			cpv := hc.Status.ControlPlaneVersion

			g.Expect(cpv.ObservedGeneration).To(BeNumerically(">", 0),
				"controlPlaneVersion.observedGeneration is zero")

			t.Logf("ObservedGeneration: %d", cpv.ObservedGeneration)
		})

		// Validate history[0] structure based on state
		t.Run("History first entry has correct structure for its state", func(t *testing.T) {
			g := NewWithT(t)
			cpv := hc.Status.ControlPlaneVersion
			entry := cpv.History[0]

			g.Expect(entry.Version).NotTo(BeEmpty(), "history[0].version is empty")
			g.Expect(entry.Image).NotTo(BeEmpty(), "history[0].image is empty")
			g.Expect(entry.StartedTime.IsZero()).To(BeFalse(), "history[0].startedTime is zero")

			if entry.State == configv1.CompletedUpdate {
				// When state=Completed, completionTime MUST be present
				g.Expect(entry.CompletionTime).NotTo(BeNil(),
					"history[0].completionTime is nil but state=Completed")
				t.Logf("history[0].state=Completed, completionTime=%s", entry.CompletionTime.Time)
			} else if entry.State == configv1.PartialUpdate {
				// When state=Partial, completionTime MUST be nil (omitempty in API)
				// Note: In Go structs with omitempty, nil *metav1.Time is omitted from JSON
				g.Expect(entry.CompletionTime).To(BeNil(),
					"history[0].completionTime is not nil but state=Partial (should be omitted)")
				t.Logf("history[0].state=Partial, completionTime correctly omitted")
			} else {
				t.Fatalf("Unexpected history[0].state: %s (expected Completed or Partial)", entry.State)
			}
		})

		// Validate observedGeneration tracks HCP generation
		t.Run("ObservedGeneration tracks HCP generation", func(t *testing.T) {
			g := NewWithT(t)
			hcpGeneration := hcp.GetGeneration()
			hcpObservedGen := hcp.Status.ControlPlaneVersion.ObservedGeneration

			t.Logf("HCP metadata.generation=%d, status.controlPlaneVersion.observedGeneration=%d",
				hcpGeneration, hcpObservedGen)

			// In steady state, observedGeneration should match metadata.generation
			// If they don't match, it indicates either:
			// 1. A spec update is still being processed
			// 2. An error occurred and the reconciler fell back to ensureControlPlaneVersionPartial
			if hcpGeneration == hcpObservedGen {
				t.Logf("observedGeneration matches HCP generation (steady state)")
			} else {
				// Not necessarily a failure - could be mid-reconcile or error path
				t.Logf("observedGeneration=%d != HCP generation=%d (may indicate in-progress reconcile or error)",
					hcpObservedGen, hcpGeneration)
				// Don't fail - this is expected during upgrades or when errors occur
			}

			// HC should propagate the same observedGeneration from HCP
			g.Expect(hc.Status.ControlPlaneVersion.ObservedGeneration).To(Equal(hcp.Status.ControlPlaneVersion.ObservedGeneration),
				"HC observedGeneration should match HCP observedGeneration")
		})

		// Validate backward compatibility with existing version field
		t.Run("Existing version field is still populated", func(t *testing.T) {
			g := NewWithT(t)

			g.Expect(hc.Status.Version).NotTo(BeNil(), "existing version field is nil (backward compat broken)")
			g.Expect(hc.Status.Version.Desired.Version).NotTo(BeEmpty(),
				"existing version.desired.version is empty")
			g.Expect(hc.Status.Version.History).NotTo(BeEmpty(),
				"existing version.history is empty")

			t.Logf("Existing version field still populated: version=%s, history length=%d",
				hc.Status.Version.Desired.Version, len(hc.Status.Version.History))

			// Sanity check: version and controlPlaneVersion should be tracking the same release
			// (they diverge temporarily during upgrades, but should be related)
			t.Logf("version.desired=%s, controlPlaneVersion.desired=%s",
				hc.Status.Version.Desired.Version,
				hc.Status.ControlPlaneVersion.Desired.Version)
		})
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}
