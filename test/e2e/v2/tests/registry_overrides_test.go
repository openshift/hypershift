//go:build e2ev2

package tests

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	"github.com/openshift/hypershift/test/e2e/v2/util/mirrorregistry"

	routev1 "github.com/openshift/api/route/v1"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/util/retry"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// registryOverridesBudget bounds everything these specs do, setup
	// included. Every wait below runs under one deadline derived from it, so
	// the per-phase timeouts never add up past it. It must fit, with the smoke
	// group that follows in the same lane (about a minute) and the reporting
	// margin, inside the CI step timeout (150m for hypershift-aws-run-e2e-v2).
	// The deadline is cooperative, not a watchdog: when it expires the waits
	// return, so Ginkgo can report the stuck phase in JUnit before the step is
	// killed, provided cancellation and reporting complete within the margin.
	// Don't count on the termination grace: run-tests doesn't forward SIGTERM.
	registryOverridesBudget = 135 * time.Minute
	// registryOverridesSetupTimeout bounds the full payload copy (~22 GiB)
	// plus the HyperShift operator rollout, within the budget. It leaves at
	// least 30 minutes of the budget for the control plane rollout.
	registryOverridesSetupTimeout = 105 * time.Minute
	// registryOverridesRolloutTimeout bounds each control plane rollout wait,
	// within the budget.
	registryOverridesRolloutTimeout = 30 * time.Minute
)

// registryOverridesEnsure mirrors the release of every HostedCluster into a
// dedicated mirror signed by the job CA, points the HyperShift operator at it
// with --registry-overrides and waits for the operator rollout. It is
// idempotent: when the operator already points at the mirror Route, it only
// reads the state back. It returns the overrides read from the operator
// Deployment, which are the oracle of the specs, and the operator image.
// budgetCtx carries the deadline of the whole Ordered container.
func registryOverridesEnsure(g Gomega, budgetCtx context.Context, tc *internal.TestContext, hc *hyperv1.HostedCluster, ca *mirrorregistry.KeyPair) (map[string]string, string) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(budgetCtx, registryOverridesSetupTimeout)
	defer cancel()
	hoKey := crclient.ObjectKey{Namespace: registryOverridesHONamespace, Name: registryOverridesHOName}

	readHO := func(g Gomega) (*appsv1.Deployment, map[string]string) {
		deployment := &appsv1.Deployment{}
		g.Expect(tc.MgmtClient.Get(ctx, hoKey, deployment)).To(Succeed(), "read HyperShift operator Deployment %s", hoKey)
		container, err := registryOverridesOperatorContainer(deployment)
		g.Expect(err).NotTo(HaveOccurred(), "HyperShift operator Deployment %s", hoKey)
		value, _, err := registryOverridesArgValue(container.Args)
		g.Expect(err).NotTo(HaveOccurred(), "HyperShift operator Deployment %s %s", hoKey, registryOverridesFlag)
		overrides, err := registryOverridesParseArg(value)
		g.Expect(err).NotTo(HaveOccurred(), "HyperShift operator Deployment %s %s", hoKey, registryOverridesFlag)
		return deployment, overrides
	}

	route := &routev1.Route{}
	routeKey := crclient.ObjectKey{Namespace: registryOverridesNamespace, Name: mirrorregistry.DefaultName}
	err := tc.MgmtClient.Get(ctx, routeKey, route)
	if err != nil && !apierrors.IsNotFound(err) {
		g.Expect(err).NotTo(HaveOccurred(), "read mirror Route %s", routeKey)
	}
	if _, current := readHO(g); err == nil && registryOverridesApplied(current, route.Spec.Host) {
		By("reusing the mirror and the HyperShift operator overrides from a previous run")
	} else {
		By("mirroring the release payload of every HostedCluster")
		hcs := &hyperv1.HostedClusterList{}
		g.Expect(tc.MgmtClient.List(ctx, hcs)).To(Succeed(), "list HostedClusters")
		releases, err := registryOverridesReleaseImages(hcs.Items)
		g.Expect(err).NotTo(HaveOccurred(), "release images to mirror")
		pullSecret, err := registryOverridesPullSecret(ctx, tc.MgmtClient, hc)
		g.Expect(err).NotTo(HaveOccurred(), "HC %s/%s pull secret", hc.Namespace, hc.Name)
		cliImage, err := registryOverridesCLIImage(ctx, tc.MgmtClient)
		g.Expect(err).NotTo(HaveOccurred(), "mirror copier image")
		registry, err := mirrorregistry.New(tc.MgmtClient, mirrorregistry.Options{
			Namespace:   registryOverridesNamespace,
			CA:          ca,
			CopierImage: cliImage,
			Storage:     mirrorregistry.StorageOptions{Size: resource.MustParse(registryOverridesStorageSize)},
		})
		g.Expect(err).NotTo(HaveOccurred(), "configure mirror in %s", registryOverridesNamespace)
		result, err := registry.Setup(ctx, mirrorregistry.MirrorRequest{ReleaseImages: releases, SourcePullSecret: pullSecret})
		g.Expect(err).NotTo(HaveOccurred(), "mirror releases %v into %s", releases, registryOverridesNamespace)
		desired, err := registryOverridesFromResult(result)
		g.Expect(err).NotTo(HaveOccurred(), "registry overrides from the mirror in %s", registryOverridesNamespace)

		// No restore: the overrides are global to the operator, so this runs
		// in a dedicated job whose management cluster is destroyed with the
		// job, and every later step of the lane runs under the overrides.
		By("pointing the HyperShift operator at the mirror")
		g.Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			deployment := &appsv1.Deployment{}
			if err := tc.MgmtClient.Get(ctx, hoKey, deployment); err != nil {
				return err
			}
			container, err := registryOverridesOperatorContainer(deployment)
			if err != nil {
				return err
			}
			container.Args = registryOverridesSetArg(container.Args, registryOverridesFormatArg(desired))
			return tc.MgmtClient.Update(ctx, deployment)
		})).To(Succeed(), "set %s on HyperShift operator Deployment %s", registryOverridesFlag, hoKey)
	}

	By("waiting for the HyperShift operator rollout")
	g.Eventually(func(g Gomega, ctx context.Context) {
		deployment := &appsv1.Deployment{}
		g.Expect(tc.MgmtClient.Get(ctx, hoKey, deployment)).To(Succeed(), "read HyperShift operator Deployment %s", hoKey)
		done, reason := registryOverridesCheckDeploymentRolledOut(deployment)
		g.Expect(done).To(BeTrue(), reason)
	}).WithContext(ctx).WithTimeout(15*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
		"HyperShift operator Deployment %s rollout", hoKey)

	deployment, overrides := readHO(g)
	g.Expect(overrides).NotTo(BeEmpty(), "HyperShift operator Deployment %s must carry %s", hoKey, registryOverridesFlag)
	container, err := registryOverridesOperatorContainer(deployment)
	g.Expect(err).NotTo(HaveOccurred(), "HyperShift operator Deployment %s", hoKey)
	return overrides, container.Image
}

// RegisterRegistryOverridesTests registers the HyperShift operator
// --registry-overrides checks. They run only in a job whose management cluster
// and operator trust the mirror CA it provides (E2E_REGISTRY_OVERRIDES_CA_*);
// elsewhere they skip. The setup patches the operator, so the job must run them
// in a lane of their own.
func RegisterRegistryOverridesTests(getTestCtx internal.TestContextGetter) {
	var (
		tc        *internal.TestContext
		hc        *hyperv1.HostedCluster
		overrides map[string]string
		hoImage   string
		// deadline is registryOverridesBudget after BeforeAll starts; every
		// node of the container works under a context that expires at it.
		deadline time.Time
	)
	budgetContext := func() (context.Context, context.CancelFunc) {
		return context.WithDeadline(tc.Context, deadline)
	}

	BeforeAll(func() {
		tc = getTestCtx()
		Expect(tc).NotTo(BeNil(), "test context must be initialized")
		deadline = time.Now().Add(registryOverridesBudget)
		budgetCtx, cancel := budgetContext()
		defer cancel()
		var err error
		hc, err = tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred(), "read HC %s/%s", tc.ClusterNamespace, tc.ClusterName)
		Expect(hc).NotTo(BeNil(), "HC %s/%s", tc.ClusterNamespace, tc.ClusterName)
		ca, err := registryOverridesLoadCA(
			internal.GetEnvVarValue("E2E_REGISTRY_OVERRIDES_CA_CERT_FILE"),
			internal.GetEnvVarValue("E2E_REGISTRY_OVERRIDES_CA_KEY_FILE"))
		Expect(err).NotTo(HaveOccurred(), "registry overrides mirror CA")
		if ca == nil {
			Skip("the job provides no registry overrides mirror CA, so the management cluster does not trust a mirror")
		}
		Expect(tc.ControlPlaneNamespace).NotTo(BeEmpty(), "HC %s/%s must have a control plane namespace", hc.Namespace, hc.Name)
		overrides, hoImage = registryOverridesEnsure(Default, budgetCtx, tc, hc, ca)
	})

	It("should resolve the release from the mirror for the control plane operator image", func() {
		budgetCtx, cancel := budgetContext()
		defer cancel()
		key := crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: "control-plane-operator"}
		Eventually(func(g Gomega, ctx context.Context) {
			deployment := &appsv1.Deployment{}
			g.Expect(tc.MgmtClient.Get(ctx, key, deployment)).To(Succeed(), "read Deployment %s", key)
			done, reason, err := registryOverridesCheckImages([]appsv1.Deployment{*deployment}, nil, overrides, hoImage, []string{key.Name})
			if err != nil {
				StopTrying("check Deployment " + key.String()).Wrap(err).Now()
			}
			g.Expect(done).To(BeTrue(), reason)
		}).WithContext(budgetCtx).WithTimeout(registryOverridesRolloutTimeout).WithPolling(10*time.Second).Should(Succeed(),
			"HC %s/%s Deployment %s must run the control plane operator image from the mirror", hc.Namespace, hc.Name, key)
	})

	It("should propagate the operator registry overrides to the CPO, HCCO and ignition-server", func() {
		budgetCtx, cancel := budgetContext()
		defer cancel()
		// One wait for all targets, so the budget is one rollout, not one per target.
		Eventually(func(g Gomega, ctx context.Context) {
			for _, name := range registryOverridesPropagationTargets {
				key := crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: name}
				deployment := &appsv1.Deployment{}
				g.Expect(tc.MgmtClient.Get(ctx, key, deployment)).To(Succeed(), "read Deployment %s", key)
				done, reason, err := registryOverridesCheckPropagated(deployment, overrides)
				if err != nil {
					StopTrying("check Deployment " + key.String()).Wrap(err).Now()
				}
				g.Expect(done).To(BeTrue(), reason)
			}
		}).WithContext(budgetCtx).WithTimeout(registryOverridesRolloutTimeout).WithPolling(10*time.Second).Should(Succeed(),
			"HC %s/%s Deployments %v must carry the operator %s", hc.Namespace, hc.Name, registryOverridesPropagationTargets, registryOverridesFlag)
	})

	It("should run every control plane workload from the mirror after the rollout", func() {
		budgetCtx, cancel := budgetContext()
		defer cancel()
		Eventually(func(g Gomega, ctx context.Context) {
			deployments := &appsv1.DeploymentList{}
			g.Expect(tc.MgmtClient.List(ctx, deployments, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed(), "list Deployments in %s", tc.ControlPlaneNamespace)
			statefulSets := &appsv1.StatefulSetList{}
			g.Expect(tc.MgmtClient.List(ctx, statefulSets, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed(), "list StatefulSets in %s", tc.ControlPlaneNamespace)
			done, reason, err := registryOverridesCheckImages(deployments.Items, statefulSets.Items, overrides, hoImage, registryOverridesMandatoryWorkloads)
			if err != nil {
				StopTrying("check control plane workloads in " + tc.ControlPlaneNamespace).Wrap(err).Now()
			}
			g.Expect(done).To(BeTrue(), reason)
		}).WithContext(budgetCtx).WithTimeout(registryOverridesRolloutTimeout).WithPolling(15*time.Second).Should(Succeed(),
			"HC %s/%s control plane workloads in %s must run mirror images and be rolled out", hc.Namespace, hc.Name, tc.ControlPlaneNamespace)
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:RegistryOverrides] Registry Overrides", Label("lifecycle", "registry-overrides", internal.InformingLabel), Ordered, func() {
	RegisterRegistryOverridesTests(internal.GetTestContext)
})
