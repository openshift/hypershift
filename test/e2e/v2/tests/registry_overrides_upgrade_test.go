//go:build e2ev2

package tests

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	"github.com/openshift/hypershift/test/e2e/v2/util/mirrorregistry"

	routev1 "github.com/openshift/api/route/v1"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// registryOverridesUpgradeTimeout bounds each wait of these specs. They
	// run after the upgrade has completed, so they only wait for rollouts that
	// are already finishing.
	registryOverridesUpgradeTimeout = 15 * time.Minute
)

// RegisterRegistryOverridesUpgradeTests registers the checks of a HostedCluster
// upgrade under the HyperShift operator --registry-overrides. They run in the
// registry overrides job, on the cluster the control plane upgrade spec has
// just upgraded, after the registry overrides specs have mirrored both
// releases and patched the operator. They read that state; they don't set it
// up. Elsewhere (no job CA) they skip. Once the job CA is present, missing
// state means an earlier step failed, and the specs fail instead of skipping.
func RegisterRegistryOverridesUpgradeTests(getTestCtx internal.TestContextGetter) {
	var (
		tc          *internal.TestContext
		hc          *hyperv1.HostedCluster
		ca          *mirrorregistry.KeyPair
		overrides   map[string]string
		hoImage     string
		mirrorHost  string
		targetImage string
	)

	BeforeAll(func() {
		tc = getTestCtx()
		Expect(tc).NotTo(BeNil(), "test context must be initialized")
		var err error
		ca, err = registryOverridesLoadCA(
			internal.GetEnvVarValue("E2E_REGISTRY_OVERRIDES_CA_CERT_FILE"),
			internal.GetEnvVarValue("E2E_REGISTRY_OVERRIDES_CA_KEY_FILE"))
		Expect(err).NotTo(HaveOccurred(), "registry overrides mirror CA")
		if ca == nil {
			Skip("the job provides no registry overrides mirror CA, so the management cluster does not trust a mirror")
		}
		targetImage = internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE")
		Expect(targetImage).NotTo(BeEmpty(), "E2E_LATEST_RELEASE_IMAGE must be set: it is the upgrade target")
		hc, err = tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred(), "read HC %s/%s", tc.ClusterNamespace, tc.ClusterName)
		Expect(hc).NotTo(BeNil(), "HC %s/%s", tc.ClusterNamespace, tc.ClusterName)
		Expect(tc.ControlPlaneNamespace).NotTo(BeEmpty(), "HC %s/%s must have a control plane namespace", hc.Namespace, hc.Name)

		By("reading the mirror and the HyperShift operator overrides set up by the registry overrides specs")
		route := &routev1.Route{}
		routeKey := crclient.ObjectKey{Namespace: registryOverridesNamespace, Name: mirrorregistry.DefaultName}
		Expect(tc.MgmtClient.Get(tc.Context, routeKey, route)).To(Succeed(), "read mirror Route %s: the registry overrides specs must run first", routeKey)
		mirrorHost = route.Spec.Host
		deployment := &appsv1.Deployment{}
		hoKey := crclient.ObjectKey{Namespace: registryOverridesHONamespace, Name: registryOverridesHOName}
		Expect(tc.MgmtClient.Get(tc.Context, hoKey, deployment)).To(Succeed(), "read HyperShift operator Deployment %s", hoKey)
		container, err := registryOverridesOperatorContainer(deployment)
		Expect(err).NotTo(HaveOccurred(), "HyperShift operator Deployment %s", hoKey)
		value, _, err := registryOverridesArgValue(container.Args)
		Expect(err).NotTo(HaveOccurred(), "HyperShift operator Deployment %s %s", hoKey, registryOverridesFlag)
		overrides, err = registryOverridesParseArg(value)
		Expect(err).NotTo(HaveOccurred(), "HyperShift operator Deployment %s %s", hoKey, registryOverridesFlag)
		Expect(registryOverridesApplied(overrides, mirrorHost)).To(BeTrue(),
			"HyperShift operator Deployment %s %s=%q must point every override at the mirror %s", hoKey, registryOverridesFlag, value, mirrorHost)
		hoImage = container.Image
	})

	It("should complete the upgrade to the latest release under the operator overrides", func() {
		Eventually(func(g Gomega) {
			ExpectHostedClusterUpgradeComplete(tc.Context, g, tc.MgmtClient, hc, targetImage)
		}).WithContext(tc.Context).WithTimeout(registryOverridesUpgradeTimeout).WithPolling(15*time.Second).Should(Succeed(),
			"HC %s/%s must have completed the upgrade to %s", hc.Namespace, hc.Name, targetImage)
		Eventually(func(g Gomega, ctx context.Context) {
			current := &hyperv1.HostedCluster{}
			g.Expect(tc.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), current)).To(Succeed(), "read HC %s/%s", hc.Namespace, hc.Name)
			g.Expect(current.Status.Conditions).To(ContainElement(And(
				HaveField("Type", string(hyperv1.HostedClusterAvailable)),
				HaveField("Status", BeEquivalentTo(corev1.ConditionTrue)),
			)), "HC %s/%s must be Available", hc.Namespace, hc.Name)
		}).WithContext(tc.Context).WithTimeout(registryOverridesUpgradeTimeout).WithPolling(15*time.Second).Should(Succeed(),
			"HC %s/%s must be Available after the upgrade", hc.Namespace, hc.Name)
	})

	It("should run every control plane workload from the mirror after the upgrade", func() {
		Eventually(func(g Gomega, ctx context.Context) {
			deployments := &appsv1.DeploymentList{}
			g.Expect(tc.MgmtClient.List(ctx, deployments, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed(), "list Deployments in %s", tc.ControlPlaneNamespace)
			statefulSets := &appsv1.StatefulSetList{}
			g.Expect(tc.MgmtClient.List(ctx, statefulSets, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed(), "list StatefulSets in %s", tc.ControlPlaneNamespace)
			cronJobs := &batchv1.CronJobList{}
			g.Expect(tc.MgmtClient.List(ctx, cronJobs, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed(), "list CronJobs in %s", tc.ControlPlaneNamespace)
			done, reason, err := registryOverridesCheckImages(deployments.Items, statefulSets.Items, cronJobs.Items, overrides, hoImage, registryOverridesMandatoryWorkloads)
			if err != nil {
				StopTrying("check control plane workloads in " + tc.ControlPlaneNamespace).Wrap(err).Now()
			}
			g.Expect(done).To(BeTrue(), reason)
		}).WithContext(tc.Context).WithTimeout(registryOverridesUpgradeTimeout).WithPolling(15*time.Second).Should(Succeed(),
			"HC %s/%s control plane workloads in %s must run mirror images and be rolled out after the upgrade", hc.Namespace, hc.Name, tc.ControlPlaneNamespace)
	})

	// The registry overrides specs mirror the release of every HostedCluster
	// and the latest release before the operator is patched, so both the
	// release the cluster upgraded from and the target must be in the mirror.
	// The mirror is read-only after the setup, so finding them now proves they
	// were mirrored before the upgrade.
	It("should serve both the source and the target release payloads from the mirror", func() {
		current := &hyperv1.HostedCluster{}
		Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hc), current)).To(Succeed(), "read HC %s/%s", hc.Namespace, hc.Name)
		from, to, _, err := registryOverridesUpgradeStart(current, targetImage)
		Expect(err).NotTo(HaveOccurred(), "HC %s/%s upgrade", hc.Namespace, hc.Name)
		client, err := imageMirroringHTTPClient(ca.CertPEM)
		Expect(err).NotTo(HaveOccurred(), "mirror HTTP client")
		for _, image := range []string{from, to} {
			_, digest, _ := strings.Cut(image, "@")
			Expect(registryOverridesCheckManifest(tc.Context, client, mirrorHost, registryOverridesReleaseMirrorRepository, digest)).To(Succeed(),
				"mirror %s must serve release payload %s in %s", mirrorHost, image, registryOverridesReleaseMirrorRepository)
		}
	})

	// Kubelet events are kept for the event TTL of the cluster (3h for
	// hosted clusters, including the nested management cluster), which
	// covers the upgrade. Current container statuses are checked too, so a
	// failure that is still going on is reported even without its event.
	It("should not fail any image pull since the upgrade started", func() {
		current := &hyperv1.HostedCluster{}
		Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hc), current)).To(Succeed(), "read HC %s/%s", hc.Namespace, hc.Name)
		_, _, started, err := registryOverridesUpgradeStart(current, targetImage)
		Expect(err).NotTo(HaveOccurred(), "HC %s/%s upgrade", hc.Namespace, hc.Name)

		By("checking the control plane namespace")
		pods := &corev1.PodList{}
		Expect(tc.MgmtClient.List(tc.Context, pods, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed(), "list pods in %s", tc.ControlPlaneNamespace)
		events := &corev1.EventList{}
		Expect(tc.MgmtClient.List(tc.Context, events, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed(), "list events in %s", tc.ControlPlaneNamespace)
		failures := append(registryOverridesPullFailures(pods.Items), registryOverridesPullFailureEvents(events.Items, started)...)

		// The global pull secret syncer runs the HCCO image from the mirror on
		// the workers; it is rolled out to the target HCCO image during the
		// upgrade. Wait for that rollout first, so pulls still in progress
		// are not reported as failures.
		By("checking the global pull secret syncer on the workers")
		hostedClient, err := tc.GetHostedClusterClient(current)
		Expect(err).NotTo(HaveOccurred(), "HC %s/%s client", hc.Namespace, hc.Name)
		cpoKey := crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: "control-plane-operator"}
		dsKey := crclient.ObjectKey{Namespace: registryOverridesSyncerNamespace, Name: registryOverridesSyncerName}
		Eventually(func(g Gomega, ctx context.Context) {
			cpo := &appsv1.Deployment{}
			g.Expect(tc.MgmtClient.Get(ctx, cpoKey, cpo)).To(Succeed(), "read Deployment %s", cpoKey)
			container, err := registryOverridesPrimaryContainer(cpo)
			if err != nil {
				StopTrying("check Deployment " + cpoKey.String()).Wrap(err).Now()
			}
			daemonSet := &appsv1.DaemonSet{}
			g.Expect(hostedClient.Get(ctx, dsKey, daemonSet)).To(Succeed(), "read hosted cluster DaemonSet %s", dsKey)
			done, reason, err := registryOverridesCheckDaemonSet(daemonSet, container.Image, overrides)
			if err != nil {
				StopTrying("check hosted cluster DaemonSet " + dsKey.String()).Wrap(err).Now()
			}
			g.Expect(done).To(BeTrue(), reason)
		}).WithContext(tc.Context).WithTimeout(registryOverridesUpgradeTimeout).WithPolling(15*time.Second).Should(Succeed(),
			"HC %s/%s DaemonSet %s must run the target HCCO image from the mirror on every eligible worker", hc.Namespace, hc.Name, dsKey)
		guestPods := &corev1.PodList{}
		Expect(hostedClient.List(tc.Context, guestPods, crclient.InNamespace(registryOverridesSyncerNamespace),
			crclient.MatchingLabels{"name": registryOverridesSyncerName})).To(Succeed(), "list %s pods in the hosted cluster", dsKey)
		guestEvents := &corev1.EventList{}
		Expect(hostedClient.List(tc.Context, guestEvents, crclient.InNamespace(registryOverridesSyncerNamespace))).To(Succeed(), "list hosted cluster events in %s", registryOverridesSyncerNamespace)
		syncerEvents := make([]corev1.Event, 0, len(guestEvents.Items))
		for _, event := range guestEvents.Items {
			if strings.HasPrefix(event.InvolvedObject.Name, registryOverridesSyncerName+"-") {
				syncerEvents = append(syncerEvents, event)
			}
		}
		failures = append(failures, registryOverridesPullFailures(guestPods.Items)...)
		failures = append(failures, registryOverridesPullFailureEvents(syncerEvents, started)...)

		Expect(failures).To(BeEmpty(), "HC %s/%s must have no image pull failure since the upgrade started at %s", hc.Namespace, hc.Name, started.UTC().Format(time.RFC3339))
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:RegistryOverrides] Registry Overrides Upgrade", Label("lifecycle", "registry-overrides-upgrade", internal.InformingLabel), Ordered, func() {
	RegisterRegistryOverridesUpgradeTests(internal.GetTestContext)
})
