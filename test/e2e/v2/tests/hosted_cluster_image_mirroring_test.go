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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// imageMirroringSetupOrSkip skips when the HC has no imageContentSources or the
// mirror fixture Route does not exist. Once both are present, the setup must be
// valid: malformed, duplicate, or non-fixture mappings fail the spec. It also
// returns the fixture Route host.
func imageMirroringSetupOrSkip(tc *internal.TestContext) (*hyperv1.HostedCluster, []hyperv1.ImageContentSource, string) {
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
	return hc, expected, route.Spec.Host
}

// RegisterHostedClusterImageMirroringTests registers independent lifecycle
// checks for the HCCO IDMS and CPO ignition propagation paths and for workload
// pulls through the mirror on data plane nodes. They never mutate the HC; the
// workload check only creates and deletes its own namespace and pods.
func RegisterHostedClusterImageMirroringTests(getTestCtx internal.TestContextGetter) {
	It("should propagate imageContentSources to the hosted cluster IDMS with the worker role", func() {
		tc := getTestCtx()
		hc, expected, _ := imageMirroringSetupOrSkip(tc)

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
		hc, expected, _ := imageMirroringSetupOrSkip(tc)
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
		hc, expected, routeHost := imageMirroringSetupOrSkip(tc)

		// Expectations come from the HC spec and the pinned input image, never
		// from the fixture result.
		mirrorHost, mirrorRepository, err := imageMirroringVirtualMirror(expected, routeHost)
		Expect(err).NotTo(HaveOccurred(), "HC %s/%s virtual source mirror", hc.Namespace, hc.Name)
		digestRef, listDigest, err := imageMirroringDigestRef(imageMirroringVirtualSource, mirrorregistry.DefaultRegistryImage)
		Expect(err).NotTo(HaveOccurred(), "build by-digest reference")
		tagRef := imageMirroringVirtualSource + ":" + imageMirroringNegativeTag

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
		byTag := imageMirroringWorkloadPod(namespace.Name, "by-tag", tagRef)
		for _, pod := range []*corev1.Pod{byDigest, byTag} {
			Expect(hostedClient.Create(tc.Context, pod)).To(Succeed(), "create pod %s/%s", pod.Namespace, pod.Name)
			DeferCleanup(func(ctx context.Context) {
				if err := hostedClient.Delete(ctx, pod); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: delete pod %s/%s", pod.Namespace, pod.Name)
				}
			})
		}

		imageMirroringWaitPod(tc, hostedClient, byDigest, imageMirroringCheckPulledPod, 10*time.Minute,
			"pod %s/%s (%s) to pull through the mirror and succeed", byDigest.Namespace, byDigest.Name, digestRef)

		pulled := &corev1.Pod{}
		Expect(hostedClient.Get(tc.Context, crclient.ObjectKeyFromObject(byDigest), pulled)).To(Succeed(), "read pod %s/%s", byDigest.Namespace, byDigest.Name)
		Expect(pulled.Spec.NodeName).NotTo(BeEmpty(), "pod %s/%s must be bound to a node", pulled.Namespace, pulled.Name)
		node := &corev1.Node{}
		Expect(hostedClient.Get(tc.Context, crclient.ObjectKey{Name: pulled.Spec.NodeName}, node)).To(Succeed(), "read node %s", pulled.Spec.NodeName)
		platformDigest, err := imageMirroringPlatformDigest(manifestList, listDigest, node.Status.NodeInfo.OperatingSystem, node.Status.NodeInfo.Architecture)
		Expect(err).NotTo(HaveOccurred(), "select the manifest for node %s", node.Name)
		Expect(imageMirroringCheckImageID(pulled, []string{platformDigest, listDigest})).To(Succeed())

		// Negative control: IDMS redirects only by-digest pulls, so the same
		// unresolvable repository by tag must fail to pull.
		imageMirroringWaitPod(tc, hostedClient, byTag, imageMirroringCheckPullRejectedPod, 5*time.Minute,
			"pod %s/%s (%s) to fail pulling from the unreachable source", byTag.Namespace, byTag.Name, tagRef)
	})
}

// imageMirroringWaitPod polls pod until check is done. GET errors and
// unfinished states keep polling; a check error is terminal and stops at once.
// It uses Gomega so failures go through the informing-aware fail handler.
func imageMirroringWaitPod(tc *internal.TestContext, client crclient.Client, pod *corev1.Pod, check func(*corev1.Pod) (bool, string, error), timeout time.Duration, objective string, args ...any) {
	GinkgoHelper()
	description := fmt.Sprintf(objective, args...)
	Eventually(func(g Gomega, ctx context.Context) {
		current := &corev1.Pod{}
		g.Expect(client.Get(ctx, crclient.ObjectKeyFromObject(pod), current)).To(Succeed(), "get pod %s/%s", pod.Namespace, pod.Name)
		done, reason, err := check(current)
		if err != nil {
			StopTrying(description).Wrap(err).Now()
		}
		g.Expect(done).To(BeTrue(), reason)
	}).WithContext(tc.Context).WithTimeout(timeout).WithPolling(5*time.Second).Should(Succeed(), description)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ImageMirroring] Hosted Cluster Image Mirroring", Label("lifecycle", "image-mirroring", internal.InformingLabel), func() {
	RegisterHostedClusterImageMirroringTests(internal.GetTestContext)
})

// RegisterHostedClusterImageMirroringRemovalTests registers the day-2 check that
// removing imageContentSources empties the hosted IDMS, deletes the CPO
// ignition ConfigMap and replaces the nodes, which then no longer redirect
// by-digest pulls to the mirror. It mutates the HC and does not restore it, so
// it must run as the last step on its variant.
func RegisterHostedClusterImageMirroringRemovalTests(getTestCtx internal.TestContextGetter) {
	It("should clean up mirror configuration and roll out nodes when imageContentSources is removed", func() {
		const (
			ignitionDeleteTimeout  = 5 * time.Minute
			idmsEmptyTimeout       = 10 * time.Minute
			rolloutStartTimeout    = 10 * time.Minute
			rolloutFinishTimeout   = 30 * time.Minute
			nodesReplacedTimeout   = 5 * time.Minute
			pullRejectedTimeout    = 5 * time.Minute
			nodePoolPollInterval   = 15 * time.Second
			resourcePollInterval   = 5 * time.Second
			nodePoolStartPollShort = 10 * time.Second
		)
		tc := getTestCtx()
		hc, expected, _ := imageMirroringSetupOrSkip(tc)
		Expect(tc.ControlPlaneNamespace).NotTo(BeEmpty(), "HC %s/%s must have a control plane namespace", hc.Namespace, hc.Name)
		digestRef, _, err := imageMirroringDigestRef(imageMirroringVirtualSource, mirrorregistry.DefaultRegistryImage)
		Expect(err).NotTo(HaveOccurred(), "build by-digest reference")

		By("verifying the pre-removal baseline")
		configMaps, err := imageMirroringGetConfigMap(tc.Context, tc.MgmtClient, tc.ControlPlaneNamespace)
		Expect(err).NotTo(HaveOccurred(), "read ConfigMap %s/%s", tc.ControlPlaneNamespace, imageMirroringIgnitionName)
		done, reason, err := imageMirroringCheckConfigMap(configMaps, tc.ControlPlaneNamespace, expected)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue(), "baseline ConfigMap %s/%s: %s", tc.ControlPlaneNamespace, imageMirroringIgnitionName, reason)

		nodePoolList := &hyperv1.NodePoolList{}
		Expect(tc.MgmtClient.List(tc.Context, nodePoolList, crclient.InNamespace(hc.Namespace))).To(Succeed(), "list NodePools in %s", hc.Namespace)
		var nodePools []*hyperv1.NodePool
		for i := range nodePoolList.Items {
			if nodePoolList.Items[i].Spec.ClusterName == hc.Name {
				nodePools = append(nodePools, &nodePoolList.Items[i])
			}
		}
		Expect(nodePools).NotTo(BeEmpty(), "HC %s/%s must have at least one NodePool", hc.Namespace, hc.Name)
		for _, np := range nodePools {
			Expect(imageMirroringCheckNodePoolBaseline(np)).To(Succeed())
		}

		// The rollout below outlives a single port-forward tunnel comfortably, so
		// hosted clients are opened per phase and only the management cluster is
		// polled while nodes are replaced.
		baselineClient, err := tc.GetHostedClusterClientViaPortForward(hc)
		Expect(err).NotTo(HaveOccurred(), "open hosted client for HC %s/%s", hc.Namespace, hc.Name)
		DeferCleanup(baselineClient.Close)
		idms, err := imageMirroringGetIDMS(tc.Context, baselineClient)
		Expect(err).NotTo(HaveOccurred(), "read hosted ImageDigestMirrorSet/cluster of HC %s/%s", hc.Namespace, hc.Name)
		done, reason, err = imageMirroringCheckIDMS(idms, expected)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue(), "baseline hosted ImageDigestMirrorSet/cluster of HC %s/%s: %s", hc.Namespace, hc.Name, reason)
		baselineNodes := map[string]sets.Set[string]{}
		for _, np := range nodePools {
			nodes := &corev1.NodeList{}
			Expect(baselineClient.List(tc.Context, nodes, crclient.MatchingLabels{hyperv1.NodePoolLabel: np.Name})).To(Succeed(), "list nodes of NodePool %s/%s", np.Namespace, np.Name)
			names := imageMirroringPoolNodeNames(nodes.Items)
			Expect(names.Len()).To(Equal(int(*np.Spec.Replicas)), "NodePool %s/%s Ready nodes %v", np.Namespace, np.Name, sets.List(names))
			baselineNodes[np.Name] = names
		}

		By("removing imageContentSources from the HostedCluster")
		// Deliberately not restored: this spec runs as the last step of the
		// sequential image-mirroring lane, on a variant that no other test uses
		// and that is destroyed with the job. Restoring would only add a second
		// full NodePool rollout without covering anything new.
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current := &hyperv1.HostedCluster{}
			if err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hc), current); err != nil {
				return err
			}
			current.Spec.ImageContentSources = nil
			return tc.MgmtClient.Update(tc.Context, current)
		})).To(Succeed(), "remove imageContentSources from HC %s/%s", hc.Namespace, hc.Name)

		By("waiting for the CPO to delete the ignition ConfigMap")
		Eventually(func(g Gomega, ctx context.Context) {
			err := tc.MgmtClient.Get(ctx, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: imageMirroringIgnitionName}, &corev1.ConfigMap{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "ConfigMap %s/%s still readable (err: %v)", tc.ControlPlaneNamespace, imageMirroringIgnitionName, err)
		}).WithContext(tc.Context).WithTimeout(ignitionDeleteTimeout).WithPolling(resourcePollInterval).Should(Succeed(),
			"ConfigMap %s/%s to be deleted", tc.ControlPlaneNamespace, imageMirroringIgnitionName)

		By("waiting for the HCCO to empty the hosted IDMS")
		Eventually(func(g Gomega, ctx context.Context) {
			objects, err := imageMirroringGetIDMS(ctx, baselineClient)
			g.Expect(err).NotTo(HaveOccurred(), "get hosted ImageDigestMirrorSet/cluster")
			done, reason := imageMirroringCheckEmptyIDMS(objects)
			g.Expect(done).To(BeTrue(), reason)
		}).WithContext(tc.Context).WithTimeout(idmsEmptyTimeout).WithPolling(resourcePollInterval).Should(Succeed(),
			"HC %s/%s hosted ImageDigestMirrorSet/cluster to have no mappings", hc.Namespace, hc.Name)
		baselineClient.Close()

		// All NodePools start rolling out at once, so wait for every pool to
		// start before waiting for any pool to finish: otherwise a pool that
		// finishes while an earlier one is still rolling would never be seen
		// with UpdatingConfig=True.
		waitNodePool := func(np *hyperv1.NodePool, want map[string]corev1.ConditionStatus, timeout, polling time.Duration, objective string) {
			GinkgoHelper()
			Eventually(func(g Gomega, ctx context.Context) {
				current := &hyperv1.NodePool{}
				g.Expect(tc.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), current)).To(Succeed(), "get NodePool %s/%s", np.Namespace, np.Name)
				done, reason := imageMirroringCheckNodePoolConditions(current, want)
				g.Expect(done).To(BeTrue(), reason)
			}).WithContext(tc.Context).WithTimeout(timeout).WithPolling(polling).Should(Succeed(),
				"NodePool %s/%s to %s", np.Namespace, np.Name, objective)
		}
		By("waiting for every NodePool to start the rollout")
		for _, np := range nodePools {
			waitNodePool(np, map[string]corev1.ConditionStatus{
				hyperv1.NodePoolUpdatingConfigConditionType: corev1.ConditionTrue,
			}, rolloutStartTimeout, nodePoolStartPollShort, "begin updating config")
		}
		By("waiting for every NodePool to finish the rollout")
		for _, np := range nodePools {
			// One Replace node is one EC2 boot plus a drain; the shared AWS config
			// update helper allows 25 minutes, so 30 leaves headroom.
			waitNodePool(np, map[string]corev1.ConditionStatus{
				hyperv1.NodePoolUpdatingConfigConditionType:  corev1.ConditionFalse,
				hyperv1.NodePoolAllNodesHealthyConditionType: corev1.ConditionTrue,
			}, rolloutFinishTimeout, nodePoolPollInterval, "finish updating config with healthy nodes")
		}

		hostedClient, err := tc.GetHostedClusterClientViaPortForward(hc)
		Expect(err).NotTo(HaveOccurred(), "open hosted client for HC %s/%s", hc.Namespace, hc.Name)
		DeferCleanup(hostedClient.Close)

		By("verifying that every pre-removal node was replaced")
		for _, np := range nodePools {
			baseline := baselineNodes[np.Name]
			replicas := *np.Spec.Replicas
			Eventually(func(g Gomega, ctx context.Context) {
				list := &corev1.NodeList{}
				g.Expect(hostedClient.List(ctx, list, crclient.MatchingLabels{hyperv1.NodePoolLabel: np.Name})).To(Succeed(), "list nodes of NodePool %s/%s", np.Namespace, np.Name)
				nodes := make([]*corev1.Node, 0, len(list.Items))
				for i := range list.Items {
					nodes = append(nodes, &list.Items[i])
				}
				done, reason, err := imageMirroringNodesReplaced(baseline, nodes, replicas)
				if err != nil {
					StopTrying(fmt.Sprintf("check nodes of NodePool %s/%s", np.Namespace, np.Name)).Wrap(err).Now()
				}
				g.Expect(done).To(BeTrue(), reason)
			}).WithContext(tc.Context).WithTimeout(nodesReplacedTimeout).WithPolling(nodePoolStartPollShort).Should(Succeed(),
				"NodePool %s/%s nodes to replace pre-removal nodes %v", np.Namespace, np.Name, sets.List(baseline))
		}

		By("verifying that new nodes no longer redirect by-digest pulls to the mirror")
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "e2e-image-mirroring-removal-"}}
		Expect(hostedClient.Create(tc.Context, namespace)).To(Succeed(), "create workload namespace in HC %s/%s", hc.Namespace, hc.Name)
		DeferCleanup(func(ctx context.Context) {
			if err := hostedClient.Delete(ctx, namespace); err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: delete namespace %s", namespace.Name)
			}
		})
		pod := imageMirroringWorkloadPod(namespace.Name, "by-digest-after-removal", digestRef)
		Expect(hostedClient.Create(tc.Context, pod)).To(Succeed(), "create pod %s/%s", pod.Namespace, pod.Name)
		DeferCleanup(func(ctx context.Context) {
			if err := hostedClient.Delete(ctx, pod); err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: delete pod %s/%s", pod.Namespace, pod.Name)
			}
		})
		imageMirroringWaitPod(tc, hostedClient, pod, imageMirroringCheckRemovedPullRejectedPod, pullRejectedTimeout,
			"pod %s/%s (%s) to fail pulling after imageContentSources removal", pod.Namespace, pod.Name, digestRef)
	})
}

// The removal mutates the HC, so it has its own Describe and label: inherited
// labels would otherwise select it together with the read-only specs above.
var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ImageMirroring] Hosted Cluster Image Mirroring Removal", Label("lifecycle", "image-mirroring-removal", internal.InformingLabel), func() {
	RegisterHostedClusterImageMirroringRemovalTests(internal.GetTestContext)
})
