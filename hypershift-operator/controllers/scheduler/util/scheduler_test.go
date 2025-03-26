package util

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestSetHostedClusterSchedulingAnnotations(t *testing.T) {
	tests := []struct {
		name        string
		hc          *hyperv1.HostedCluster
		size        string
		config      *schedulingv1alpha1.ClusterSizingConfiguration
		nodes       []corev1.Node
		expectedHC  *hyperv1.HostedCluster
		expectedErr string
	}{
		{
			name:        "NoSizeConfig",
			hc:          &hyperv1.HostedCluster{},
			size:        "small",
			config:      &schedulingv1alpha1.ClusterSizingConfiguration{},
			nodes:       []corev1.Node{},
			expectedHC:  nil,
			expectedErr: "could not find size configuration for size small",
		},
		{
			name: "ValidSizeConfigWithAnnotations",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			size: "medium",
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{
							Name: "medium",
							Effects: &schedulingv1alpha1.Effects{
								KASGoMemLimit: resource.NewQuantity(1024, resource.BinarySI),
							},
						},
					},
				},
			},
			nodes: []corev1.Node{},
			expectedHC: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.HostedClusterScheduledAnnotation:     "true",
						hyperv1.KubeAPIServerGOMemoryLimitAnnotation: "1Ki",
					},
				},
			},
			expectedErr: "",
		},
		{
			name: "ValidSizeConfigWithNodesProvidingAnnotations",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			size: "large",
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{
							Name: "large",
							Effects: &schedulingv1alpha1.Effects{
								KASGoMemLimit: resource.NewQuantity(2048, resource.BinarySI),
							},
						},
					},
				},
			},
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							GoMemLimitLabel: "4096",
						},
					},
				},
			},
			expectedHC: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.HostedClusterScheduledAnnotation:               "true",
						hyperv1.KubeAPIServerGOMemoryLimitAnnotation:           "4096",
						hyperv1.RequestServingNodeAdditionalSelectorAnnotation: fmt.Sprintf("%s=%s", hyperv1.NodeSizeLabel, "large"),
					},
				},
			},
			expectedErr: "",
		},
		{
			name: "SizeConfigWithMissingOptionalFields",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			size: "small",
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{
							Name: "small",
						},
					},
				},
			},
			nodes: []corev1.Node{},
			expectedHC: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.HostedClusterScheduledAnnotation: "true",
					},
				},
			},
			expectedErr: "",
		},
		{
			name: "SizeConfigWithMachineHealthCheckTimeout",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			size: "medium",
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{
							Name: "medium",
							Effects: &schedulingv1alpha1.Effects{
								MachineHealthCheckTimeout: &metav1.Duration{Duration: 30000000000},
							},
						},
					},
				},
			},
			nodes: []corev1.Node{},
			expectedHC: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.HostedClusterScheduledAnnotation:    "true",
						hyperv1.MachineHealthCheckTimeoutAnnotation: "30s",
					},
				},
			},
			expectedErr: "",
		},
		{
			name: "SizeConfigWithMachineHealthCheckTimeoutRemoved",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.MachineHealthCheckTimeoutAnnotation: "30s",
					},
				},
			},
			size: "medium",
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{
							Name: "medium",
						},
						{
							Name: "large",
							Effects: &schedulingv1alpha1.Effects{
								MachineHealthCheckTimeout: &metav1.Duration{Duration: 30000000000},
							},
						},
					},
				},
			},
			nodes: []corev1.Node{},
			expectedHC: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.HostedClusterScheduledAnnotation: "true",
					},
				},
			},
			expectedErr: "",
		},
		{
			name: "SizeConfigWithResourceRequests",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			size: "large",
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{
							Name: "large",
							Effects: &schedulingv1alpha1.Effects{
								ResourceRequests: []schedulingv1alpha1.ResourceRequest{
									{
										DeploymentName: "kas",
										ContainerName:  "kube-apiserver",
										Memory:         resource.NewQuantity(2048, resource.BinarySI),
										CPU:            resource.NewMilliQuantity(500, resource.DecimalSI),
									},
								},
							},
						},
					},
				},
			},
			nodes: []corev1.Node{},
			expectedHC: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.HostedClusterScheduledAnnotation:                                                          "true",
						fmt.Sprintf("%s/%s.%s", hyperv1.ResourceRequestOverrideAnnotationPrefix, "kas", "kube-apiserver"): "memory=2Ki,cpu=500m",
					},
				},
			},
			expectedErr: "",
		},
		{
			name: "SizeConfigWithSubnets",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			size: "medium",
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{
							Name: "medium",
						},
					},
				},
			},
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							LBSubnetsLabel: "subnet-54321",
						},
					},
				},
			},
			expectedHC: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.HostedClusterScheduledAnnotation:               "true",
						hyperv1.AWSLoadBalancerSubnetsAnnotation:               "subnet-54321",
						hyperv1.RequestServingNodeAdditionalSelectorAnnotation: fmt.Sprintf("%s=%s", hyperv1.NodeSizeLabel, "medium"),
					},
				},
			},
			expectedErr: "",
		},

		{
			name: "SizeConfigWithPriorityClasses",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			size: "medium",
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{
							Name: "medium",
							Effects: &schedulingv1alpha1.Effects{
								ControlPlanePriorityClassName: ptr.To("control-plane-priority"),
								EtcdPriorityClassName:         ptr.To("etcd-priority"),
								APICriticalPriorityClassName:  ptr.To("api-critical-priority"),
							},
						},
					},
				},
			},
			nodes: []corev1.Node{},
			expectedHC: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.HostedClusterScheduledAnnotation: "true",
						hyperv1.ControlPlanePriorityClass:        "control-plane-priority",
						hyperv1.EtcdPriorityClass:                "etcd-priority",
						hyperv1.APICriticalPriorityClass:         "api-critical-priority",
					},
				},
			},
			expectedErr: "",
		},
		{
			name: "SizeConfigWithMaximumRequestsInflight",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			size: "large",
			config: &schedulingv1alpha1.ClusterSizingConfiguration{
				Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
					Sizes: []schedulingv1alpha1.SizeConfiguration{
						{
							Name: "large",
							Effects: &schedulingv1alpha1.Effects{
								MaximumRequestsInflight:         ptr.To(1000),
								MaximumMutatingRequestsInflight: ptr.To(500),
							},
						},
					},
				},
			},
			nodes: []corev1.Node{},
			expectedHC: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.HostedClusterScheduledAnnotation:             "true",
						hyperv1.KubeAPIServerMaximumRequestsInFlight:         "1000",
						hyperv1.KubeAPIServerMaximumMutatingRequestsInFlight: "500",
					},
				},
			},
			expectedErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			updatedHC, err := setHostedClusterSchedulingAnnotations(tt.hc, tt.size, tt.config, tt.nodes)
			if tt.expectedErr != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tt.expectedErr))
			}
			g.Expect(updatedHC).To(Equal(tt.expectedHC))
		})
	}
}

func TestResourceRequestsToOverrideAnnotations(t *testing.T) {
	tests := []struct {
		name     string
		input    []schedulingv1alpha1.ResourceRequest
		expected map[string]string
	}{
		{
			name:     "empty input",
			input:    []schedulingv1alpha1.ResourceRequest{},
			expected: map[string]string{},
		},
		{
			name: "kube apiserver memory request",
			input: []schedulingv1alpha1.ResourceRequest{
				{
					DeploymentName: "kube-apiserver",
					ContainerName:  "kube-apiserver",
					Memory:         mustQty("1Gi"),
				},
			},
			expected: map[string]string{
				"resource-request-override.hypershift.openshift.io/kube-apiserver.kube-apiserver": "memory=1Gi",
			},
		},
		{
			name: "etcd memory and cpu request",
			input: []schedulingv1alpha1.ResourceRequest{
				{
					DeploymentName: "etcd",
					ContainerName:  "etcd",
					Memory:         mustQty("1Gi"),
					CPU:            mustQty("500m"),
				},
			},
			expected: map[string]string{
				"resource-request-override.hypershift.openshift.io/etcd.etcd": "memory=1Gi,cpu=500m",
			},
		},
		{
			name: "kube-controller manager cpu request",
			input: []schedulingv1alpha1.ResourceRequest{
				{
					DeploymentName: "kube-controller-manager",
					ContainerName:  "kube-controller-manager",
					CPU:            mustQty("500m"),
				},
			},
			expected: map[string]string{
				"resource-request-override.hypershift.openshift.io/kube-controller-manager.kube-controller-manager": "cpu=500m",
			},
		},
		{
			name: "kube-apiserver and etcd memory request",
			input: []schedulingv1alpha1.ResourceRequest{
				{
					DeploymentName: "kube-apiserver",
					ContainerName:  "kube-apiserver",
					Memory:         mustQty("1Gi"),
				},
				{
					DeploymentName: "etcd",
					ContainerName:  "etcd",
					Memory:         mustQty("2Gi"),
				},
			},
			expected: map[string]string{
				"resource-request-override.hypershift.openshift.io/kube-apiserver.kube-apiserver": "memory=1Gi",
				"resource-request-override.hypershift.openshift.io/etcd.etcd":                     "memory=2Gi",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			actual := ResourceRequestsToOverrideAnnotations(test.input)
			g.Expect(actual).To(Equal(test.expected))
		})
	}
}

func mustQty(qty string) *resource.Quantity {
	result, err := resource.ParseQuantity(qty)
	if err != nil {
		panic(err)
	}
	return &result
}
