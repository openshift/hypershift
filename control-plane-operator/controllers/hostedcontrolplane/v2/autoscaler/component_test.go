package autoscaler

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

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
			name:                 "when CAPI kubeconfig secret exist predicate returns true",
			capiKubeconfigSecret: manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID),
			expected:             true,
		},
		{
			name:     "when CAPI kubeconfig secret doesn't exist, predicate return false",
			expected: false,
		},
		{
			name: "when HCP has DisableMachineManagement annotation predicate return false",
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
				Context: context.Background(),
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
		expectedReplicas  int32
	}{
		{
			name: "when HCP has DisableClusterAutoscalerAnnotation annotation replicas should be 0",
			hcpAnnotations: map[string]string{
				hyperv1.DisableClusterAutoscalerAnnotation: "true",
			},
			expectedReplicas: 0,
		},
		{
			name: "when autoscaling options is set, container has optional arguments",
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
			name: "when scale down is disabled, container has scale down disabled argument",
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
			name: "when scale down is enabled with all options, container has all scale down arguments",
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
			name: "when expanders are configured, container has expander arguments",
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
			name: "when balancing ignored labels are configured, container has balancing ignore label arguments",
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
			name: "when MaxFreeDifferenceRatioPercent is set, container has max-free-difference-ratio argument",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				MaxFreeDifferenceRatioPercent: ptr.To[int32](20),
			},
			ExpectedArgs: []string{
				"--max-free-difference-ratio=0.20",
			},
			expectedReplicas: 1,
		},
		{
			name: "when ExtraArgs is set, container has extra arguments",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				ExtraArgs: "--cordon-node-before-terminating=true    --daemonset-eviction-for-empty-nodes=false --ok-total-unready-count=100",
			},
			ExpectedArgs: []string{
				"--cordon-node-before-terminating=true",
				"--daemonset-eviction-for-empty-nodes=false",
				"--ok-total-unready-count=100",
			},
			expectedReplicas: 1,
		},
		{
			name: "when ExtraArgs is empty, no extra arguments are added",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				ExtraArgs: "",
			},
			ExpectedArgs:     []string{},
			expectedReplicas: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp.Annotations = tc.hcpAnnotations
			hcp.Spec.Autoscaling = tc.AutoscalerOptions

			cpContext := controlplanecomponent.WorkloadContext{
				Context: context.Background(),
				HCP:     hcp,
			}

			g := NewGomegaWithT(t)

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(deployment.Spec.Replicas).To(HaveValue(Equal(tc.expectedReplicas)))

			if len(tc.ExpectedArgs) > 0 {
				observedArgs := deployment.Spec.Template.Spec.Containers[0].Args
				g.Expect(observedArgs).To(ContainElements(tc.ExpectedArgs))
			}
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
		Context: context.Background(),
		HCP:     hcp,
	}, deployment)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal("quay.io/custom/cluster-autoscaler:v1.28.0"))
}
