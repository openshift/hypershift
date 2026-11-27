package podspec

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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testDeploymentWithRS(namespace, componentName, revision, hash string) (*appsv1.Deployment, *appsv1.ReplicaSet) {
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        componentName,
			Namespace:   namespace,
			UID:         types.UID("deploy-" + componentName),
			Annotations: map[string]string{deploymentRevisionAnnotation: revision},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": componentName},
			},
			Replicas: ptr.To[int32](1),
		},
	}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        componentName + "-" + hash,
			Namespace:   namespace,
			Labels:      map[string]string{"app": componentName, "pod-template-hash": hash},
			Annotations: map[string]string{deploymentRevisionAnnotation: revision},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       componentName,
					UID:        deploy.UID,
					Controller: ptr.To(true),
				},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": componentName, "pod-template-hash": hash},
			},
		},
	}
	return deploy, rs
}

func TestDiagnoseWorkloadPodFailure(t *testing.T) {
	namespace := "test-ns"
	componentName := "test-component"

	defaultDeploy, defaultRS := testDeploymentWithRS(namespace, componentName, "1", "rev1")

	oldRS := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        componentName + "-old",
			Namespace:   namespace,
			Labels:      map[string]string{"app": componentName, "pod-template-hash": "old"},
			Annotations: map[string]string{deploymentRevisionAnnotation: "1"},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       componentName,
					UID:        types.UID("deploy-" + componentName),
					Controller: ptr.To(true),
				},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": componentName, "pod-template-hash": "old"},
			},
		},
	}
	rev2Deploy, rev2RS := testDeploymentWithRS(namespace, componentName, "2", "new")

	testCases := []struct {
		name           string
		workload       client.Object
		replicaSets    []*appsv1.ReplicaSet
		pods           []*corev1.Pod
		expectedReason string
		expectedMsg    string
	}{
		{
			name:        "When a pod has CrashLoopBackOff, it should return CrashLoopBackOff reason",
			workload:    defaultDeploy,
			replicaSets: []*appsv1.ReplicaSet{defaultRS},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-abc", Namespace: namespace,
						Labels: map[string]string{"app": componentName, "pod-template-hash": "rev1"},
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
			expectedReason: hyperv1.CrashLoopBackOffReason,
			expectedMsg:    "CrashLoopBackOff",
		},
		{
			name:        "When a pod has ImagePullBackOff, it should return ImagePullBackOff reason",
			workload:    defaultDeploy,
			replicaSets: []*appsv1.ReplicaSet{defaultRS},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-def", Namespace: namespace,
						Labels: map[string]string{"app": componentName, "pod-template-hash": "rev1"},
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
			expectedReason: hyperv1.ImagePullBackOffReason,
			expectedMsg:    "ImagePullBackOff",
		},
		{
			name:        "When a pod is unschedulable, it should return sanitized message with resource types only",
			workload:    defaultDeploy,
			replicaSets: []*appsv1.ReplicaSet{defaultRS},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-ghi", Namespace: namespace,
						Labels: map[string]string{"app": componentName, "pod-template-hash": "rev1"},
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
			expectedReason: hyperv1.PodSchedulingFailedReason,
			expectedMsg:    "Pod test-component-ghi is unschedulable: Insufficient cpu, Insufficient memory",
		},
		{
			name:        "When pod-0 is in CrashLoopBackOff and pod-1 is Unschedulable in the same revision, it should prioritize PodSchedulingFailed",
			workload:    defaultDeploy,
			replicaSets: []*appsv1.ReplicaSet{defaultRS},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-0", Namespace: namespace,
						Labels: map[string]string{"app": componentName, "pod-template-hash": "rev1"},
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
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-1", Namespace: namespace,
						Labels: map[string]string{"app": componentName, "pod-template-hash": "rev1"},
					},
					Status: corev1.PodStatus{
						Conditions: []corev1.PodCondition{
							{
								Type:    corev1.PodScheduled,
								Status:  corev1.ConditionFalse,
								Reason:  "Unschedulable",
								Message: "0/3 nodes are available: 3 Insufficient cpu.",
							},
						},
					},
				},
			},
			expectedReason: hyperv1.PodSchedulingFailedReason,
			expectedMsg:    "Pod test-component-1 is unschedulable: Insufficient cpu",
		},
		{
			name:        "When old ReplicaSet pod is CrashLoopBackOff while new ReplicaSet pod is starting, it should return empty reason",
			workload:    rev2Deploy,
			replicaSets: []*appsv1.ReplicaSet{oldRS, rev2RS},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-old-0", Namespace: namespace,
						Labels: map[string]string{"app": componentName, "pod-template-hash": "old"},
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
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-new-0", Namespace: namespace,
						Labels: map[string]string{"app": componentName, "pod-template-hash": "new"},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodPending,
					},
				},
			},
			expectedReason: "",
			expectedMsg:    "",
		},
		{
			name:        "When old ReplicaSet pod is Unschedulable while new ReplicaSet pod is CrashLoopBackOff, it should report the new ReplicaSet failure",
			workload:    rev2Deploy,
			replicaSets: []*appsv1.ReplicaSet{oldRS, rev2RS},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-old-0", Namespace: namespace,
						Labels: map[string]string{"app": componentName, "pod-template-hash": "old"},
					},
					Status: corev1.PodStatus{
						Conditions: []corev1.PodCondition{
							{
								Type:    corev1.PodScheduled,
								Status:  corev1.ConditionFalse,
								Reason:  "Unschedulable",
								Message: "0/3 nodes are available: 3 Insufficient cpu.",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-new-0", Namespace: namespace,
						Labels: map[string]string{"app": componentName, "pod-template-hash": "new"},
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
			expectedReason: hyperv1.CrashLoopBackOffReason,
			expectedMsg:    "Pod test-component-new-0 container main: CrashLoopBackOff",
		},
		{
			name: "When Deployment is already ready, it should short-circuit and return empty reason",
			workload: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:        componentName,
					Namespace:   namespace,
					UID:         defaultDeploy.UID,
					Generation:  1,
					Annotations: map[string]string{deploymentRevisionAnnotation: "1"},
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To[int32](1),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": componentName},
					},
				},
				Status: appsv1.DeploymentStatus{
					ObservedGeneration: 1,
					Replicas:           1,
					UpdatedReplicas:    1,
					ReadyReplicas:      1,
					AvailableReplicas:  1,
				},
			},
			replicaSets: []*appsv1.ReplicaSet{defaultRS},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-abc", Namespace: namespace,
						Labels: map[string]string{"app": componentName, "pod-template-hash": "rev1"},
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
			expectedReason: "",
			expectedMsg:    "",
		},
		{
			name: "When Deployment has no revision annotation, it should return empty reason",
			workload: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name: componentName, Namespace: namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To[int32](1),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": componentName},
					},
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
							},
						},
					},
				},
			},
			expectedReason: "",
			expectedMsg:    "",
		},
		{
			name:        "When all pods are healthy, it should return empty reason",
			workload:    defaultDeploy,
			replicaSets: []*appsv1.ReplicaSet{defaultRS},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-jkl", Namespace: namespace,
						Labels: map[string]string{"app": componentName, "pod-template-hash": "rev1"},
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
			name: "When a StatefulSet current revision pod has CrashLoopBackOff, it should return CrashLoopBackOff reason",
			workload: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name: componentName, Namespace: namespace,
				},
				Spec: appsv1.StatefulSetSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": componentName},
					},
					Replicas: ptr.To[int32](1),
				},
				Status: appsv1.StatefulSetStatus{
					UpdateRevision: "etcd-rev2",
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-0", Namespace: namespace,
						Labels: map[string]string{
							"app":                                 componentName,
							appsv1.ControllerRevisionHashLabelKey: "etcd-rev2",
						},
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
			expectedReason: hyperv1.CrashLoopBackOffReason,
			expectedMsg:    "CrashLoopBackOff",
		},
		{
			name: "When a StatefulSet old revision pod is Unschedulable while new revision pod is starting, it should return empty reason",
			workload: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name: componentName, Namespace: namespace,
				},
				Spec: appsv1.StatefulSetSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": componentName},
					},
					Replicas: ptr.To[int32](2),
				},
				Status: appsv1.StatefulSetStatus{
					CurrentRevision: "etcd-rev1",
					UpdateRevision:  "etcd-rev2",
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-0", Namespace: namespace,
						Labels: map[string]string{
							"app":                                 componentName,
							appsv1.ControllerRevisionHashLabelKey: "etcd-rev1",
						},
					},
					Status: corev1.PodStatus{
						Conditions: []corev1.PodCondition{
							{
								Type:    corev1.PodScheduled,
								Status:  corev1.ConditionFalse,
								Reason:  "Unschedulable",
								Message: "0/3 nodes are available: 3 Insufficient cpu.",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-1", Namespace: namespace,
						Labels: map[string]string{
							"app":                                 componentName,
							appsv1.ControllerRevisionHashLabelKey: "etcd-rev2",
						},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodPending,
					},
				},
			},
			expectedReason: "",
			expectedMsg:    "",
		},
		{
			name: "When a StatefulSet is already ready, it should short-circuit and return empty reason",
			workload: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       componentName,
					Namespace:  namespace,
					Generation: 1,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](1),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": componentName},
					},
				},
				Status: appsv1.StatefulSetStatus{
					ObservedGeneration: 1,
					Replicas:           1,
					UpdatedReplicas:    1,
					ReadyReplicas:      1,
					AvailableReplicas:  1,
					UpdateRevision:     "etcd-rev2",
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-0", Namespace: namespace,
						Labels: map[string]string{
							"app":                                 componentName,
							appsv1.ControllerRevisionHashLabelKey: "etcd-rev2",
						},
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
			expectedReason: "",
			expectedMsg:    "",
		},
		{
			name: "When a StatefulSet has no revision in status, it should return empty reason",
			workload: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name: componentName, Namespace: namespace,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](1),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": componentName},
					},
				},
			},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-0", Namespace: namespace,
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
			expectedReason: "",
			expectedMsg:    "",
		},
		{
			name:        "When an init container has CrashLoopBackOff, it should return CrashLoopBackOff reason",
			workload:    defaultDeploy,
			replicaSets: []*appsv1.ReplicaSet{defaultRS},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-init", Namespace: namespace,
						Labels: map[string]string{"app": componentName, "pod-template-hash": "rev1"},
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
			expectedReason: hyperv1.CrashLoopBackOffReason,
			expectedMsg:    "init container",
		},
		{
			name:        "When scheduling message has no resource types, it should omit details",
			workload:    defaultDeploy,
			replicaSets: []*appsv1.ReplicaSet{defaultRS},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-nores", Namespace: namespace,
						Labels: map[string]string{"app": componentName, "pod-template-hash": "rev1"},
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
			expectedReason: hyperv1.PodSchedulingFailedReason,
			expectedMsg:    "Pod test-component-nores is unschedulable",
		},
		{
			name:        "When CrashLoopBackOff message exceeds max length, it should be truncated",
			workload:    defaultDeploy,
			replicaSets: []*appsv1.ReplicaSet{defaultRS},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: componentName + "-" + strings.Repeat("x", 300), Namespace: namespace,
						Labels: map[string]string{"app": componentName, "pod-template-hash": "rev1"},
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
			expectedReason: hyperv1.CrashLoopBackOffReason,
			expectedMsg:    "...",
		},
		{
			name:        "When a terminating pod has a failure, it should be skipped in favor of active pods",
			workload:    defaultDeploy,
			replicaSets: []*appsv1.ReplicaSet{defaultRS},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              componentName + "-aaa-terminating",
						Namespace:         namespace,
						Labels:            map[string]string{"app": componentName, "pod-template-hash": "rev1"},
						DeletionTimestamp: &metav1.Time{Time: metav1.Now().Time},
						Finalizers:        []string{"test-finalizer"},
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
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      componentName + "-zzz-active",
						Namespace: namespace,
						Labels:    map[string]string{"app": componentName, "pod-template-hash": "rev1"},
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
			expectedReason: hyperv1.ImagePullBackOffReason,
			expectedMsg:    "Pod test-component-zzz-active container main: ImagePullBackOff",
		},
		{
			name:        "When completed or failed pods exist, they should be skipped",
			workload:    defaultDeploy,
			replicaSets: []*appsv1.ReplicaSet{defaultRS},
			pods: []*corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      componentName + "-aaa-failed",
						Namespace: namespace,
						Labels:    map[string]string{"app": componentName, "pod-template-hash": "rev1"},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodFailed,
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
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      componentName + "-bbb-succeeded",
						Namespace: namespace,
						Labels:    map[string]string{"app": componentName, "pod-template-hash": "rev1"},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
					},
				},
			},
			expectedReason: "",
			expectedMsg:    "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			builder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.workload != nil {
				builder = builder.WithObjects(tc.workload)
			}
			for _, rs := range tc.replicaSets {
				builder = builder.WithObjects(rs)
			}
			for _, pod := range tc.pods {
				builder = builder.WithObjects(pod)
			}
			c := builder.Build()

			reason, msg := DiagnoseWorkloadPodFailure(context.Background(), c, tc.workload)
			g.Expect(reason).To(Equal(tc.expectedReason))
			if tc.expectedMsg != "" {
				g.Expect(msg).To(ContainSubstring(tc.expectedMsg))
			}
			if tc.expectedMsg == "..." {
				g.Expect(len(msg)).To(BeNumerically("<=", maxDiagnosticMessageLen))
			}
			// Scheduling failure messages must not leak management cluster details.
			if tc.expectedReason == hyperv1.PodSchedulingFailedReason {
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
			name:     "When message has Insufficient cpu and memory, it should extract both",
			podName:  "kube-apiserver-abc",
			raw:      "0/3 nodes are available: 1 Insufficient cpu, 2 Insufficient memory. preemption: 0/3 nodes are available: 3 No preemption victims found for incoming pod.",
			expected: "Pod kube-apiserver-abc is unschedulable: Insufficient cpu, Insufficient memory",
		},
		{
			name:     "When message has no Insufficient patterns, it should return generic message",
			podName:  "etcd-0",
			raw:      "0/3 nodes are available: node(s) had taint {key: value}, that the pod didn't tolerate.",
			expected: "Pod etcd-0 is unschedulable",
		},
		{
			name:     "When message has duplicate resource types, it should deduplicate",
			podName:  "kas-pod",
			raw:      "0/6 nodes are available: 3 Insufficient cpu, 3 Insufficient cpu.",
			expected: "Pod kas-pod is unschedulable: Insufficient cpu",
		},
		{
			name:     "When message has single resource type, it should return it",
			podName:  "oauth-server-xyz",
			raw:      "0/1 nodes are available: 1 Insufficient memory.",
			expected: "Pod oauth-server-xyz is unschedulable: Insufficient memory",
		},
		{
			name:     "When message is empty, it should return generic message",
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

	securityCases := []struct {
		name           string
		raw            string
		mustNotContain []string
		mustContain    string
	}{
		{
			name:           "When message contains AWS node names, they should not appear in output",
			raw:            "0/6 nodes are available: 3 Insufficient cpu. ip-10-0-1-42.ec2.internal had untolerated taint, ip-10-0-2-99.ec2.internal had untolerated taint.",
			mustNotContain: []string{"ip-10-0-1-42", "ip-10-0-2-99", "ec2.internal"},
			mustContain:    "Insufficient cpu",
		},
		{
			name:           "When message contains Azure VMSS node names, they should not appear in output",
			raw:            "0/3 nodes are available: 2 Insufficient memory. aks-nodepool1-12345678-vmss000000 had untolerated taint.",
			mustNotContain: []string{"aks-nodepool1-12345678-vmss000000", "vmss"},
			mustContain:    "Insufficient memory",
		},
		{
			name:           "When message contains GCP node names, they should not appear in output",
			raw:            "0/4 nodes are available: 2 Insufficient cpu. gke-mgmt-cluster-pool-0-a1b2c3d4-wxyz had untolerated taint.",
			mustNotContain: []string{"gke-mgmt-cluster", "a1b2c3d4"},
			mustContain:    "Insufficient cpu",
		},
		{
			name:           "When message contains OpenShift worker FQDNs, they should not appear in output",
			raw:            "0/5 nodes are available: 1 Insufficient cpu. worker-0.mgmt.ocp.example.com had untolerated taint, master-2.mgmt.ocp.example.com had untolerated taint.",
			mustNotContain: []string{"worker-0.mgmt", "master-2.mgmt", "ocp.example.com"},
			mustContain:    "Insufficient cpu",
		},
		{
			name:           "When message contains IPv4 addresses, they should not appear in output",
			raw:            "0/3 nodes are available: 2 Insufficient cpu. 10.0.1.42 had taint, 172.16.0.5 had taint, 192.168.1.100 had taint.",
			mustNotContain: []string{"10.0.1.42", "172.16.0.5", "192.168.1.100"},
			mustContain:    "Insufficient cpu",
		},
		{
			name:           "When message contains IPv6 addresses, they should not appear in output",
			raw:            "0/3 nodes are available: 1 Insufficient memory. fd00::1 had taint, 2001:db8::abcd:1234 had taint.",
			mustNotContain: []string{"fd00::1", "2001:db8::abcd:1234"},
			mustContain:    "Insufficient memory",
		},
		{
			name:           "When message contains management cluster node count, it should not appear in output",
			raw:            "0/42 nodes are available: 10 Insufficient cpu, 32 Insufficient memory.",
			mustNotContain: []string{"0/42 nodes"},
			mustContain:    "Insufficient cpu",
		},
		{
			name:           "When message contains preemption details, they should not appear in output",
			raw:            "0/3 nodes are available: 3 Insufficient cpu. preemption: 0/3 nodes are available: 3 No preemption victims found for incoming pod.",
			mustNotContain: []string{"preemption", "No preemption victims"},
			mustContain:    "Insufficient cpu",
		},
		{
			name:           "When message contains taint details, they should not appear in output",
			raw:            "0/3 nodes are available: 1 Insufficient cpu, 2 node(s) had untolerated taint {node-role.kubernetes.io/master: }.",
			mustNotContain: []string{"untolerated taint", "node-role.kubernetes.io"},
			mustContain:    "Insufficient cpu",
		},
		{
			name:           "When message contains registry hostnames, they should not appear in output",
			raw:            "0/6 nodes are available: 3 Insufficient memory. quay.io/openshift-release-dev image pull failed on registry.ci.openshift.org.",
			mustNotContain: []string{"quay.io", "registry.ci.openshift.org"},
			mustContain:    "Insufficient memory",
		},
	}

	for _, tc := range securityCases {
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
