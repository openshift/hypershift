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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// imageMirroringSetupOrSkip skips when the HC has no imageContentSources or the
// mirror fixture Route does not exist. Once both are present, the setup must be
// valid: malformed, duplicate, or non-fixture mappings fail the spec, as do API
// errors. It also returns the fixture Route host. The decision logic lives in
// imageMirroringLoadSetup; this wrapper only asserts and skips.
func imageMirroringSetupOrSkip(g Gomega, tc *internal.TestContext) (*hyperv1.HostedCluster, []hyperv1.ImageContentSource, string) {
	GinkgoHelper()
	g.Expect(tc).NotTo(BeNil(), "test context must be initialized")
	hc, err := tc.GetHostedCluster()
	g.Expect(err).NotTo(HaveOccurred(), "read HC %s/%s", tc.ClusterNamespace, tc.ClusterName)
	g.Expect(hc).NotTo(BeNil(), "HC %s/%s", tc.ClusterNamespace, tc.ClusterName)
	configured, expected, routeHost, err := imageMirroringLoadSetup(tc.Context, tc.MgmtClient, hc)
	g.Expect(err).NotTo(HaveOccurred(), "HC %s/%s image-mirroring setup", hc.Namespace, hc.Name)
	if !configured {
		Skip("image mirroring is not configured on this hosted cluster")
	}
	return hc, expected, routeHost
}

// RegisterHostedClusterImageMirroringTests registers independent lifecycle
// checks for the HCCO IDMS and CPO ignition propagation paths and for workload
// pulls through the mirror on data plane nodes. They never mutate the HC; the
// workload check only creates and deletes its own namespace and pods.
func RegisterHostedClusterImageMirroringTests(getTestCtx internal.TestContextGetter) {
	It("should propagate imageContentSources to the hosted cluster IDMS with the worker role", func() {
		tc := getTestCtx()
		hc, expected, _ := imageMirroringSetupOrSkip(Default, tc)

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
		hc, expected, _ := imageMirroringSetupOrSkip(Default, tc)
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
	It("should pull a workload image by digest through the mirror when the source is unreachable", func() {
		tc := getTestCtx()
		hc, expected, routeHost := imageMirroringSetupOrSkip(Default, tc)

		// Expectations come from the HC spec and the pinned input image, never
		// from the fixture result.
		mirrorHost, mirrorRepository, err := imageMirroringVirtualMirror(expected, routeHost)
		Expect(err).NotTo(HaveOccurred(), "HC %s/%s virtual source mirror", hc.Namespace, hc.Name)
		digestRef, listDigest, err := imageMirroringDigestRef(imageMirroringVirtualSource, mirrorregistry.DefaultRegistryImage)
		Expect(err).NotTo(HaveOccurred(), "build by-digest reference")

		// CRI-O reports the per-platform manifest digest for a manifest-list
		// pull, so read the list from the mirror and verify it by content.
		caPEM, err := imageMirroringTrustBundle(tc.Context, tc.MgmtClient, hc)
		Expect(err).NotTo(HaveOccurred(), "HC %s/%s trust bundle", hc.Namespace, hc.Name)
		httpClient, err := imageMirroringHTTPClient(caPEM)
		Expect(err).NotTo(HaveOccurred(), "HC %s/%s trust bundle client", hc.Namespace, hc.Name)
		manifestList, err := imageMirroringFetchManifest(tc.Context, httpClient, mirrorHost, mirrorRepository, listDigest)
		Expect(err).NotTo(HaveOccurred(), "read manifest list %s from mirror %s/%s", listDigest, mirrorHost, mirrorRepository)

		hostedClient, err := tc.GetHostedClusterClientViaPortForward(hc)
		Expect(err).NotTo(HaveOccurred(), "open hosted client for HC %s/%s", hc.Namespace, hc.Name)
		DeferCleanup(hostedClient.Close)

		nodes := &corev1.NodeList{}
		Expect(hostedClient.List(tc.Context, nodes)).To(Succeed(), "list nodes of HC %s/%s", hc.Namespace, hc.Name)
		Expect(imageMirroringReadyLinuxNodes(nodes.Items)).To(BeNumerically(">", 0), "HC %s/%s must have a Ready linux node", hc.Namespace, hc.Name)

		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "e2e-image-mirroring-"}}
		Expect(hostedClient.Create(tc.Context, namespace)).To(Succeed(), "create workload namespace in HC %s/%s", hc.Namespace, hc.Name)
		DeferCleanup(func(ctx context.Context) {
			if err := hostedClient.Delete(ctx, namespace); err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: delete namespace %s", namespace.Name)
			}
		})

		byDigest := imageMirroringWorkloadPod(namespace.Name, "by-digest", digestRef)
		Expect(hostedClient.Create(tc.Context, byDigest)).To(Succeed(), "create pod %s/%s", byDigest.Namespace, byDigest.Name)
		DeferCleanup(func(ctx context.Context) {
			if err := hostedClient.Delete(ctx, byDigest); err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: delete pod %s/%s", byDigest.Namespace, byDigest.Name)
			}
		})

		// GET errors and unfinished states keep polling; a check error (a failure
		// that retries cannot fix) stops at once.
		waitObjective := fmt.Sprintf("pod %s/%s (%s) to pull through the mirror and succeed", byDigest.Namespace, byDigest.Name, digestRef)
		Eventually(func(g Gomega, ctx context.Context) {
			current := &corev1.Pod{}
			g.Expect(hostedClient.Get(ctx, crclient.ObjectKeyFromObject(byDigest), current)).To(Succeed(), "get pod %s/%s", byDigest.Namespace, byDigest.Name)
			done, reason, err := imageMirroringCheckPulledPod(current)
			if err != nil {
				StopTrying(waitObjective).Wrap(err).Now()
			}
			g.Expect(done).To(BeTrue(), reason)
		}).WithContext(tc.Context).WithTimeout(10*time.Minute).WithPolling(5*time.Second).Should(Succeed(), waitObjective)

		pulled := &corev1.Pod{}
		Expect(hostedClient.Get(tc.Context, crclient.ObjectKeyFromObject(byDigest), pulled)).To(Succeed(), "read pod %s/%s", byDigest.Namespace, byDigest.Name)
		Expect(pulled.Spec.NodeName).NotTo(BeEmpty(), "pod %s/%s must be bound to a node", pulled.Namespace, pulled.Name)
		node := &corev1.Node{}
		Expect(hostedClient.Get(tc.Context, crclient.ObjectKey{Name: pulled.Spec.NodeName}, node)).To(Succeed(), "read node %s", pulled.Spec.NodeName)
		platformDigest, err := imageMirroringPlatformDigest(manifestList, listDigest, node.Status.NodeInfo.OperatingSystem, node.Status.NodeInfo.Architecture)
		Expect(err).NotTo(HaveOccurred(), "select the manifest for node %s", node.Name)
		Expect(imageMirroringCheckImageID(pulled, []string{platformDigest, listDigest})).To(Succeed())
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ImageMirroring] Hosted Cluster Image Mirroring", Label("lifecycle", "image-mirroring", internal.InformingLabel), func() {
	RegisterHostedClusterImageMirroringTests(internal.GetTestContext)
})
