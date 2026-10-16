package autoscaler

import (
	"context"
	"slices"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/google/go-cmp/cmp"
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
		name                   string
		hcpAnnotations         map[string]string
		AutoscalerOptions      hyperv1.ClusterAutoscaling
		ExpectedArgs           []string
		expectedKubeClientArgs []string
		expectedReplicas       int32
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
			name: "When kubeClientQPS and kubeClientBurst are set, it should override both defaults",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				KubeClientQPS:   ptr.To[int32](20),
				KubeClientBurst: 40,
			},
			expectedKubeClientArgs: []string{
				"--kube-client-qps=50",
				"--kube-client-burst=100",
				"--kube-client-qps=20",
				"--kube-client-burst=40",
			},
			expectedReplicas: 1,
		},
		{
			name: "When only kubeClientQPS is set, it should override QPS and retain the default burst",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				KubeClientQPS: ptr.To[int32](20),
			},
			expectedKubeClientArgs: []string{
				"--kube-client-qps=50",
				"--kube-client-burst=100",
				"--kube-client-qps=20",
			},
			expectedReplicas: 1,
		},
		{
			name: "When only kubeClientBurst is set, it should override burst and retain the default QPS",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				KubeClientBurst: 40,
			},
			expectedKubeClientArgs: []string{
				"--kube-client-qps=50",
				"--kube-client-burst=100",
				"--kube-client-burst=40",
			},
			expectedReplicas: 1,
		},
		{
			name:              "When kubeClientQPS and kubeClientBurst are omitted, it should use QPS 50 and burst 100",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{},
			expectedReplicas:  1,
		},
		{
			name: "When kubeClientQPS is -1, it should include kube-client-qps=-1 to disable rate limiting",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				KubeClientQPS: ptr.To[int32](-1),
			},
			expectedKubeClientArgs: []string{
				"--kube-client-qps=50",
				"--kube-client-burst=100",
				"--kube-client-qps=-1",
			},
			expectedReplicas: 1,
		},
		{
			name: "When kubeClientQPS is 0, it should include kube-client-qps=0 as a float",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				KubeClientQPS: ptr.To[int32](0),
			},
			expectedKubeClientArgs: []string{
				"--kube-client-qps=50",
				"--kube-client-burst=100",
				"--kube-client-qps=0",
			},
			expectedReplicas: 1,
		},
		{
			name: "When kubeClientQPS is 1000, it should include kube-client-qps=1000 as a float",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				KubeClientQPS: ptr.To[int32](1000),
			},
			expectedKubeClientArgs: []string{
				"--kube-client-qps=50",
				"--kube-client-burst=100",
				"--kube-client-qps=1000",
			},
			expectedReplicas: 1,
		},
		{
			name: "When kubeClientBurst is 1, it should include kube-client-burst=1",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				KubeClientBurst: 1,
			},
			expectedKubeClientArgs: []string{
				"--kube-client-qps=50",
				"--kube-client-burst=100",
				"--kube-client-burst=1",
			},
			expectedReplicas: 1,
		},
		{
			name: "When kubeClientBurst is 2000, it should include kube-client-burst=2000",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				KubeClientBurst: 2000,
			},
			expectedKubeClientArgs: []string{
				"--kube-client-qps=50",
				"--kube-client-burst=100",
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
			var kubeClientArgs []string
			for _, arg := range observedArgs {
				if strings.HasPrefix(arg, KubeClientQPSArg.String()+"=") || strings.HasPrefix(arg, KubeClientBurstArg.String()+"=") {
					kubeClientArgs = append(kubeClientArgs, arg)
				}
			}
			expectedKubeClientArgs := tc.expectedKubeClientArgs
			if expectedKubeClientArgs == nil {
				expectedKubeClientArgs = []string{"--kube-client-qps=50", "--kube-client-burst=100"}
			}
			// Scalar flags use the last value, so explicit overrides must follow the defaults.
			g.Expect(kubeClientArgs).To(Equal(expectedKubeClientArgs))
		})
	}
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

func TestReconcile(t *testing.T) {
	t.Run("When an existing deployment omits kube client limits, it should add defaults and reconcile idempotently", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hcp",
				Namespace: "hcp-namespace",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				InfraID:      "test-infra-id",
				ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.22.0-x86_64",
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform,
				},
			},
			Status: hyperv1.HostedControlPlaneStatus{
				KubeConfig: &hyperv1.KubeconfigSecretRef{Name: "admin-kubeconfig"},
			},
		}
		imageProvider := testutil.FakeImageProvider(testutil.WithVersion("4.22.0"))
		kubeAPIServer := &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kube-apiserver",
				Namespace: hcp.Namespace,
			},
			Status: hyperv1.ControlPlaneComponentStatus{
				Version: imageProvider.Version(),
				Conditions: []metav1.Condition{
					{Type: string(hyperv1.ControlPlaneComponentAvailable), Status: metav1.ConditionTrue},
					{Type: string(hyperv1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue},
				},
			},
		}
		deployment, err := assets.LoadDeploymentManifest(ComponentName)
		g.Expect(err).ToNot(HaveOccurred())
		deployment.Namespace = hcp.Namespace
		deployment.Spec.Template.Spec.Containers[0].Args = slices.DeleteFunc(deployment.Spec.Template.Spec.Containers[0].Args, func(arg string) bool {
			return strings.HasPrefix(arg, KubeClientQPSArg.String()+"=") || strings.HasPrefix(arg, KubeClientBurstArg.String()+"=")
		})
		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(
			deployment, kubeAPIServer, manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID),
		).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				// Match the real client's preservation of a typed object's GVK on Get.
				gvk := obj.GetObjectKind().GroupVersionKind()
				defer obj.GetObjectKind().SetGroupVersionKind(gvk)
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()
		cpContext := controlplanecomponent.ControlPlaneContext{
			Context:                  t.Context(),
			Client:                   fakeClient,
			HCP:                      hcp,
			ApplyProvider:            upsert.NewApplyProvider(true),
			ReleaseImageProvider:     imageProvider,
			UserReleaseImageProvider: imageProvider,
		}
		component := NewComponent()
		g.Expect(component.Reconcile(cpContext)).To(Succeed())

		key := client.ObjectKeyFromObject(deployment)
		updated := &appsv1.Deployment{}
		g.Expect(fakeClient.Get(t.Context(), key, updated)).To(Succeed())
		g.Expect(updated.Spec.Template.Spec.Containers[0].Args).To(ContainElements("--kube-client-qps=50", "--kube-client-burst=100"))

		g.Expect(component.Reconcile(cpContext)).To(Succeed())
		reconciled := &appsv1.Deployment{}
		g.Expect(fakeClient.Get(t.Context(), key, reconciled)).To(Succeed())
		g.Expect(cmp.Diff(updated, reconciled)).To(BeEmpty())
		g.Expect(cpContext.ApplyProvider.ValidateUpdateEvents(1)).To(Succeed())
	})
}
