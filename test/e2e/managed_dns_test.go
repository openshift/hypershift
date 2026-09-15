//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestCreateClusterManagedDNS creates an AWS hosted cluster with CPO-managed ingress
// DNS enabled (spec.platform.aws.managedDNS) and no NS delegation configured. In that
// mode the CPO only creates the public and private Route53 ingress zones and reports
// them ready, so the AWSManagedDNSAvailable condition reaches True deterministically
// without depending on a controllable parent zone or live NS resolution. Delegation
// (ExternalDNS/Manual) is intentionally out of scope here because verifying it requires
// a parent zone and propagated NS records, which are not deterministic in e2e.
func TestCreateClusterManagedDNS(t *testing.T) {
	// AWSManagedDNS is a TechPreview feature; only exercise it where the gate exists.
	e2eutil.AtLeast(t, e2eutil.Version423)

	t.Parallel()

	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("managed ingress DNS is only supported on the AWS platform")
	}

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.NodePoolReplicas = 1
	// The AWSManagedDNS feature gate is only enabled in the TechPreviewNoUpgrade set.
	clusterOpts.FeatureSet = string(configv1.TechPreviewNoUpgrade)

	// Enable managed ingress DNS without delegation before the cluster is applied.
	clusterOpts.BeforeApply = func(o crclient.Object) {
		if hc, ok := o.(*hyperv1.HostedCluster); ok {
			hc.Spec.Platform.AWS.ManagedDNS = &hyperv1.AWSManagedDNSSpec{}
		}
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		g.Expect(hostedCluster.Spec.Platform.AWS).NotTo(BeNil(), "AWS platform spec should be set")
		g.Expect(hostedCluster.Spec.Platform.AWS.ManagedDNS).NotTo(BeNil(), "managedDNS should be set on the AWS platform spec")

		// Wait for the CPO to create the ingress zones and mark managed DNS available.
		// HCP status.platform.aws.dnsZones and the condition are mirrored onto the
		// HostedCluster, so the management client is sufficient.
		g.Eventually(func(g Gomega) {
			latest := &hyperv1.HostedCluster{}
			g.Expect(mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), latest)).To(Succeed())

			cond := meta.FindStatusCondition(latest.Status.Conditions, string(hyperv1.AWSManagedDNSAvailable))
			g.Expect(cond).NotTo(BeNil(), "AWSManagedDNSAvailable condition should be present")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
				"AWSManagedDNSAvailable should be True, got reason %q message %q", cond.Reason, cond.Message)
			g.Expect(cond.Reason).To(Equal(hyperv1.AWSManagedDNSSuccessReason))

			g.Expect(latest.Status.Platform).NotTo(BeNil(), "platform status should be set")
			g.Expect(latest.Status.Platform.AWS).NotTo(BeNil(), "AWS platform status should be set")

			zones := latest.Status.Platform.AWS.DNSZones
			g.Expect(zones).NotTo(BeEmpty(), "status.platform.aws.dnsZones should be populated")

			byType := map[hyperv1.AWSDNSZoneType]hyperv1.AWSDNSZoneStatus{}
			for _, z := range zones {
				g.Expect(z.ZoneID).NotTo(BeEmpty(), "zone %q should report a ZoneID", z.Name)
				g.Expect(z.Name).NotTo(BeEmpty(), "zone %q should report a name", z.ZoneID)
				byType[z.ZoneType] = z
			}
			// A standard (non-shared-VPC) cluster gets both a public and a private zone.
			g.Expect(byType).To(HaveKey(hyperv1.PublicIngressZone), "a public ingress zone should be created")
			g.Expect(byType).To(HaveKey(hyperv1.PrivateIngressZone), "a private ingress zone should be created")
		}).WithContext(ctx).WithTimeout(15 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())

		t.Log("managed ingress DNS zones created and AWSManagedDNSAvailable is True")
		// Zone cleanup on deletion is exercised by the framework's cluster teardown.
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "managed-dns", globalOpts.ServiceAccountSigningKey)
}
