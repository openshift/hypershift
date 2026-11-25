package controlplanecomponent

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/testutil"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testVersion = "4.18.0"
)

func TestReconcileComponentStatus(t *testing.T) {
	g := NewGomegaWithT(t)

	componentName := "test-component"
	namespace := "test-namespace"

	testCases := []struct {
		name                    string
		deployment              *appsv1.Deployment
		unmetPreconditions      []hyperv1.ConditionType
		unavailableDependencies []string
		reconciliationError     error
		expectedConditions      []metav1.Condition
		expectedVersion         string
	}{
		{
			name: "should set component ready and version when deployment is available and no dependencies",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      componentName,
					Namespace: namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To[int32](1),
				},
				Status: appsv1.DeploymentStatus{
					AvailableReplicas: 1,
					ReadyReplicas:     1,
					Replicas:          1,
					UpdatedReplicas:   1,
					Conditions: []appsv1.DeploymentCondition{
						{
							Type:   appsv1.DeploymentAvailable,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			unavailableDependencies: nil,
			reconciliationError:     nil,
			expectedConditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ControlPlaneComponentAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:   string(hyperv1.ControlPlaneComponentRolloutComplete),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			expectedVersion: testVersion,
		},
		{
			name: "should block rollout when dependencies not satisfied",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      componentName,
					Namespace: namespace,
				},
				Status: appsv1.DeploymentStatus{
					AvailableReplicas: 1,
					ReadyReplicas:     1,
					Replicas:          1,
					UpdatedReplicas:   1,
					Conditions: []appsv1.DeploymentCondition{
						{
							Type:   appsv1.DeploymentAvailable,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			unavailableDependencies: []string{"dependency1", "dependency2"},
			reconciliationError:     nil,
			expectedConditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ControlPlaneComponentAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.WaitingForDependenciesReason,
					Message: "Waiting for Dependencies: dependency1, dependency2",
				},
			},
			expectedVersion: "",
		},
		{
			name: "should report error when reconciliation fails",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      componentName,
					Namespace: namespace,
				},
				Status: appsv1.DeploymentStatus{
					AvailableReplicas: 1,
					ReadyReplicas:     1,
					Replicas:          1,
					UpdatedReplicas:   1,
				},
			},
			unavailableDependencies: nil,
			reconciliationError:     fmt.Errorf("failed to reconcile"),
			expectedConditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ControlPlaneComponentAvailable),
					Status: metav1.ConditionFalse,
					Reason: hyperv1.NotFoundReason,
				},
				{
					Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.ReconciliationErrorReason,
					Message: "failed to reconcile",
				},
			},
			expectedVersion: "",
		},
		{
			name: "should not set version when workload not ready",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      componentName,
					Namespace: namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.DeploymentStatus{
					// Only 2 out of 3 replicas are ready
					AvailableReplicas: 2,
					ReadyReplicas:     2,
					Replicas:          3,
					UpdatedReplicas:   3,
					Conditions: []appsv1.DeploymentCondition{
						{
							Type:   appsv1.DeploymentAvailable,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			unavailableDependencies: nil,
			reconciliationError:     nil,
			expectedConditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ControlPlaneComponentAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:   string(hyperv1.ControlPlaneComponentRolloutComplete),
					Status: metav1.ConditionFalse,
					Reason: "WaitingForRolloutComplete",
				},
			},
			expectedVersion: "",
		},
		{
			name: "should block rollout with precondition message when precondition is unmet",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      componentName,
					Namespace: namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.DeploymentStatus{
					AvailableReplicas: 3,
					ReadyReplicas:     3,
					Replicas:          3,
					UpdatedReplicas:   3,
					Conditions: []appsv1.DeploymentCondition{
						{
							Type:   appsv1.DeploymentAvailable,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			unmetPreconditions: []hyperv1.ConditionType{
				hyperv1.ConfigOperatorReconciliationSucceeded,
			},
			reconciliationError: nil,
			expectedConditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ControlPlaneComponentAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.WaitingForPreconditionsReason,
					Message: "Waiting for HCP conditions: ConfigOperatorReconciliationSucceeded",
				},
			},
			expectedVersion: "",
		},
		{
			name: "precondition takes priority over unavailable dependency in message",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      componentName,
					Namespace: namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.DeploymentStatus{
					AvailableReplicas: 3,
					ReadyReplicas:     3,
					Replicas:          3,
					UpdatedReplicas:   3,
					Conditions: []appsv1.DeploymentCondition{
						{
							Type:   appsv1.DeploymentAvailable,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			unmetPreconditions: []hyperv1.ConditionType{
				hyperv1.ConfigOperatorReconciliationSucceeded,
			},
			unavailableDependencies: []string{"some-dependency"},
			reconciliationError:     nil,
			expectedConditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ControlPlaneComponentAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.WaitingForPreconditionsReason,
					Message: "Waiting for HCP conditions: ConfigOperatorReconciliationSucceeded",
				},
			},
			expectedVersion: "",
		},
		{
			name: "should join all unmet preconditions in the message",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      componentName,
					Namespace: namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.DeploymentStatus{
					AvailableReplicas: 3,
					ReadyReplicas:     3,
					Replicas:          3,
					UpdatedReplicas:   3,
					Conditions: []appsv1.DeploymentCondition{
						{
							Type:   appsv1.DeploymentAvailable,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			unmetPreconditions: []hyperv1.ConditionType{
				hyperv1.ConfigOperatorReconciliationSucceeded,
				hyperv1.EtcdSnapshotRestored,
			},
			reconciliationError: nil,
			expectedConditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ControlPlaneComponentAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.WaitingForPreconditionsReason,
					Message: "Waiting for HCP conditions: ConfigOperatorReconciliationSucceeded, EtcdSnapshotRestored",
				},
			},
			expectedVersion: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set up the test context
			client := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(tc.deployment).
				Build()

			var hcpGeneration int64 = 5
			cpContext := ControlPlaneContext{
				Context: context.Background(),
				Client:  client,
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:  namespace,
						Generation: hcpGeneration,
					},
				},
				ReleaseImageProvider: testutil.FakeImageProvider(),
			}

			// Create the component to test
			component := NewDeploymentComponent(componentName, nil)
			workload := component.Build().(*controlPlaneWorkload[*appsv1.Deployment])

			// Create the component status object
			componentStatus := &hyperv1.ControlPlaneComponent{
				ObjectMeta: metav1.ObjectMeta{
					Name:      componentName,
					Namespace: namespace,
				},
				Status: hyperv1.ControlPlaneComponentStatus{},
			}

			// Run reconcileComponentStatus
			err := workload.reconcileComponentStatus(cpContext, componentStatus, tc.unmetPreconditions, tc.unavailableDependencies, tc.reconciliationError)
			g.Expect(err).NotTo(HaveOccurred())

			// Check conditions
			g.Expect(componentStatus.Status.Conditions).To(HaveLen(len(tc.expectedConditions)))
			for _, expectedCond := range tc.expectedConditions {
				actualCond := meta.FindStatusCondition(componentStatus.Status.Conditions, expectedCond.Type)
				g.Expect(actualCond).NotTo(BeNil())
				g.Expect(actualCond.Status).To(Equal(expectedCond.Status))
				g.Expect(actualCond.Reason).To(Equal(expectedCond.Reason))
				if expectedCond.Message != "" {
					g.Expect(actualCond.Message).To(Equal(expectedCond.Message))
				}
			}

			// Check version
			g.Expect(componentStatus.Status.Version).To(Equal(tc.expectedVersion))

			// Check observedGeneration is always set to the HCP generation
			g.Expect(componentStatus.Status.ObservedGeneration).To(Equal(hcpGeneration))
		})
	}

	t.Run("should set RolloutComplete with PodSchedulingFailed and append diagnosis when a pod is unschedulable", func(t *testing.T) {
		g := NewGomegaWithT(t)

		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:        componentName,
				Namespace:   namespace,
				UID:         "deploy-uid-1",
				Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"},
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: ptr.To[int32](3),
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": componentName},
				},
			},
			Status: appsv1.DeploymentStatus{
				AvailableReplicas: 2,
				ReadyReplicas:     2,
				Replicas:          3,
				UpdatedReplicas:   3,
				Conditions: []appsv1.DeploymentCondition{
					{
						Type:   appsv1.DeploymentAvailable,
						Status: corev1.ConditionTrue,
					},
				},
			},
		}

		currentRS := &appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:        componentName + "-rev2",
				Namespace:   namespace,
				Labels:      map[string]string{"app": componentName, "pod-template-hash": "rev2"},
				Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       componentName,
						UID:        deployment.UID,
						Controller: ptr.To(true),
					},
				},
			},
			Spec: appsv1.ReplicaSetSpec{
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": componentName, "pod-template-hash": "rev2"},
				},
			},
		}

		unschedulablePod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      componentName + "-abc",
				Namespace: namespace,
				Labels:    map[string]string{"app": componentName, "pod-template-hash": "rev2"},
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
		}

		c := fake.NewClientBuilder().
			WithScheme(api.Scheme).
			WithObjects(deployment, currentRS, unschedulablePod).
			Build()

		cpContext := ControlPlaneContext{
			Context: context.Background(),
			Client:  c,
			HCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  namespace,
					Generation: 5,
				},
			},
			ReleaseImageProvider: testutil.FakeImageProvider(),
		}

		component := NewDeploymentComponent(componentName, nil)
		workload := component.Build().(*controlPlaneWorkload[*appsv1.Deployment])

		componentStatus := &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{
				Name:      componentName,
				Namespace: namespace,
			},
		}

		err := workload.reconcileComponentStatus(cpContext, componentStatus, nil, nil, nil)
		g.Expect(err).NotTo(HaveOccurred())

		rollout := meta.FindStatusCondition(componentStatus.Status.Conditions, string(hyperv1.ControlPlaneComponentRolloutComplete))
		g.Expect(rollout).NotTo(BeNil())
		g.Expect(rollout.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(rollout.Reason).To(Equal(hyperv1.PodSchedulingFailedReason))
		// Preserves rollout context and appends pod diagnosis.
		g.Expect(rollout.Message).To(ContainSubstring("Waiting for deployment " + componentName + " rollout to finish"))
		g.Expect(rollout.Message).To(ContainSubstring("; Pod " + componentName + "-abc is unschedulable: Insufficient cpu"))
		// Must not leak management cluster details.
		g.Expect(rollout.Message).NotTo(ContainSubstring("nodes are available"))
	})

	t.Run("should immediately diagnose when transitioning from healthy to unhealthy and preserve distinct messages", func(t *testing.T) {
		g := NewGomegaWithT(t)

		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:        componentName,
				Namespace:   namespace,
				UID:         "deploy-uid-2",
				Annotations: map[string]string{"deployment.kubernetes.io/revision": "3"},
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: ptr.To[int32](3),
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": componentName},
				},
			},
			Status: appsv1.DeploymentStatus{
				AvailableReplicas: 0,
				ReadyReplicas:     0,
				Replicas:          3,
				UpdatedReplicas:   1,
				Conditions: []appsv1.DeploymentCondition{
					{
						Type:    appsv1.DeploymentAvailable,
						Status:  corev1.ConditionFalse,
						Message: "Deployment does not have minimum availability.",
					},
				},
			},
		}

		currentRS := &appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:        componentName + "-rev3",
				Namespace:   namespace,
				Labels:      map[string]string{"app": componentName, "pod-template-hash": "rev3"},
				Annotations: map[string]string{"deployment.kubernetes.io/revision": "3"},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       componentName,
						UID:        deployment.UID,
						Controller: ptr.To(true),
					},
				},
			},
			Spec: appsv1.ReplicaSetSpec{
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": componentName, "pod-template-hash": "rev3"},
				},
			},
		}

		crashingPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      componentName + "-xyz",
				Namespace: namespace,
				Labels:    map[string]string{"app": componentName, "pod-template-hash": "rev3"},
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "server",
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{
								Reason: "CrashLoopBackOff",
							},
						},
					},
				},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(api.Scheme).
			WithObjects(deployment, currentRS, crashingPod).
			Build()

		cpContext := ControlPlaneContext{
			Context: context.Background(),
			Client:  c,
			HCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  namespace,
					Generation: 5,
				},
			},
			ReleaseImageProvider: testutil.FakeImageProvider(),
		}

		component := NewDeploymentComponent(componentName, nil)
		workload := component.Build().(*controlPlaneWorkload[*appsv1.Deployment])

		// Simulate a previously healthy component transitioning to unhealthy.
		componentStatus := &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{
				Name:      componentName,
				Namespace: namespace,
			},
			Status: hyperv1.ControlPlaneComponentStatus{
				Conditions: []metav1.Condition{
					{Type: string(hyperv1.ControlPlaneComponentAvailable), Status: metav1.ConditionTrue, Reason: hyperv1.AsExpectedReason},
					{Type: string(hyperv1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue, Reason: hyperv1.AsExpectedReason},
				},
			},
		}

		err := workload.reconcileComponentStatus(cpContext, componentStatus, nil, nil, nil)
		g.Expect(err).NotTo(HaveOccurred())

		avail := meta.FindStatusCondition(componentStatus.Status.Conditions, string(hyperv1.ControlPlaneComponentAvailable))
		g.Expect(avail).NotTo(BeNil())
		g.Expect(avail.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(avail.Reason).To(Equal(hyperv1.CrashLoopBackOffReason))
		g.Expect(avail.Message).To(Equal("Deployment " + componentName + " is not available: Deployment does not have minimum availability.; Pod " + componentName + "-xyz container server: CrashLoopBackOff"))

		rollout := meta.FindStatusCondition(componentStatus.Status.Conditions, string(hyperv1.ControlPlaneComponentRolloutComplete))
		g.Expect(rollout).NotTo(BeNil())
		g.Expect(rollout.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(rollout.Reason).To(Equal(hyperv1.CrashLoopBackOffReason))
		g.Expect(rollout.Message).To(Equal("Waiting for deployment " + componentName + " rollout to finish: 1 out of 3 new replicas have been updated; Pod " + componentName + "-xyz container server: CrashLoopBackOff"))
	})
}

func createMockControlPlaneComponent(name string, available, rolloutComplete bool, version string) hyperv1.ControlPlaneComponent {
	status := metav1.ConditionTrue
	if !available {
		status = metav1.ConditionFalse
	}
	rolloutStatus := metav1.ConditionTrue
	if !rolloutComplete {
		rolloutStatus = metav1.ConditionFalse
	}
	return hyperv1.ControlPlaneComponent{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: hyperv1.ControlPlaneComponentStatus{
			Conditions: []metav1.Condition{
				{Type: string(hyperv1.ControlPlaneComponentAvailable), Status: status},
				{Type: string(hyperv1.ControlPlaneComponentRolloutComplete), Status: rolloutStatus},
			},
			Version: version,
		},
	}
}

func createMockWorkload(name string, dependencies []string) *controlPlaneWorkload[*appsv1.Deployment] {
	component := NewDeploymentComponent(name, nil)
	if len(dependencies) > 0 {
		component = component.WithDependencies(dependencies...)
	}
	return component.Build().(*controlPlaneWorkload[*appsv1.Deployment])
}

func TestCheckDependencies(t *testing.T) {
	g := NewGomegaWithT(t)

	testCases := []struct {
		testName        string
		mockComponents  []hyperv1.ControlPlaneComponent
		expectedMissing []string
		setup           func(cpContext *ControlPlaneContext) *controlPlaneWorkload[*appsv1.Deployment]
	}{
		{
			testName: "Should return no missing dependencies when all dependencies are available",
			mockComponents: []hyperv1.ControlPlaneComponent{
				createMockControlPlaneComponent(kubeAPIServerComponentName, true, true, testVersion),
			},
			expectedMissing: []string{},
		},
		{
			testName:        "Should return kube-apiserver as missing when it is not present",
			mockComponents:  []hyperv1.ControlPlaneComponent{},
			expectedMissing: []string{kubeAPIServerComponentName},
		},
		{
			testName: "Should return kube-apiserver as missing when its rollout is not complete",
			mockComponents: []hyperv1.ControlPlaneComponent{
				createMockControlPlaneComponent(kubeAPIServerComponentName, true, false, testVersion),
			},
			expectedMissing: []string{kubeAPIServerComponentName},
		},
		{
			testName: "Should return dependency1 as missing when its Available condition is false",
			mockComponents: []hyperv1.ControlPlaneComponent{
				createMockControlPlaneComponent("dependency1", false, true, testVersion),
				createMockControlPlaneComponent(kubeAPIServerComponentName, true, true, testVersion),
			},
			expectedMissing: []string{"dependency1"},
			setup: func(cpContext *ControlPlaneContext) *controlPlaneWorkload[*appsv1.Deployment] {
				return createMockWorkload("test-component", []string{"dependency1"})
			},
		},
		{
			testName: "Should return kube-apiserver as missing when its version does not match desired version",
			mockComponents: []hyperv1.ControlPlaneComponent{
				createMockControlPlaneComponent(kubeAPIServerComponentName, true, true, "4.17.0"),
			},
			expectedMissing: []string{kubeAPIServerComponentName},
		},
		{
			testName:        "Should not include kube-apiserver as dependency for etcd component",
			mockComponents:  []hyperv1.ControlPlaneComponent{},
			expectedMissing: []string{},
			setup: func(cpContext *ControlPlaneContext) *controlPlaneWorkload[*appsv1.Deployment] {
				return createMockWorkload(etcdComponentName, nil)
			},
		},
		{
			testName:        "When external OIDC webhook has no KAS component, it should have no unavailable dependencies",
			mockComponents:  []hyperv1.ControlPlaneComponent{},
			expectedMissing: []string{},
			setup: func(cpContext *ControlPlaneContext) *controlPlaneWorkload[*appsv1.Deployment] {
				return createMockWorkload("external-oidc-webhook", nil)
			},
		},
		{
			// capi-provider mounts the service-network-admin-kubeconfig secret the
			// kube-apiserver component applies, so unlike the other components that
			// predate the kube-apiserver (cluster-api, router, ...) it must wait for
			// its rollout on releases guaranteed to run a v2 CPO (4.20+).
			testName:        "When capi-provider uses release 4.20 or later, it should depend on kube-apiserver",
			mockComponents:  []hyperv1.ControlPlaneComponent{},
			expectedMissing: []string{kubeAPIServerComponentName},
			setup: func(cpContext *ControlPlaneContext) *controlPlaneWorkload[*appsv1.Deployment] {
				cpContext.ReleaseImageProvider = testutil.FakeImageProvider(testutil.WithVersion("4.20.0"))
				return createMockWorkload("capi-provider", nil)
			},
		},
		{
			// Releases before 4.20 may run a v1 CPO, which never publishes a
			// kube-apiserver ControlPlaneComponent; waiting for one would block
			// capi-provider forever (OCPBUGS-143987).
			testName:        "When capi-provider uses a release before 4.20, it should not depend on kube-apiserver",
			mockComponents:  []hyperv1.ControlPlaneComponent{},
			expectedMissing: []string{},
			setup: func(cpContext *ControlPlaneContext) *controlPlaneWorkload[*appsv1.Deployment] {
				cpContext.ReleaseImageProvider = testutil.FakeImageProvider(testutil.WithVersion("4.19.0"))
				return createMockWorkload("capi-provider", nil)
			},
		},
		{
			// 4.20 pre-release payloads (ec, rc, nightly) already ship a v2-only
			// CPO, so the pre-release must not drag them below the 4.20.0
			// threshold and strip the dependency.
			testName:        "When capi-provider uses a 4.20 pre-release, it should depend on kube-apiserver",
			mockComponents:  []hyperv1.ControlPlaneComponent{},
			expectedMissing: []string{kubeAPIServerComponentName},
			setup: func(cpContext *ControlPlaneContext) *controlPlaneWorkload[*appsv1.Deployment] {
				cpContext.ReleaseImageProvider = testutil.FakeImageProvider(testutil.WithVersion("4.20.0-0.nightly-2025-01-01-000000"))
				return createMockWorkload("capi-provider", nil)
			},
		},
		{
			// An unparsable version can't prove the CPO is v2-only. Skipping the
			// dependency risks a transient FailedMount; keeping it would block the
			// component forever, so the skip is the recoverable choice.
			testName:        "When capi-provider has an unparsable release version, it should not depend on kube-apiserver",
			mockComponents:  []hyperv1.ControlPlaneComponent{},
			expectedMissing: []string{},
			setup: func(cpContext *ControlPlaneContext) *controlPlaneWorkload[*appsv1.Deployment] {
				cpContext.ReleaseImageProvider = testutil.FakeImageProvider(testutil.WithVersion("not-a-version"))
				return createMockWorkload("capi-provider", nil)
			},
		},
		{
			testName: "Should remove etcd from dependencies when etcd management type is unmanaged",
			mockComponents: []hyperv1.ControlPlaneComponent{
				createMockControlPlaneComponent(kubeAPIServerComponentName, true, true, testVersion),
			},
			expectedMissing: []string{},
			setup: func(cpContext *ControlPlaneContext) *controlPlaneWorkload[*appsv1.Deployment] {
				cpContext.HCP.Spec.Etcd.ManagementType = hyperv1.Unmanaged
				return createMockWorkload("test-component", []string{"etcd"})
			},
		},
		{
			testName: "Should remove circular dependency when component depends on itself",
			mockComponents: []hyperv1.ControlPlaneComponent{
				createMockControlPlaneComponent(kubeAPIServerComponentName, true, true, testVersion),
			},
			expectedMissing: []string{},
			setup: func(cpContext *ControlPlaneContext) *controlPlaneWorkload[*appsv1.Deployment] {
				return createMockWorkload("test-component", []string{"test-component"})
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testName, func(t *testing.T) {
			// Create a fresh client and context for each test case
			client := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			cpContext := ControlPlaneContext{
				Client: client,
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{},
				},
				ReleaseImageProvider: testutil.FakeImageProvider(),
			}

			var workload *controlPlaneWorkload[*appsv1.Deployment]
			if tc.setup != nil {
				workload = tc.setup(&cpContext)
			} else {
				workload = createMockWorkload("test-component", nil)
			}

			for _, component := range tc.mockComponents {
				_ = client.Create(cpContext, &component)
			}

			unavailableDependencies, err := workload.checkDependencies(cpContext)
			g.Expect(err).To(BeNil())
			g.Expect(unavailableDependencies).To(ConsistOf(tc.expectedMissing))
		})
	}
}

func createMockOperandDeployment(name string, ready bool, version string, componentName string) *appsv1.Deployment {
	var replicas int32 = 3
	generation := int64(1)

	// For unready deployments, simulate different failure modes
	var readyReplicas, updatedReplicas, availableReplicas int32
	var unavailableReplicas int32
	var observedGeneration int64

	if ready {
		readyReplicas = replicas
		updatedReplicas = replicas
		availableReplicas = replicas
		unavailableReplicas = 0
		observedGeneration = generation
	} else {
		// Simulate an updating deployment with some pods not ready
		readyReplicas = 2
		updatedReplicas = 2
		availableReplicas = 2
		unavailableReplicas = 1
		observedGeneration = generation
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Generation: generation,
			Labels: map[string]string{
				"hypershift.openshift.io/managed-by": componentName,
			},
			Annotations: map[string]string{
				"release.openshift.io/version": version,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
		},
		Status: appsv1.DeploymentStatus{
			Replicas:            replicas,
			UpdatedReplicas:     updatedReplicas,
			ReadyReplicas:       readyReplicas,
			AvailableReplicas:   availableReplicas,
			UnavailableReplicas: unavailableReplicas,
			ObservedGeneration:  observedGeneration,
		},
	}
}

func TestCheckOperandsRolloutStatus(t *testing.T) {
	g := NewGomegaWithT(t)

	componentName := "test-component"

	testCases := []struct {
		name                    string
		deployment              *appsv1.Deployment
		expectedRolloutComplete bool
		expectedErrorMessage    string
	}{
		{
			name:                    "All replicas ready and updated",
			deployment:              createMockOperandDeployment("test-deployment", true, "4.18.0", componentName),
			expectedRolloutComplete: true,
			expectedErrorMessage:    "",
		},
		{
			name:                    "Replicas not all ready",
			deployment:              createMockOperandDeployment("test-deployment", false, "4.18.0", componentName),
			expectedRolloutComplete: false,
			expectedErrorMessage:    "deployment /test-deployment is not ready",
		},
		{
			name:                    "No deployments to monitor",
			deployment:              createMockOperandDeployment("test-deployment", true, "4.18.0", "other-component"),
			expectedRolloutComplete: true,
			expectedErrorMessage:    "",
		},
		{
			name:                    "Different version",
			deployment:              createMockOperandDeployment("test-deployment", true, "4.17.0", componentName),
			expectedRolloutComplete: false,
			expectedErrorMessage:    "deployment /test-deployment has version 4.17.0, expected 4.18.0",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a fresh client and context for each test case
			client := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(tc.deployment).
				WithStatusSubresource(&appsv1.Deployment{}).
				Build()

			cpContext := ControlPlaneContext{
				Client: client,
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{},
				},
				ReleaseImageProvider: testutil.FakeImageProvider(),
			}

			// Create a workload that monitors operands based on the deployment label
			component := NewDeploymentComponent(componentName, nil)
			component = component.MonitorOperandsRolloutStatus()
			workload := component.Build().(*controlPlaneWorkload[*appsv1.Deployment])

			isReady, err := workload.checkOperandsRolloutStatus(cpContext.workloadContext())
			g.Expect(isReady).To(Equal(tc.expectedRolloutComplete))

			if tc.expectedErrorMessage != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrorMessage))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}
