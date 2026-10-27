//go:build e2ev2

package tests

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	"github.com/openshift/hypershift/test/e2e/v2/util/mirrorregistry"

	routev1 "github.com/openshift/api/route/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// imageMirroringSetupOrSkip skips when the HC has no imageContentSources or the
// mirror fixture Route does not exist. Once both are present, the setup must be
// valid: malformed, duplicate, or non-fixture mappings fail the spec.
func imageMirroringSetupOrSkip(tc *internal.TestContext) (*hyperv1.HostedCluster, []hyperv1.ImageContentSource) {
	GinkgoHelper()
	Expect(tc).NotTo(BeNil(), "test context must be initialized")
	hc, err := tc.GetHostedCluster()
	Expect(err).NotTo(HaveOccurred(), "read HC %s/%s", tc.ClusterNamespace, tc.ClusterName)
	route := &routev1.Route{}
	err = tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: mirrorregistry.DefaultNamespace, Name: mirrorregistry.DefaultName}, route)
	if err != nil && !apierrors.IsNotFound(err) {
		Expect(err).NotTo(HaveOccurred(), "read mirror fixture Route %s/%s", mirrorregistry.DefaultNamespace, mirrorregistry.DefaultName)
	}
	if !imageMirroringConfigured(hc, err == nil) {
		Skip("image mirroring is not configured on this hosted cluster")
	}
	expected, err := imageMirroringExpected(hc, route)
	Expect(err).NotTo(HaveOccurred(), "HC %s/%s image-mirroring setup", hc.Namespace, hc.Name)
	return hc, expected
}

// RegisterHostedClusterImageMirroringTests registers independent, read-only
// lifecycle checks for the HCCO IDMS and CPO ignition propagation paths.
func RegisterHostedClusterImageMirroringTests(getTestCtx internal.TestContextGetter) {
	It("should propagate imageContentSources to the hosted cluster IDMS with the worker role", func() {
		tc := getTestCtx()
		hc, expected := imageMirroringSetupOrSkip(tc)

		hostedClient, err := tc.GetHostedClusterClientViaPortForward(hc)
		Expect(err).NotTo(HaveOccurred(), "open hosted client for HC %s/%s", hc.Namespace, hc.Name)
		DeferCleanup(hostedClient.Close)
		Eventually(func(g Gomega, ctx context.Context) {
			objects, err := imageMirroringGetIDMS(ctx, hostedClient)
			g.Expect(err).NotTo(HaveOccurred(), "get hosted ImageDigestMirrorSet/cluster")
			done, reason, err := imageMirroringCheckIDMS(objects, expected)
			if err != nil {
				StopTrying("check hosted ImageDigestMirrorSet/cluster").Wrap(err).Now()
			}
			g.Expect(done).To(BeTrue(), reason)
		}).WithContext(tc.Context).WithTimeout(10*time.Minute).WithPolling(3*time.Second).Should(Succeed(),
			"HC %s/%s hosted ImageDigestMirrorSet/cluster mappings and worker role", hc.Namespace, hc.Name)
	})

	It("should propagate imageContentSources independently to the control plane ignition ConfigMap", func() {
		tc := getTestCtx()
		hc, expected := imageMirroringSetupOrSkip(tc)
		Expect(tc.ControlPlaneNamespace).NotTo(BeEmpty(), "HC %s/%s must have a control plane namespace", hc.Namespace, hc.Name)

		Eventually(func(g Gomega, ctx context.Context) {
			objects, err := imageMirroringGetConfigMap(ctx, tc.MgmtClient, tc.ControlPlaneNamespace)
			g.Expect(err).NotTo(HaveOccurred(), "get ConfigMap %s/%s", tc.ControlPlaneNamespace, imageMirroringIgnitionName)
			done, reason, err := imageMirroringCheckConfigMap(objects, tc.ControlPlaneNamespace, expected)
			if err != nil {
				StopTrying(fmt.Sprintf("check ConfigMap %s/%s", tc.ControlPlaneNamespace, imageMirroringIgnitionName)).Wrap(err).Now()
			}
			g.Expect(done).To(BeTrue(), reason)
		}).WithContext(tc.Context).WithTimeout(10*time.Minute).WithPolling(3*time.Second).Should(Succeed(),
			"HC %s/%s ConfigMap %s/%s decoded IDMS mappings and labels", hc.Namespace, hc.Name, tc.ControlPlaneNamespace, imageMirroringIgnitionName)
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ImageMirroring] Hosted Cluster Image Mirroring", Label("lifecycle", "image-mirroring", internal.InformingLabel), func() {
	RegisterHostedClusterImageMirroringTests(internal.GetTestContext)
})
