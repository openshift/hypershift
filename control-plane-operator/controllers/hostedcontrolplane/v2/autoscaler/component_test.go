package autoscaler

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	cpofeaturegates "github.com/openshift/hypershift/control-plane-operator/featuregates"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPredicate(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "hcp-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
		},
	}

	testCases := []struct {
		name                 string
		capiKubeconfigSecret client.Object
		hcpAnnotations       map[string]string

		expected bool
	}{
		{
			name:                 "When CAPI kubeconfig secret exists, it should return true",
			capiKubeconfigSecret: manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID),
			expected:             true,
		},
		{
			name:     "When CAPI kubeconfig secret does not exist, it should return false",
			expected: false,
		},
		{
			name: "When HCP has DisableMachineManagement annotation, it should return false",
			hcpAnnotations: map[string]string{
				hyperv1.DisableMachineManagement: "true",
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp.Annotations = tc.hcpAnnotations

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.capiKubeconfigSecret != nil {
				clientBuilder = clientBuilder.WithObjects(tc.capiKubeconfigSecret)
				hcp.Status.KubeConfig = &hyperv1.KubeconfigSecretRef{
					Name: tc.capiKubeconfigSecret.GetName(),
				}
			}
			client := clientBuilder.Build()

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				Client:  client,
				HCP:     hcp,
			}

			g := NewGomegaWithT(t)

			result, err := predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestAdaptDeployment(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "hcp-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
		},
	}

	testCases := []struct {
		name              string
		hcpAnnotations    map[string]string
		AutoscalerOptions hyperv1.ClusterAutoscaling
		ExpectedArgs      []string
		UnexpectedArgs    []string
		expectedReplicas  int32
	}{
		{
			name: "When HCP has DisableClusterAutoscalerAnnotation annotation, it should set replicas to 0",
			hcpAnnotations: map[string]string{
				hyperv1.DisableClusterAutoscalerAnnotation: "true",
			},
			expectedReplicas: 0,
		},
		{
			name: "When autoscaling options are set, it should include optional arguments",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				MaxNodesTotal:        ptr.To[int32](100),
				MaxPodGracePeriod:    ptr.To[int32](300),
				MaxNodeProvisionTime: "20m",
				PodPriorityThreshold: ptr.To[int32](-5),
				Scaling:              hyperv1.ScaleUpAndScaleDown,
				ScaleDown: &hyperv1.ScaleDownConfig{
					DelayAfterAddSeconds: ptr.To[int32](600),
				},
			},
			ExpectedArgs: []string{
				"--max-nodes-total=100",
				"--max-graceful-termination-sec=300",
				"--max-node-provision-time=20m",
				"--expendable-pods-priority-cutoff=-5",
				"--scale-down-delay-after-add=600s",
			},
			expectedReplicas: 1,
		},
		{
			name: "When scale down is disabled, it should include scale-down-enabled=false argument",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				Scaling:   hyperv1.ScaleUpOnly,
				ScaleDown: &hyperv1.ScaleDownConfig{},
			},
			ExpectedArgs: []string{
				"--scale-down-enabled=false",
			},
			expectedReplicas: 1,
		},
		{
			name: "When scale down is enabled with all options, it should include all scale down arguments",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				Scaling: hyperv1.ScaleUpAndScaleDown,
				ScaleDown: &hyperv1.ScaleDownConfig{
					DelayAfterAddSeconds:        ptr.To[int32](300),
					DelayAfterDeleteSeconds:     ptr.To[int32](120),
					DelayAfterFailureSeconds:    ptr.To[int32](180),
					UnneededDurationSeconds:     ptr.To[int32](600),
					UtilizationThresholdPercent: ptr.To[int32](50),
				},
			},
			ExpectedArgs: []string{
				"--scale-down-enabled=true",
				"--scale-down-delay-after-add=300s",
				"--scale-down-delay-after-delete=120s",
				"--scale-down-delay-after-failure=180s",
				"--scale-down-unneeded-time=600s",
				"--scale-down-utilization-threshold=0.50",
			},
			expectedReplicas: 1,
		},
		{
			name: "When expanders are configured, it should include expander arguments",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				Expanders: []hyperv1.ExpanderString{
					hyperv1.LeastWasteExpander,
					hyperv1.PriorityExpander,
					hyperv1.RandomExpander,
				},
			},
			ExpectedArgs: []string{
				"--expander=least-waste,priority,random",
			},
			expectedReplicas: 1,
		},
		{
			name: "When balancing ignored labels are configured, it should include balancing-ignore-label arguments",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				BalancingIgnoredLabels: []string{
					"custom.label/zone",
					"custom.label/instance-type",
				},
			},
			ExpectedArgs: []string{
				"--balancing-ignore-label=custom.label/zone",
				"--balancing-ignore-label=custom.label/instance-type",
			},
			expectedReplicas: 1,
		},
		{
			name: "When MaxFreeDifferenceRatioPercent is set, it should include max-free-difference-ratio argument",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				MaxFreeDifferenceRatioPercent: ptr.To[int32](20),
			},
			ExpectedArgs: []string{
				"--max-free-difference-ratio=0.20",
			},
			expectedReplicas: 1,
		},
		{
			name: "When kubeClientQPS and kubeClientBurst are set, it should include both kube-client arguments",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				KubeClientQPS:   ptr.To[int32](50),
				KubeClientBurst: 100,
			},
			ExpectedArgs: []string{
				"--kube-client-qps=50",
				"--kube-client-burst=100",
			},
			expectedReplicas: 1,
		},
		{
			name: "When only kubeClientQPS is set, it should include kube-client-qps and omit kube-client-burst",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				KubeClientQPS: ptr.To[int32](20),
			},
			ExpectedArgs: []string{
				"--kube-client-qps=20",
			},
			UnexpectedArgs: []string{
				"--kube-client-burst",
			},
			expectedReplicas: 1,
		},
		{
			name: "When only kubeClientBurst is set, it should include kube-client-burst and omit kube-client-qps",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				KubeClientBurst: 40,
			},
			ExpectedArgs: []string{
				"--kube-client-burst=40",
			},
			UnexpectedArgs: []string{
				"--kube-client-qps",
			},
			expectedReplicas: 1,
		},
		{
			name:              "When kubeClientQPS and kubeClientBurst are omitted, it should not include kube-client arguments",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{},
			UnexpectedArgs: []string{
				"--kube-client-qps",
				"--kube-client-burst",
			},
			expectedReplicas: 1,
		},
		{
			name: "When kubeClientQPS is -1, it should include kube-client-qps=-1 to disable rate limiting",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				KubeClientQPS: ptr.To[int32](-1),
			},
			ExpectedArgs: []string{
				"--kube-client-qps=-1",
			},
			expectedReplicas: 1,
		},
		{
			name: "When kubeClientQPS is 0, it should include kube-client-qps=0 as a float",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				KubeClientQPS: ptr.To[int32](0),
			},
			ExpectedArgs: []string{
				"--kube-client-qps=0",
			},
			expectedReplicas: 1,
		},
		{
			name: "When kubeClientQPS is 1000, it should include kube-client-qps=1000 as a float",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				KubeClientQPS: ptr.To[int32](1000),
			},
			ExpectedArgs: []string{
				"--kube-client-qps=1000",
			},
			expectedReplicas: 1,
		},
		{
			name: "When kubeClientBurst is 1, it should include kube-client-burst=1",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				KubeClientBurst: 1,
			},
			ExpectedArgs: []string{
				"--kube-client-burst=1",
			},
			expectedReplicas: 1,
		},
		{
			name: "When kubeClientBurst is 2000, it should include kube-client-burst=2000",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				KubeClientBurst: 2000,
			},
			ExpectedArgs: []string{
				"--kube-client-burst=2000",
			},
			expectedReplicas: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp.Annotations = tc.hcpAnnotations
			hcp.Spec.Autoscaling = tc.AutoscalerOptions

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			g := NewGomegaWithT(t)

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(deployment.Spec.Replicas).To(HaveValue(Equal(tc.expectedReplicas)))

			observedArgs := deployment.Spec.Template.Spec.Containers[0].Args
			if len(tc.ExpectedArgs) > 0 {
				g.Expect(observedArgs).To(ContainElements(tc.ExpectedArgs))
			}
			for _, unexpected := range tc.UnexpectedArgs {
				g.Expect(observedArgs).NotTo(ContainElement(ContainSubstring(unexpected)))
			}
		})
	}

	t.Run("NodeBehaviorFlags", testAdaptDeploymentNodeBehaviorFlags)
	t.Run("ResourceLimits", testAdaptDeploymentResourceLimits)
	t.Run("ScaleUpAndCordon", testAdaptDeploymentScaleUpAndCordon)
	t.Run("LogVerbosity", testAdaptDeploymentLogVerbosity)
}

func TestAdaptDeploymentWithClusterAutoscalerImage(t *testing.T) {
	g := NewGomegaWithT(t)

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "hcp-namespace",
			Annotations: map[string]string{
				hyperv1.ClusterAutoscalerImage: "quay.io/custom/cluster-autoscaler:v1.28.0",
			},
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
		},
	}

	deployment, err := assets.LoadDeploymentManifest(ComponentName)
	g.Expect(err).ToNot(HaveOccurred())

	err = adaptDeployment(controlplanecomponent.WorkloadContext{
		Context: t.Context(),
		HCP:     hcp,
	}, deployment)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal("quay.io/custom/cluster-autoscaler:v1.28.0"))
}

func testAdaptDeploymentNodeBehaviorFlags(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "hcp-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
		},
	}

	testCases := []struct {
		name              string
		featureSet        configv1.FeatureSet
		AutoscalerOptions hyperv1.ClusterAutoscaling
		ExpectedArgs      []string
		UnexpectedArgs    []string
	}{
		{
			name:       "With the gate disabled, historical defaults are rendered regardless of configured fields",
			featureSet: configv1.Default,
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				LocalStorageScaleDown:     ptr.To(hyperv1.LocalStorageScaleDownSkip),
				SimilarNodeGroupBalancing: ptr.To(hyperv1.SimilarNodeGroupBalancingDoNotBalance),
				DaemonSetUtilization:      ptr.To(hyperv1.DaemonSetUtilizationExclude),
			},
			ExpectedArgs: []string{
				"--skip-nodes-with-local-storage=false",
				"--balance-similar-node-groups=true",
			},
			UnexpectedArgs: []string{
				"--skip-nodes-with-local-storage=true",
				"--balance-similar-node-groups=false",
				"--ignore-daemonsets-utilization=true",
				"--ignore-daemonsets-utilization=false",
			},
		},
		{
			name:       "With the gate enabled and fields omitted, historical defaults are rendered",
			featureSet: configv1.TechPreviewNoUpgrade,
			ExpectedArgs: []string{
				"--skip-nodes-with-local-storage=false",
				"--balance-similar-node-groups=true",
			},
			UnexpectedArgs: []string{
				"--ignore-daemonsets-utilization=true",
				"--ignore-daemonsets-utilization=false",
			},
		},
		{
			name:       "With the gate enabled, Skip/DoNotBalance/Exclude are rendered",
			featureSet: configv1.TechPreviewNoUpgrade,
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				LocalStorageScaleDown:     ptr.To(hyperv1.LocalStorageScaleDownSkip),
				SimilarNodeGroupBalancing: ptr.To(hyperv1.SimilarNodeGroupBalancingDoNotBalance),
				DaemonSetUtilization:      ptr.To(hyperv1.DaemonSetUtilizationExclude),
			},
			ExpectedArgs: []string{
				"--skip-nodes-with-local-storage=true",
				"--balance-similar-node-groups=false",
				"--ignore-daemonsets-utilization=true",
			},
			UnexpectedArgs: []string{
				"--skip-nodes-with-local-storage=false",
				"--balance-similar-node-groups=true",
				"--ignore-daemonsets-utilization=false",
			},
		},
		{
			name:       "With the gate enabled, Consider/Balance/Include are rendered",
			featureSet: configv1.TechPreviewNoUpgrade,
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				LocalStorageScaleDown:     ptr.To(hyperv1.LocalStorageScaleDownConsider),
				SimilarNodeGroupBalancing: ptr.To(hyperv1.SimilarNodeGroupBalancingBalance),
				DaemonSetUtilization:      ptr.To(hyperv1.DaemonSetUtilizationInclude),
			},
			ExpectedArgs: []string{
				"--skip-nodes-with-local-storage=false",
				"--balance-similar-node-groups=true",
				"--ignore-daemonsets-utilization=false",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cpofeaturegates.ConfigureFeatureSet(string(tc.featureSet))
			t.Cleanup(func() { cpofeaturegates.ConfigureFeatureSet(string(configv1.Default)) })

			hcp.Spec.Autoscaling = tc.AutoscalerOptions

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			g := NewGomegaWithT(t)

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			observedArgs := deployment.Spec.Template.Spec.Containers[0].Args
			g.Expect(observedArgs).To(ContainElements(tc.ExpectedArgs))
			for _, unexpected := range tc.UnexpectedArgs {
				g.Expect(observedArgs).NotTo(ContainElement(unexpected))
			}

			// These two flags must always be rendered exactly once, never duplicated.
			g.Expect(countArgsWithPrefix(observedArgs, "--skip-nodes-with-local-storage=")).To(Equal(1))
			g.Expect(countArgsWithPrefix(observedArgs, "--balance-similar-node-groups=")).To(Equal(1))
			g.Expect(countArgsWithPrefix(observedArgs, "--ignore-daemonsets-utilization=")).To(BeNumerically("<=", 1))
		})
	}
}

func countArgsWithPrefix(args []string, prefix string) int {
	count := 0
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			count++
		}
	}
	return count
}

func testAdaptDeploymentResourceLimits(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "hcp-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
		},
	}

	testCases := []struct {
		name              string
		featureSet        configv1.FeatureSet
		AutoscalerOptions hyperv1.ClusterAutoscaling
		ExpectedArgs      []string
		UnexpectedArgs    []string
	}{
		{
			name:       "When the gate is disabled, it should not render any resource limit flags even if configured",
			featureSet: configv1.Default,
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				ResourceLimits: hyperv1.ResourceLimits{
					Cores:  hyperv1.ResourceRange{Min: ptr.To(int32(0)), Max: int32(128)},
					Memory: hyperv1.ResourceRange{Min: ptr.To(int32(0)), Max: int32(512)},
					GPUs: []hyperv1.GPULimit{
						{Type: "nvidia-tesla-t4", Min: ptr.To(int32(0)), Max: ptr.To(int32(8))},
					},
				},
			},
			UnexpectedArgs: []string{
				"--cores-total=0:128",
				"--memory-total=0:512",
				"--gpu-total=nvidia-tesla-t4:0:8",
			},
		},
		{
			name:       "When the gate is enabled and resourceLimits is omitted, it should not render any resource limit flags",
			featureSet: configv1.TechPreviewNoUpgrade,
			UnexpectedArgs: []string{
				"--cores-total=",
				"--memory-total=",
				"--gpu-total=",
			},
		},
		{
			name:       "When the gate is enabled with cores and memory set, it should render both flags including an explicit zero minimum",
			featureSet: configv1.TechPreviewNoUpgrade,
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				ResourceLimits: hyperv1.ResourceLimits{
					Cores:  hyperv1.ResourceRange{Min: ptr.To(int32(0)), Max: int32(128)},
					Memory: hyperv1.ResourceRange{Min: ptr.To(int32(16)), Max: int32(512)},
				},
			},
			ExpectedArgs: []string{
				"--cores-total=0:128",
				"--memory-total=16:512",
			},
			UnexpectedArgs: []string{
				"--gpu-total=",
			},
		},
		{
			name:       "When the gate is enabled with multiple GPU types, it should render one flag per type including a zero max",
			featureSet: configv1.TechPreviewNoUpgrade,
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				ResourceLimits: hyperv1.ResourceLimits{
					GPUs: []hyperv1.GPULimit{
						{Type: "nvidia-tesla-t4", Min: ptr.To(int32(0)), Max: ptr.To(int32(8))},
						{Type: "nvidia-a100", Min: ptr.To(int32(0)), Max: ptr.To(int32(0))},
					},
				},
			},
			ExpectedArgs: []string{
				"--gpu-total=nvidia-tesla-t4:0:8",
				"--gpu-total=nvidia-a100:0:0",
			},
			UnexpectedArgs: []string{
				"--cores-total=",
				"--memory-total=",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cpofeaturegates.ConfigureFeatureSet(string(tc.featureSet))
			t.Cleanup(func() { cpofeaturegates.ConfigureFeatureSet(string(configv1.Default)) })

			hcp.Spec.Autoscaling = tc.AutoscalerOptions

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			g := NewGomegaWithT(t)

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			observedArgs := deployment.Spec.Template.Spec.Containers[0].Args
			g.Expect(observedArgs).To(ContainElements(tc.ExpectedArgs))
			for _, unexpected := range tc.UnexpectedArgs {
				for _, arg := range observedArgs {
					g.Expect(arg).NotTo(HavePrefix(unexpected))
				}
			}

			g.Expect(countArgsWithPrefix(observedArgs, "--cores-total=")).To(BeNumerically("<=", 1))
			g.Expect(countArgsWithPrefix(observedArgs, "--memory-total=")).To(BeNumerically("<=", 1))
		})
	}
}

func testAdaptDeploymentScaleUpAndCordon(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "hcp-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
		},
	}

	testCases := []struct {
		name              string
		featureSet        configv1.FeatureSet
		AutoscalerOptions hyperv1.ClusterAutoscaling
		ExpectedArgs      []string
		UnexpectedArgs    []string
	}{
		{
			name:       "When the gate is disabled, cordon and new-pod-scale-up-delay flags are never rendered even if configured",
			featureSet: configv1.Default,
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				Scaling: hyperv1.ScaleUpAndScaleDown,
				ScaleDown: &hyperv1.ScaleDownConfig{
					CordonNodeBeforeTerminating: ptr.To(hyperv1.CordonNodeBeforeTerminatingEnabled),
				},
				ScaleUp: hyperv1.ScaleUpConfig{
					NewPodScaleUpDelay: "30s",
				},
			},
			UnexpectedArgs: []string{
				"--cordon-node-before-terminating=",
				"--new-pod-scale-up-delay=",
			},
		},
		{
			name:       "When the gate is enabled and fields are omitted, cordon renders false and no delay flag is rendered",
			featureSet: configv1.TechPreviewNoUpgrade,
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				Scaling:   hyperv1.ScaleUpAndScaleDown,
				ScaleDown: &hyperv1.ScaleDownConfig{},
			},
			ExpectedArgs: []string{
				"--cordon-node-before-terminating=false",
			},
			UnexpectedArgs: []string{
				"--cordon-node-before-terminating=true",
				"--new-pod-scale-up-delay=",
			},
		},
		{
			name:       "When the gate is enabled and scaleDown is nil entirely, cordon still renders false",
			featureSet: configv1.TechPreviewNoUpgrade,
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				Scaling: hyperv1.ScaleUpAndScaleDown,
			},
			ExpectedArgs: []string{
				"--cordon-node-before-terminating=false",
			},
			UnexpectedArgs: []string{
				"--cordon-node-before-terminating=true",
				"--scale-down-enabled=",
				"--new-pod-scale-up-delay=",
			},
		},
		{
			name:       "When the gate is enabled with cordonNodeBeforeTerminating Enabled, it renders true",
			featureSet: configv1.TechPreviewNoUpgrade,
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				Scaling: hyperv1.ScaleUpAndScaleDown,
				ScaleDown: &hyperv1.ScaleDownConfig{
					CordonNodeBeforeTerminating: ptr.To(hyperv1.CordonNodeBeforeTerminatingEnabled),
				},
			},
			ExpectedArgs: []string{
				"--cordon-node-before-terminating=true",
			},
			UnexpectedArgs: []string{
				"--cordon-node-before-terminating=false",
			},
		},
		{
			name:       "When the gate is enabled with cordonNodeBeforeTerminating Disabled, it renders false",
			featureSet: configv1.TechPreviewNoUpgrade,
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				Scaling: hyperv1.ScaleUpAndScaleDown,
				ScaleDown: &hyperv1.ScaleDownConfig{
					CordonNodeBeforeTerminating: ptr.To(hyperv1.CordonNodeBeforeTerminatingDisabled),
				},
			},
			ExpectedArgs: []string{
				"--cordon-node-before-terminating=false",
			},
			UnexpectedArgs: []string{
				"--cordon-node-before-terminating=true",
			},
		},
		{
			name:       "When the gate is enabled with newPodScaleUpDelay set, it renders the duration including zero",
			featureSet: configv1.TechPreviewNoUpgrade,
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				ScaleUp: hyperv1.ScaleUpConfig{
					NewPodScaleUpDelay: "0s",
				},
			},
			ExpectedArgs: []string{
				"--new-pod-scale-up-delay=0s",
			},
		},
		{
			name:       "When the gate is enabled with a 1h newPodScaleUpDelay, it renders the duration",
			featureSet: configv1.TechPreviewNoUpgrade,
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				ScaleUp: hyperv1.ScaleUpConfig{
					NewPodScaleUpDelay: "1h",
				},
			},
			ExpectedArgs: []string{
				"--new-pod-scale-up-delay=1h",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cpofeaturegates.ConfigureFeatureSet(string(tc.featureSet))
			t.Cleanup(func() { cpofeaturegates.ConfigureFeatureSet(string(configv1.Default)) })

			hcp.Spec.Autoscaling = tc.AutoscalerOptions

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			g := NewGomegaWithT(t)

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			observedArgs := deployment.Spec.Template.Spec.Containers[0].Args
			g.Expect(observedArgs).To(ContainElements(tc.ExpectedArgs))
			for _, unexpected := range tc.UnexpectedArgs {
				for _, arg := range observedArgs {
					g.Expect(arg).NotTo(HavePrefix(unexpected))
				}
			}

			g.Expect(countArgsWithPrefix(observedArgs, "--cordon-node-before-terminating=")).To(BeNumerically("<=", 1))
			g.Expect(countArgsWithPrefix(observedArgs, "--new-pod-scale-up-delay=")).To(BeNumerically("<=", 1))
		})
	}
}

func testAdaptDeploymentLogVerbosity(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "hcp-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
		},
	}

	testCases := []struct {
		name              string
		featureSet        configv1.FeatureSet
		AutoscalerOptions hyperv1.ClusterAutoscaling
		ExpectedArg       string
	}{
		{
			name:        "When the gate is disabled and logVerbosity is omitted, it defaults to 4",
			featureSet:  configv1.Default,
			ExpectedArg: "--v=4",
		},
		{
			name:       "When the gate is disabled but logVerbosity is set, it is ignored and defaults to 4",
			featureSet: configv1.Default,
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				LogVerbosity: ptr.To(int32(8)),
			},
			ExpectedArg: "--v=4",
		},
		{
			name:        "When the gate is enabled and logVerbosity is omitted, it defaults to 4",
			featureSet:  configv1.TechPreviewNoUpgrade,
			ExpectedArg: "--v=4",
		},
		{
			name:       "When the gate is enabled and logVerbosity is explicitly 0, it renders 0",
			featureSet: configv1.TechPreviewNoUpgrade,
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				LogVerbosity: ptr.To(int32(0)),
			},
			ExpectedArg: "--v=0",
		},
		{
			name:       "When the gate is enabled and logVerbosity is 6, it renders 6",
			featureSet: configv1.TechPreviewNoUpgrade,
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				LogVerbosity: ptr.To(int32(6)),
			},
			ExpectedArg: "--v=6",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cpofeaturegates.ConfigureFeatureSet(string(tc.featureSet))
			t.Cleanup(func() { cpofeaturegates.ConfigureFeatureSet(string(configv1.Default)) })

			hcp.Spec.Autoscaling = tc.AutoscalerOptions

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			g := NewGomegaWithT(t)

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			observedArgs := deployment.Spec.Template.Spec.Containers[0].Args
			g.Expect(observedArgs).To(ContainElement(tc.ExpectedArg))
			g.Expect(countArgsWithPrefix(observedArgs, "--v=")).To(Equal(1))
		})
	}
}
