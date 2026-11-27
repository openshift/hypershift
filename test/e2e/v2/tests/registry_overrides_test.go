//go:build e2ev2

package tests

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// RegisterRegistryOverridesTests registers the HyperShift operator
// --registry-overrides checks. They only read state: the job deploys and
// populates the mirror, configures the operator with the overrides before it
// creates the hosted clusters, makes the management cluster and the workers
// trust the mirror, and passes the overrides it configured in
// E2E_REGISTRY_OVERRIDES. When it is unset or empty the specs skip.
func RegisterRegistryOverridesTests(getTestCtx internal.TestContextGetter) {
	var (
		tc       *internal.TestContext
		hc       *hyperv1.HostedCluster
		expected map[string]string
	)

	BeforeEach(func() {
		tc = getTestCtx()
		Expect(tc).NotTo(BeNil(), "test context must be initialized")
		var err error
		hc, err = tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred(), "read HC %s/%s", tc.ClusterNamespace, tc.ClusterName)
		Expect(hc).NotTo(BeNil(), "HC %s/%s", tc.ClusterNamespace, tc.ClusterName)
		var configured bool
		expected, configured, err = registryOverridesInput(internal.GetEnvVarValue(registryOverridesInputEnv))
		Expect(err).NotTo(HaveOccurred(), "registry overrides input")
		if !configured {
			Skip(registryOverridesInputEnv + " is unset or empty: the job configured no registry overrides")
		}
		Expect(tc.ControlPlaneNamespace).NotTo(BeEmpty(), "HC %s/%s must have a control plane namespace", hc.Namespace, hc.Name)
	})

	// readOperator returns the overrides and the image of the HyperShift
	// operator, failing when the operator doesn't run with the expected
	// overrides.
	readOperator := func() (map[string]string, string) {
		GinkgoHelper()
		key := crclient.ObjectKey{Namespace: registryOverridesHONamespace, Name: registryOverridesHOName}
		deployment := &appsv1.Deployment{}
		Expect(tc.MgmtClient.Get(tc.Context, key, deployment)).To(Succeed(), "read HyperShift operator Deployment %s", key)
		overrides, found, image, err := registryOverridesOperatorState(deployment)
		Expect(err).NotTo(HaveOccurred(), "HyperShift operator Deployment %s", key)
		Expect(found).To(BeTrue(), "HyperShift operator Deployment %s must run with %s", key, registryOverridesFlag)
		Expect(registryOverridesFormatArg(overrides)).To(Equal(registryOverridesFormatArg(expected)),
			"HyperShift operator Deployment %s %s must match %s", key, registryOverridesFlag, registryOverridesInputEnv)
		return overrides, image
	}

	It("should run the HyperShift operator with the registry overrides configured by the job", func() {
		readOperator()
	})

	// The HO resolves the release through RegistryMirrorProviderDecorator,
	// which rewrites the release pullspec and the component images
	// separately, wrapped in a decorator that first tries the management
	// cluster IDMS/ICSP mirrors. With the release on an overridden source and
	// no IDMS/ICSP mirror applying to it, the lookup itself went to the
	// override mirror; the CPO image check shows the components taken from
	// that release are on the mirror.
	It("should resolve the release from the mirror for the control plane operator image", func() {
		_, hoImage := readOperator()
		done, reason := registryOverridesCheckReleaseCovered(hc, expected)
		Expect(done).To(BeTrue(), reason)
		key := crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: "control-plane-operator"}
		deployment := &appsv1.Deployment{}
		Expect(tc.MgmtClient.Get(tc.Context, key, deployment)).To(Succeed(), "read Deployment %s", key)
		done, reason, err := registryOverridesCheckNoPolicyMirror(hc, deployment)
		Expect(err).NotTo(HaveOccurred(), "check management cluster mirrors on Deployment %s", key)
		Expect(done).To(BeTrue(), reason)
		done, reason, err = registryOverridesCheckImages([]appsv1.Deployment{*deployment}, nil, nil, expected, hoImage, []string{key.Name})
		Expect(err).NotTo(HaveOccurred(), "check Deployment %s", key)
		Expect(done).To(BeTrue(), reason)
	})

	It("should propagate the operator registry overrides to the CPO, HCCO and ignition-server", func() {
		for _, name := range registryOverridesPropagationTargets {
			key := crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: name}
			deployment := &appsv1.Deployment{}
			Expect(tc.MgmtClient.Get(tc.Context, key, deployment)).To(Succeed(), "read Deployment %s", key)
			done, reason, err := registryOverridesCheckPropagated(deployment, expected)
			Expect(err).NotTo(HaveOccurred(), "check Deployment %s", key)
			Expect(done).To(BeTrue(), reason)
		}
	})

	// A sweep over every workload template in the control plane namespace,
	// including workloads not in the workload registry. The per-workload pod
	// checks are in control_plane_workloads_test.go ("Registry overrides").
	It("should replace every overridden source in the control plane workload templates", func() {
		_, hoImage := readOperator()
		deployments := &appsv1.DeploymentList{}
		Expect(tc.MgmtClient.List(tc.Context, deployments, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed(), "list Deployments in %s", tc.ControlPlaneNamespace)
		statefulSets := &appsv1.StatefulSetList{}
		Expect(tc.MgmtClient.List(tc.Context, statefulSets, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed(), "list StatefulSets in %s", tc.ControlPlaneNamespace)
		cronJobs := &batchv1.CronJobList{}
		Expect(tc.MgmtClient.List(tc.Context, cronJobs, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed(), "list CronJobs in %s", tc.ControlPlaneNamespace)
		done, reason, err := registryOverridesCheckImages(deployments.Items, statefulSets.Items, cronJobs.Items, expected, hoImage, registryOverridesMandatoryWorkloads)
		Expect(err).NotTo(HaveOccurred(), "check control plane workloads in %s", tc.ControlPlaneNamespace)
		Expect(done).To(BeTrue(), reason)
	})

	// The ignition-server resolves the release through the overrides when it
	// generates a NodePool payload. Each pod has its own cache (no leader
	// election), so every pod of the current ReplicaSet must have logged a
	// generated payload. The Deployment must be rolled out, so no pod of a
	// previous revision is counted.
	It("should generate the ignition payloads with the operator registry overrides", func() {
		kubeClient, err := kubernetes.NewForConfig(tc.MgmtRESTConfig)
		Expect(err).NotTo(HaveOccurred(), "management cluster clientset")
		key := crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: registryOverridesIgnitionServerName}
		deployment := &appsv1.Deployment{}
		Expect(tc.MgmtClient.Get(tc.Context, key, deployment)).To(Succeed(), "read Deployment %s", key)
		done, reason := registryOverridesCheckDeploymentRolledOut(deployment)
		Expect(done).To(BeTrue(), reason)
		replicaSets := &appsv1.ReplicaSetList{}
		Expect(tc.MgmtClient.List(tc.Context, replicaSets, crclient.InNamespace(key.Namespace))).To(Succeed(), "list ReplicaSets in %s", key.Namespace)
		templateHash, err := registryOverridesCurrentTemplateHash(deployment, replicaSets.Items)
		Expect(err).NotTo(HaveOccurred(), "current ReplicaSet of %s", key)
		pods := &corev1.PodList{}
		Expect(tc.MgmtClient.List(tc.Context, pods, crclient.InNamespace(key.Namespace), crclient.MatchingLabels{"app": registryOverridesIgnitionServerName})).To(Succeed(), "list %s pods", key)
		current, err := registryOverridesCurrentPods(pods.Items, templateHash, registryOverridesIgnitionServerName, expected)
		Expect(err).NotTo(HaveOccurred(), "check %s pods", key)
		Expect(current).To(HaveLen(int(ptr.Deref(deployment.Spec.Replicas, 1))), "%s pods running with the operator %s", key, registryOverridesFlag)
		for _, name := range current {
			stream, err := kubeClient.CoreV1().Pods(key.Namespace).GetLogs(name, &corev1.PodLogOptions{Container: registryOverridesIgnitionServerName}).Stream(tc.Context)
			Expect(err).NotTo(HaveOccurred(), "stream log of pod %s/%s", key.Namespace, name)
			found, err := registryOverridesLogContainsText(stream, registryOverridesPayloadGeneratedLine)
			_ = stream.Close()
			Expect(err).NotTo(HaveOccurred(), "read log of pod %s/%s", key.Namespace, name)
			Expect(found).To(BeTrue(), "pod %s/%s must log %q", key.Namespace, name, registryOverridesPayloadGeneratedLine)
		}
		nodePools := &hyperv1.NodePoolList{}
		Expect(tc.MgmtClient.List(tc.Context, nodePools, crclient.InNamespace(hc.Namespace))).To(Succeed(), "list NodePools in %s", hc.Namespace)
		done, reason = registryOverridesCheckPayloadsGenerated(nodePools.Items, hc.Name)
		Expect(done).To(BeTrue(), reason)
	})

	// The HCCO runs the global pull secret syncer DaemonSet on the workers with
	// its own image, which comes from the mirror, so the eligible workers pull
	// it from the mirror. Only some workers are eligible (Replace NodePools on
	// AWS and Azure, Karpenter nodes); with none the spec skips.
	It("should run the global pull secret syncer on the eligible workers with the HCCO image from the mirror", func() {
		hostedClient, err := tc.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred(), "HC %s/%s client", hc.Namespace, hc.Name)
		nodes := &corev1.NodeList{}
		Expect(hostedClient.List(tc.Context, nodes)).To(Succeed(), "list hosted cluster nodes")
		if len(registryOverridesSyncerEligibleNodes(nodes.Items)) == 0 {
			Skip("no hosted cluster node is eligible for the global pull secret syncer")
		}
		// The HO sets HOSTED_CLUSTER_CONFIG_OPERATOR_IMAGE to the CPO image.
		cpoKey := crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: "control-plane-operator"}
		cpo := &appsv1.Deployment{}
		Expect(tc.MgmtClient.Get(tc.Context, cpoKey, cpo)).To(Succeed(), "read Deployment %s", cpoKey)
		container, err := registryOverridesPrimaryContainer(cpo)
		Expect(err).NotTo(HaveOccurred(), "Deployment %s", cpoKey)
		dsKey := crclient.ObjectKey{Namespace: registryOverridesSyncerNamespace, Name: registryOverridesSyncerName}
		daemonSet := &appsv1.DaemonSet{}
		Expect(hostedClient.Get(tc.Context, dsKey, daemonSet)).To(Succeed(), "read hosted cluster DaemonSet %s", dsKey)
		done, reason, err := registryOverridesCheckDaemonSet(daemonSet, container.Image, expected)
		Expect(err).NotTo(HaveOccurred(), "check hosted cluster DaemonSet %s", dsKey)
		Expect(done).To(BeTrue(), reason)
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:RegistryOverrides] Registry Overrides", Label("registry-overrides", internal.InformingLabel), func() {
	RegisterRegistryOverridesTests(internal.GetTestContext)
})
