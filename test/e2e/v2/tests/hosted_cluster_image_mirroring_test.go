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
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"

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
		Expect(imageMirroringCheckImageID(pulled, []string{platformDigest, listDigest})).To(Succeed(),
			"pod %s/%s on node %s must report one of the mirror digests %v", pulled.Namespace, pulled.Name, node.Name, []string{platformDigest, listDigest})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ImageMirroring] Hosted Cluster Image Mirroring", Label("lifecycle", "image-mirroring", internal.InformingLabel), func() {
	RegisterHostedClusterImageMirroringTests(internal.GetTestContext)
})

// RegisterHostedClusterImageMirroringRemovalTests registers the day-2 check that
// removing imageContentSources empties the hosted IDMS, deletes the CPO
// ignition ConfigMap and replaces the nodes. Every replacement node must then
// fail a by-digest pull from the mirror-only source while a direct pull of the
// same digest from the mirror succeeds on that node. It mutates the HC and does
// not restore it, so it must run as the last step on its variant.
func RegisterHostedClusterImageMirroringRemovalTests(getTestCtx internal.TestContextGetter) {
	It("should clean up mirror configuration and roll out nodes when imageContentSources is removed", func() {
		const (
			ignitionDeleteTimeout = 5 * time.Minute
			idmsEmptyTimeout      = 10 * time.Minute
			rolloutTimeout        = 30 * time.Minute
			nodesReplacedTimeout  = 5 * time.Minute
			pullRejectedTimeout   = 5 * time.Minute
			mirrorPullTimeout     = 10 * time.Minute
			nodePoolPollInterval  = 15 * time.Second
			resourcePollInterval  = 5 * time.Second
			nodePollInterval      = 10 * time.Second
		)
		tc := getTestCtx()
		hc, expected, routeHost := imageMirroringSetupOrSkip(Default, tc)
		Expect(tc.ControlPlaneNamespace).NotTo(BeEmpty(), "HC %s/%s must have a control plane namespace", hc.Namespace, hc.Name)
		digestRef, _, err := imageMirroringDigestRef(imageMirroringVirtualSource, mirrorregistry.DefaultRegistryImage)
		Expect(err).NotTo(HaveOccurred(), "build by-digest reference")
		// The direct mirror reference comes from the HC spec, captured before the
		// removal, and the pinned input image, never from the fixture result.
		mirrorHost, mirrorRepository, err := imageMirroringVirtualMirror(expected, routeHost)
		Expect(err).NotTo(HaveOccurred(), "HC %s/%s virtual source mirror", hc.Namespace, hc.Name)
		mirrorRef, _, err := imageMirroringDigestRef(mirrorHost+"/"+mirrorRepository, mirrorregistry.DefaultRegistryImage)
		Expect(err).NotTo(HaveOccurred(), "build direct mirror by-digest reference")

		By("verifying the pre-removal baseline")
		configMaps, err := imageMirroringGetConfigMap(tc.Context, tc.MgmtClient, tc.ControlPlaneNamespace)
		Expect(err).NotTo(HaveOccurred(), "read ConfigMap %s/%s", tc.ControlPlaneNamespace, imageMirroringIgnitionName)
		done, reason, err := imageMirroringCheckConfigMap(configMaps, tc.ControlPlaneNamespace, expected)
		Expect(err).NotTo(HaveOccurred(), "HC %s/%s baseline ConfigMap %s/%s", hc.Namespace, hc.Name, tc.ControlPlaneNamespace, imageMirroringIgnitionName)
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
		baselineConfigs := map[string]string{}
		for _, np := range nodePools {
			Expect(imageMirroringCheckNodePoolBaseline(np)).To(Succeed(), "HC %s/%s baseline NodePool %s/%s", hc.Namespace, hc.Name, np.Namespace, np.Name)
			baselineConfigs[np.Name] = np.Annotations[imageMirroringNodePoolCurrentConfigAnnotation]
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
		Expect(err).NotTo(HaveOccurred(), "HC %s/%s baseline hosted ImageDigestMirrorSet/cluster", hc.Namespace, hc.Name)
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

		// The rollout may already be running, or even finished, by now, so the
		// check relies on durable completion evidence (the current config
		// annotation moving away from the baseline) rather than on observing
		// UpdatingConfig=True. That also makes waiting on the pools one by one
		// safe. The node replacement check below ties the rollout to new nodes.
		By("waiting for every NodePool to complete the config rollout")
		for _, np := range nodePools {
			baselineConfig := baselineConfigs[np.Name]
			// One Replace node is one EC2 boot plus a drain; the shared AWS config
			// update helper allows 25 minutes, so 30 leaves headroom.
			Eventually(func(g Gomega, ctx context.Context) {
				current := &hyperv1.NodePool{}
				g.Expect(tc.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(np), current)).To(Succeed(), "get NodePool %s/%s", np.Namespace, np.Name)
				done, reason, err := imageMirroringCheckNodePoolRolledOut(current, baselineConfig)
				if err != nil {
					StopTrying(fmt.Sprintf("check rollout of NodePool %s/%s", np.Namespace, np.Name)).Wrap(err).Now()
				}
				g.Expect(done).To(BeTrue(), reason)
			}).WithContext(tc.Context).WithTimeout(rolloutTimeout).WithPolling(nodePoolPollInterval).Should(Succeed(),
				"NodePool %s/%s to complete a config rollout with healthy nodes", np.Namespace, np.Name)
		}

		hostedClient, err := tc.GetHostedClusterClientViaPortForward(hc)
		Expect(err).NotTo(HaveOccurred(), "open hosted client for HC %s/%s", hc.Namespace, hc.Name)
		DeferCleanup(hostedClient.Close)

		By("verifying that every pre-removal node was replaced")
		var replacementNodes []string
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
			}).WithContext(tc.Context).WithTimeout(nodesReplacedTimeout).WithPolling(nodePollInterval).Should(Succeed(),
				"NodePool %s/%s nodes to replace pre-removal nodes %v", np.Namespace, np.Name, sets.List(baseline))
			list := &corev1.NodeList{}
			Expect(hostedClient.List(tc.Context, list, crclient.MatchingLabels{hyperv1.NodePoolLabel: np.Name})).To(Succeed(), "list nodes of NodePool %s/%s", np.Namespace, np.Name)
			names := imageMirroringPoolNodeNames(list.Items)
			Expect(names.HasAny(sets.List(baseline)...)).To(BeFalse(), "NodePool %s/%s nodes %v must not include pre-removal nodes %v", np.Namespace, np.Name, sets.List(names), sets.List(baseline))
			replacementNodes = append(replacementNodes, sets.List(names)...)
		}
		Expect(replacementNodes).NotTo(BeEmpty(), "HC %s/%s must have replacement nodes", hc.Namespace, hc.Name)

		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "e2e-image-mirroring-removal-"}}
		Expect(hostedClient.Create(tc.Context, namespace)).To(Succeed(), "create workload namespace in HC %s/%s", hc.Namespace, hc.Name)
		DeferCleanup(func(ctx context.Context) {
			if err := hostedClient.Delete(ctx, namespace); err != nil && !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred(), "cleanup: delete namespace %s", namespace.Name)
			}
		})
		// createPinnedPod creates a workload pod bound to node, bypassing the
		// scheduler, so that each probe exercises that node's registry config.
		createPinnedPod := func(name, image, node string) *corev1.Pod {
			pod := imageMirroringWorkloadPod(namespace.Name, name, image)
			pod.Spec.NodeName = node
			Expect(hostedClient.Create(tc.Context, pod)).To(Succeed(), "create pod %s/%s on node %s", pod.Namespace, pod.Name, node)
			DeferCleanup(func(ctx context.Context) {
				if err := hostedClient.Delete(ctx, pod); err != nil && !apierrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred(), "cleanup: delete pod %s/%s", pod.Namespace, pod.Name)
				}
			})
			return pod
		}

		// A pull failure alone could also come from an unreachable mirror behind
		// a stale redirect, so each replacement node gets a pair of probes: the
		// mirror-only source must fail, then a direct pull of the same digest
		// from the mirror must succeed on the same node. The source probe runs
		// first, so the direct pull is a real pull on a node that has never
		// obtained the image (the node was created after the removal).
		By("verifying that every replacement node no longer redirects by-digest pulls to the mirror")
		sourcePods := make([]*corev1.Pod, len(replacementNodes))
		for i, node := range replacementNodes {
			sourcePods[i] = createPinnedPod(fmt.Sprintf("source-by-digest-%d", i), digestRef, node)
		}
		for i, pod := range sourcePods {
			// GET errors and unfinished states keep polling; a check error (the
			// image was obtained after all) stops at once.
			waitObjective := fmt.Sprintf("pod %s/%s (%s) on node %s to fail pulling after imageContentSources removal", pod.Namespace, pod.Name, digestRef, replacementNodes[i])
			Eventually(func(g Gomega, ctx context.Context) {
				current := &corev1.Pod{}
				g.Expect(hostedClient.Get(ctx, crclient.ObjectKeyFromObject(pod), current)).To(Succeed(), "get pod %s/%s", pod.Namespace, pod.Name)
				done, reason, err := imageMirroringCheckRemovedPullRejectedPod(current)
				if err != nil {
					StopTrying(waitObjective).Wrap(err).Now()
				}
				g.Expect(done).To(BeTrue(), reason)
			}).WithContext(tc.Context).WithTimeout(pullRejectedTimeout).WithPolling(resourcePollInterval).Should(Succeed(), waitObjective)
		}

		By("verifying that the mirror still serves the digest to every replacement node")
		mirrorPods := make([]*corev1.Pod, len(replacementNodes))
		for i, node := range replacementNodes {
			mirrorPods[i] = createPinnedPod(fmt.Sprintf("mirror-by-digest-%d", i), mirrorRef, node)
		}
		for i, pod := range mirrorPods {
			// GET errors and unfinished states keep polling; a check error (a
			// failure that retries cannot fix) stops at once.
			waitObjective := fmt.Sprintf("pod %s/%s (%s) on node %s to pull directly from the mirror and succeed", pod.Namespace, pod.Name, mirrorRef, replacementNodes[i])
			Eventually(func(g Gomega, ctx context.Context) {
				current := &corev1.Pod{}
				g.Expect(hostedClient.Get(ctx, crclient.ObjectKeyFromObject(pod), current)).To(Succeed(), "get pod %s/%s", pod.Namespace, pod.Name)
				done, reason, err := imageMirroringCheckPulledPod(current)
				if err != nil {
					StopTrying(waitObjective).Wrap(err).Now()
				}
				g.Expect(done).To(BeTrue(), reason)
			}).WithContext(tc.Context).WithTimeout(mirrorPullTimeout).WithPolling(resourcePollInterval).Should(Succeed(), waitObjective)
		}
	})
}

// The removal mutates the HC, so it has its own Describe and label: inherited
// labels would otherwise select it together with the read-only specs above.
var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ImageMirroring] Hosted Cluster Image Mirroring Removal", Label("lifecycle", "image-mirroring-removal", internal.InformingLabel), func() {
	RegisterHostedClusterImageMirroringRemovalTests(internal.GetTestContext)
})
