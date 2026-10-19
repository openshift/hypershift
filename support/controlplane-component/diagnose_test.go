package controlplanecomponent

import (
	"context"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDiagnosePodFailure(t *testing.T) {
	namespace := "test-ns"
	componentName := "test-component"

	testCases := []struct {
		name           string
		deployment     *appsv1.Deployment
		pods           []*corev1.Pod
		expectedReason string
		expectedMsg    string
	}{
		{
			name: "When a pod has CrashLoopBackOff it should return CrashLoopBackOff reason",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: componentName, Namespace: namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": componentName},
					},
					Replicas: ptr.To[int32](1),
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-abc", Namespace: namespace,
						Labels: map[string]string{"app": componentName},
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								Name: "main",
								State: corev1.ContainerState{
									Waiting: &corev1.ContainerStateWaiting{
										Reason: "CrashLoopBackOff",
									},
								},
								LastTerminationState: corev1.ContainerState{
									Terminated: &corev1.ContainerStateTerminated{
										Reason: "OOMKilled",
									},
								},
							},
						},
					},
				},
			},
			expectedReason: hyperv1.ReasonCrashLoopBackOff,
			expectedMsg:    "CrashLoopBackOff",
		},
		{
			name: "When a pod has ImagePullBackOff it should return ImagePullBackOff reason",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: componentName, Namespace: namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": componentName},
					},
					Replicas: ptr.To[int32](1),
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-def", Namespace: namespace,
						Labels: map[string]string{"app": componentName},
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								Name: "main",
								State: corev1.ContainerState{
									Waiting: &corev1.ContainerStateWaiting{
										Reason: "ImagePullBackOff",
									},
								},
							},
						},
					},
				},
			},
			expectedReason: hyperv1.ReasonImagePullBackOff,
			expectedMsg:    "ImagePullBackOff",
		},
		{
			name: "When a pod is unschedulable it should return sanitized message with resource types only",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: componentName, Namespace: namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": componentName},
					},
					Replicas: ptr.To[int32](1),
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-ghi", Namespace: namespace,
						Labels: map[string]string{"app": componentName},
					},
					Status: corev1.PodStatus{
						Conditions: []corev1.PodCondition{
							{
								Type:    corev1.PodScheduled,
								Status:  corev1.ConditionFalse,
								Reason:  "Unschedulable",
								Message: "0/3 nodes are available: 1 Insufficient cpu, 2 Insufficient memory. preemption: 0/3 nodes are available: 3 No preemption victims found for incoming pod.",
							},
						},
					},
				},
			},
			expectedReason: hyperv1.ReasonPodSchedulingFailed,
			expectedMsg:    "Pod test-component-ghi is unschedulable: Insufficient cpu, Insufficient memory",
		},
		{
			name: "When all pods are healthy it should return empty reason",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: componentName, Namespace: namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": componentName},
					},
					Replicas: ptr.To[int32](1),
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-jkl", Namespace: namespace,
						Labels: map[string]string{"app": componentName},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
						ContainerStatuses: []corev1.ContainerStatus{
							{
								Name:  "main",
								Ready: true,
								State: corev1.ContainerState{
									Running: &corev1.ContainerStateRunning{},
								},
							},
						},
					},
				},
			},
			expectedReason: "",
			expectedMsg:    "",
		},
		{
			name:           "When the deployment does not exist it should return empty reason",
			pods:           []*corev1.Pod{},
			expectedReason: "",
			expectedMsg:    "",
		},
		{
			name: "When an init container has CrashLoopBackOff it should return CrashLoopBackOff reason",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: componentName, Namespace: namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": componentName},
					},
					Replicas: ptr.To[int32](1),
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-init", Namespace: namespace,
						Labels: map[string]string{"app": componentName},
					},
					Status: corev1.PodStatus{
						InitContainerStatuses: []corev1.ContainerStatus{
							{
								Name: "init-setup",
								State: corev1.ContainerState{
									Waiting: &corev1.ContainerStateWaiting{
										Reason: "CrashLoopBackOff",
									},
								},
							},
						},
					},
				},
			},
			expectedReason: hyperv1.ReasonCrashLoopBackOff,
			expectedMsg:    "init container",
		},
		{
			name: "When scheduling message has no resource types it should omit details",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: componentName, Namespace: namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": componentName},
					},
					Replicas: ptr.To[int32](1),
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-nores", Namespace: namespace,
						Labels: map[string]string{"app": componentName},
					},
					Status: corev1.PodStatus{
						Conditions: []corev1.PodCondition{
							{
								Type:    corev1.PodScheduled,
								Status:  corev1.ConditionFalse,
								Reason:  "Unschedulable",
								Message: "0/3 nodes are available: node(s) had taint {key: value}, that the pod didn't tolerate.",
							},
						},
					},
				},
			},
			expectedReason: hyperv1.ReasonPodSchedulingFailed,
			expectedMsg:    "Pod test-component-nores is unschedulable",
		},
		{
			name: "When CrashLoopBackOff message exceeds max length it should be truncated",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: componentName, Namespace: namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": componentName},
					},
					Replicas: ptr.To[int32](1),
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-" + strings.Repeat("x", 300), Namespace: namespace,
						Labels: map[string]string{"app": componentName},
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								Name: "main",
								State: corev1.ContainerState{
									Waiting: &corev1.ContainerStateWaiting{
										Reason: "CrashLoopBackOff",
									},
								},
							},
						},
					},
				},
			},
			expectedReason: hyperv1.ReasonCrashLoopBackOff,
			expectedMsg:    "...",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			builder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.deployment != nil {
				builder = builder.WithObjects(tc.deployment)
			}
			for _, pod := range tc.pods {
				builder = builder.WithObjects(pod)
			}
			c := builder.Build()

			cpContext := ControlPlaneContext{
				Context: context.Background(),
				Client:  c,
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
					},
				},
			}

			reason, msg := diagnosePodFailure(cpContext, componentName)
			g.Expect(reason).To(Equal(tc.expectedReason))
			if tc.expectedMsg != "" {
				g.Expect(msg).To(ContainSubstring(tc.expectedMsg))
			}
			if tc.expectedMsg == "..." {
				g.Expect(len(msg)).To(BeNumerically("<=", maxDiagnosticMessageLen))
			}
			// Scheduling failure messages must not leak management cluster details.
			if tc.expectedReason == hyperv1.ReasonPodSchedulingFailed {
				g.Expect(msg).NotTo(ContainSubstring("nodes are available"))
				g.Expect(msg).NotTo(ContainSubstring("preemption"))
			}
		})
	}
}

func TestSanitizeSchedulerMessage(t *testing.T) {
	testCases := []struct {
		name     string
		podName  string
		raw      string
		expected string
	}{
		{
			name:     "When message has Insufficient cpu and memory it should extract both",
			podName:  "kube-apiserver-abc",
			raw:      "0/3 nodes are available: 1 Insufficient cpu, 2 Insufficient memory. preemption: 0/3 nodes are available: 3 No preemption victims found for incoming pod.",
			expected: "Pod kube-apiserver-abc is unschedulable: Insufficient cpu, Insufficient memory",
		},
		{
			name:     "When message has no Insufficient patterns it should return generic message",
			podName:  "etcd-0",
			raw:      "0/3 nodes are available: node(s) had taint {key: value}, that the pod didn't tolerate.",
			expected: "Pod etcd-0 is unschedulable",
		},
		{
			name:     "When message has duplicate resource types it should deduplicate",
			podName:  "kas-pod",
			raw:      "0/6 nodes are available: 3 Insufficient cpu, 3 Insufficient cpu.",
			expected: "Pod kas-pod is unschedulable: Insufficient cpu",
		},
		{
			name:     "When message has single resource type it should return it",
			podName:  "oauth-server-xyz",
			raw:      "0/1 nodes are available: 1 Insufficient memory.",
			expected: "Pod oauth-server-xyz is unschedulable: Insufficient memory",
		},
		{
			name:     "When message is empty it should return generic message",
			podName:  "pod-abc",
			raw:      "",
			expected: "Pod pod-abc is unschedulable",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := sanitizeSchedulerMessage(tc.podName, tc.raw)
			g.Expect(result).To(Equal(tc.expected))
			// Must never contain node counts or topology details.
			g.Expect(result).NotTo(ContainSubstring("nodes are available"))
			g.Expect(result).NotTo(ContainSubstring("preemption"))
		})
	}
}

func TestSanitizeSchedulerMessageSecuritySanitization(t *testing.T) {
	testCases := []struct {
		name           string
		raw            string
		mustNotContain []string
		mustContain    string
	}{
		{
			name:           "AWS node names must not appear",
			raw:            "0/6 nodes are available: 3 Insufficient cpu. ip-10-0-1-42.ec2.internal had untolerated taint, ip-10-0-2-99.ec2.internal had untolerated taint.",
			mustNotContain: []string{"ip-10-0-1-42", "ip-10-0-2-99", "ec2.internal"},
			mustContain:    "Insufficient cpu",
		},
		{
			name:           "Azure VMSS node names must not appear",
			raw:            "0/3 nodes are available: 2 Insufficient memory. aks-nodepool1-12345678-vmss000000 had untolerated taint.",
			mustNotContain: []string{"aks-nodepool1-12345678-vmss000000", "vmss"},
			mustContain:    "Insufficient memory",
		},
		{
			name:           "GCP node names must not appear",
			raw:            "0/4 nodes are available: 2 Insufficient cpu. gke-mgmt-cluster-pool-0-a1b2c3d4-wxyz had untolerated taint.",
			mustNotContain: []string{"gke-mgmt-cluster", "a1b2c3d4"},
			mustContain:    "Insufficient cpu",
		},
		{
			name:           "OpenShift worker FQDNs must not appear",
			raw:            "0/5 nodes are available: 1 Insufficient cpu. worker-0.mgmt.ocp.example.com had untolerated taint, master-2.mgmt.ocp.example.com had untolerated taint.",
			mustNotContain: []string{"worker-0.mgmt", "master-2.mgmt", "ocp.example.com"},
			mustContain:    "Insufficient cpu",
		},
		{
			name:           "IPv4 addresses must not appear",
			raw:            "0/3 nodes are available: 2 Insufficient cpu. 10.0.1.42 had taint, 172.16.0.5 had taint, 192.168.1.100 had taint.",
			mustNotContain: []string{"10.0.1.42", "172.16.0.5", "192.168.1.100"},
			mustContain:    "Insufficient cpu",
		},
		{
			name:           "IPv6 addresses must not appear",
			raw:            "0/3 nodes are available: 1 Insufficient memory. fd00::1 had taint, 2001:db8::abcd:1234 had taint.",
			mustNotContain: []string{"fd00::1", "2001:db8::abcd:1234"},
			mustContain:    "Insufficient memory",
		},
		{
			name:           "Management cluster node count must not appear",
			raw:            "0/42 nodes are available: 10 Insufficient cpu, 32 Insufficient memory.",
			mustNotContain: []string{"0/42 nodes"},
			mustContain:    "Insufficient cpu",
		},
		{
			name:           "Preemption details must not appear",
			raw:            "0/3 nodes are available: 3 Insufficient cpu. preemption: 0/3 nodes are available: 3 No preemption victims found for incoming pod.",
			mustNotContain: []string{"preemption", "No preemption victims"},
			mustContain:    "Insufficient cpu",
		},
		{
			name:           "Taint details must not appear",
			raw:            "0/3 nodes are available: 1 Insufficient cpu, 2 node(s) had untolerated taint {node-role.kubernetes.io/master: }.",
			mustNotContain: []string{"untolerated taint", "node-role.kubernetes.io"},
			mustContain:    "Insufficient cpu",
		},
		{
			name:           "Registry hostnames must not appear",
			raw:            "0/6 nodes are available: 3 Insufficient memory. quay.io/openshift-release-dev image pull failed on registry.ci.openshift.org.",
			mustNotContain: []string{"quay.io", "registry.ci.openshift.org"},
			mustContain:    "Insufficient memory",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := sanitizeSchedulerMessage("test-pod", tc.raw)
			for _, forbidden := range tc.mustNotContain {
				g.Expect(result).NotTo(ContainSubstring(forbidden),
					"sanitized message %q should not contain %q", result, forbidden)
			}
			if tc.mustContain != "" {
				g.Expect(result).To(ContainSubstring(tc.mustContain),
					"sanitized message %q should contain %q", result, tc.mustContain)
			}
		})
	}
}

func TestTruncateMessage(t *testing.T) {
	g := NewGomegaWithT(t)

	short := "short message"
	g.Expect(truncateMessage(short)).To(Equal(short))

	long := strings.Repeat("a", 300)
	result := truncateMessage(long)
	g.Expect(len(result)).To(Equal(maxDiagnosticMessageLen))
	g.Expect(result).To(HaveSuffix("..."))
}
