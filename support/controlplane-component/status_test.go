package controlplanecomponent

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/testutil"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	testVersion = "4.18.0"
)

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

func createMockWorload(name string, dependencies []string) *controlPlaneWorkload[*appsv1.Deployment] {
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
				return createMockWorload("test-component", []string{"dependency1"})
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
				return createMockWorload(etcdComponentName, nil)
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
				return createMockWorload("test-component", []string{"etcd"})
			},
		},
		{
			testName: "Should remove circular dependency when component depends on itself",
			mockComponents: []hyperv1.ControlPlaneComponent{
				createMockControlPlaneComponent(kubeAPIServerComponentName, true, true, testVersion),
			},
			expectedMissing: []string{},
			setup: func(cpContext *ControlPlaneContext) *controlPlaneWorkload[*appsv1.Deployment] {
				return createMockWorload("test-component", []string{"test-component"})
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
				workload = createMockWorload("test-component", nil)
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
