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

func createMockComponent(name string, available, rolloutComplete bool, version string) hyperv1.ControlPlaneComponent {
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

func createTestComponent(name string, dependencies []string) *controlPlaneWorkload[*appsv1.Deployment] {
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
				createMockComponent(kubeAPIServerComponentName, true, true, testVersion),
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
				createMockComponent(kubeAPIServerComponentName, true, false, testVersion),
			},
			expectedMissing: []string{kubeAPIServerComponentName},
		},
		{
			testName: "Should return dependency1 as missing when its Available condition is false",
			mockComponents: []hyperv1.ControlPlaneComponent{
				createMockComponent("dependency1", false, true, testVersion),
				createMockComponent(kubeAPIServerComponentName, true, true, testVersion),
			},
			expectedMissing: []string{"dependency1"},
			setup: func(cpContext *ControlPlaneContext) *controlPlaneWorkload[*appsv1.Deployment] {
				return createTestComponent("test-component", []string{"dependency1"})
			},
		},
		{
			testName: "Should return kube-apiserver as missing when its version does not match desired version",
			mockComponents: []hyperv1.ControlPlaneComponent{
				createMockComponent(kubeAPIServerComponentName, true, true, "4.17.0"),
			},
			expectedMissing: []string{kubeAPIServerComponentName},
		},
		{
			testName:        "Should not include kube-apiserver as dependency for etcd component",
			mockComponents:  []hyperv1.ControlPlaneComponent{},
			expectedMissing: []string{},
			setup: func(cpContext *ControlPlaneContext) *controlPlaneWorkload[*appsv1.Deployment] {
				return createTestComponent(etcdComponentName, nil)
			},
		},
		{
			testName: "Should remove etcd from dependencies when etcd management type is unmanaged",
			mockComponents: []hyperv1.ControlPlaneComponent{
				createMockComponent(kubeAPIServerComponentName, true, true, testVersion),
			},
			expectedMissing: []string{},
			setup: func(cpContext *ControlPlaneContext) *controlPlaneWorkload[*appsv1.Deployment] {
				cpContext.HCP.Spec.Etcd.ManagementType = hyperv1.Unmanaged
				return createTestComponent("test-component", []string{"etcd"})
			},
		},
		{
			testName: "Should remove circular dependency when component depends on itself",
			mockComponents: []hyperv1.ControlPlaneComponent{
				createMockComponent(kubeAPIServerComponentName, true, true, testVersion),
			},
			expectedMissing: []string{},
			setup: func(cpContext *ControlPlaneContext) *controlPlaneWorkload[*appsv1.Deployment] {
				return createTestComponent("test-component", []string{"test-component"})
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
				workload = createTestComponent("test-component", nil)
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
