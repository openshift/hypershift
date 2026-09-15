//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// managedDNSHostedCluster returns the HostedCluster and its status.platform.aws.dnsZones
// keyed by zone type. It skips the test when managed ingress DNS is not enabled on the
// cluster, so the test only runs on the managed-dns AWS variant (created with
// TechPreviewNoUpgrade, where the AWSManagedDNS feature gate lives).
func managedDNSHostedCluster(tc *internal.TestContext) (*hyperv1.HostedCluster, map[hyperv1.AWSDNSZoneType]hyperv1.AWSDNSZoneStatus) {
	GinkgoHelper()
	hc, err := tc.GetHostedCluster()
	Expect(err).NotTo(HaveOccurred())
	if hc.Spec.Platform.AWS == nil || hc.Spec.Platform.AWS.ManagedDNS.IngressDomainPrefix == "" {
		Skip("managed ingress DNS is not enabled on this hosted cluster")
	}

	Expect(hc.Status.Platform).NotTo(BeNil(), "platform status should be set")
	Expect(hc.Status.Platform.AWS).NotTo(BeNil(), "AWS platform status should be set")
	zones := hc.Status.Platform.AWS.DNSZones
	Expect(zones).NotTo(BeEmpty(), "status.platform.aws.dnsZones should be populated")

	byType := map[hyperv1.AWSDNSZoneType]hyperv1.AWSDNSZoneStatus{}
	for _, z := range zones {
		Expect(z.ZoneID).NotTo(BeEmpty(), "zone %q should report a ZoneID", z.Name)
		Expect(z.Name).NotTo(BeEmpty(), "zone %q should report a name", z.ZoneID)
		byType[z.ZoneType] = z
	}
	return hc, byType
}

// AWSManagedDNSTest validates CPO-managed Route53 ingress DNS on AWS.
//
// The test runs against a cluster created with spec.platform.aws.managedDNS and
// no NS delegation. In that mode the CPO only creates the public and private
// Route53 ingress zones and reports them ready, so AWSManagedDNSAvailable reaches
// True deterministically without depending on a controllable parent zone or live
// NS resolution. Delegation (ExternalDNS/Manual) is out of scope because verifying
// it requires a parent zone and propagated NS records, which are not deterministic
// in e2e.
//
// The managed-dns AWS variant provisions this cluster with TechPreviewNoUpgrade,
// which is where the AWSManagedDNS feature gate lives. On any other cluster the
// managedDNS field is absent and the tests skip.
func AWSManagedDNSTest(getTestCtx internal.TestContextGetter) {
	Context("AWS Managed Ingress DNS", Label("AWS"), func() {
		BeforeEach(func() {
			getTestCtx().SkipIfNotPlatform(hyperv1.AWSPlatform)
		})

		It("should report AWSManagedDNSAvailable True with public and private ingress zones", func() {
			tc := getTestCtx()
			hc, byType := managedDNSHostedCluster(tc)

			// HCP status.platform.aws.dnsZones and the AWSManagedDNSAvailable
			// condition are mirrored onto the HostedCluster, so reading the HC is
			// sufficient.
			cond := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.AWSManagedDNSAvailable))
			Expect(cond).NotTo(BeNil(), "AWSManagedDNSAvailable condition should be present")
			Expect(cond.Status).To(Equal(metav1.ConditionTrue),
				"AWSManagedDNSAvailable should be True, got reason %q message %q", cond.Reason, cond.Message)
			Expect(cond.Reason).To(Equal(hyperv1.AWSManagedDNSSuccessReason))

			// A standard (non-shared-VPC) cluster gets both a public and a private zone.
			Expect(byType).To(HaveKey(hyperv1.PublicIngressZone), "a public ingress zone should be created")
			Expect(byType).To(HaveKey(hyperv1.PrivateIngressZone), "a private ingress zone should be created")
		})

		It("should point the guest dns.config public and private zones at the managed zones", func() {
			tc := getTestCtx()
			hc, byType := managedDNSHostedCluster(tc)

			hcClient, err := tc.GetHostedClusterClient(hc)
			Expect(err).NotTo(HaveOccurred(), "failed to build hosted cluster client")

			dnsConfig := &configv1.DNS{}
			Expect(hcClient.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, dnsConfig)).To(Succeed(),
				"failed to get guest dns.config.openshift.io/cluster")

			// The CPO overrides the guest dns config zones so the ingress operator
			// creates the wildcard *.apps records in the managed zones. Compare against
			// the extracted zone IDs, not hardcoded values, to verify the wiring.
			Expect(dnsConfig.Spec.PublicZone).NotTo(BeNil(), "dns.config public zone should be set")
			Expect(dnsConfig.Spec.PublicZone.ID).To(Equal(byType[hyperv1.PublicIngressZone].ZoneID),
				"dns.config public zone should point at the managed public ingress zone")

			Expect(dnsConfig.Spec.PrivateZone).NotTo(BeNil(), "dns.config private zone should be set")
			Expect(dnsConfig.Spec.PrivateZone.ID).To(Equal(byType[hyperv1.PrivateIngressZone].ZoneID),
				"dns.config private zone should point at the managed private ingress zone")
		})
	})
}

// RegisterHostedClusterManagedDNSTests registers all managed ingress DNS tests.
func RegisterHostedClusterManagedDNSTests(getTestCtx internal.TestContextGetter) {
	AWSManagedDNSTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:AWSManagedDNS] Hosted Cluster Managed DNS", Label("lifecycle", "hosted-cluster-managed-dns"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterHostedClusterManagedDNSTests(func() *internal.TestContext { return testCtx })
})
