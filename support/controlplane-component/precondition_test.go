package controlplanecomponent

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/testutil"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestCheckPreconditions covers the condition-reading logic that decides which
// preconditions are unmet based on the HCP status conditions.
func TestCheckPreconditions(t *testing.T) {
	testCases := []struct {
		name          string
		preconditions []Precondition
		hcpConditions []metav1.Condition
		expectedUnmet []hyperv1.ConditionType
	}{
		{
			name:          "no preconditions returns none unmet",
			preconditions: nil,
			hcpConditions: nil,
			expectedUnmet: nil,
		},
		{
			name: "condition True is satisfied",
			preconditions: []Precondition{
				{ConditionType: hyperv1.ConfigOperatorReconciliationSucceeded},
			},
			hcpConditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ConfigOperatorReconciliationSucceeded),
					Status: metav1.ConditionTrue,
				},
			},
			expectedUnmet: nil,
		},
		{
			name: "condition False is unmet",
			preconditions: []Precondition{
				{ConditionType: hyperv1.ConfigOperatorReconciliationSucceeded},
			},
			hcpConditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ConfigOperatorReconciliationSucceeded),
					Status: metav1.ConditionFalse,
				},
			},
			expectedUnmet: []hyperv1.ConditionType{hyperv1.ConfigOperatorReconciliationSucceeded},
		},
		{
			name: "absent condition is unmet",
			preconditions: []Precondition{
				{ConditionType: hyperv1.ConfigOperatorReconciliationSucceeded},
			},
			hcpConditions: nil,
			expectedUnmet: []hyperv1.ConditionType{hyperv1.ConfigOperatorReconciliationSucceeded},
		},
		{
			name: "unknown condition status is unmet",
			preconditions: []Precondition{
				{ConditionType: hyperv1.ConfigOperatorReconciliationSucceeded},
			},
			hcpConditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ConfigOperatorReconciliationSucceeded),
					Status: metav1.ConditionUnknown,
				},
			},
			expectedUnmet: []hyperv1.ConditionType{hyperv1.ConfigOperatorReconciliationSucceeded},
		},
		{
			name: "mixed: only the unmet one is returned",
			preconditions: []Precondition{
				{ConditionType: hyperv1.ConfigOperatorReconciliationSucceeded},
				{ConditionType: hyperv1.EtcdSnapshotRestored},
			},
			hcpConditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ConfigOperatorReconciliationSucceeded),
					Status: metav1.ConditionTrue,
				},
				{
					Type:   string(hyperv1.EtcdSnapshotRestored),
					Status: metav1.ConditionFalse,
				},
			},
			expectedUnmet: []hyperv1.ConditionType{hyperv1.EtcdSnapshotRestored},
		},
		{
			name: "all unmet are returned in order",
			preconditions: []Precondition{
				{ConditionType: hyperv1.ConfigOperatorReconciliationSucceeded},
				{ConditionType: hyperv1.EtcdSnapshotRestored},
			},
			hcpConditions: nil,
			expectedUnmet: []hyperv1.ConditionType{
				hyperv1.ConfigOperatorReconciliationSucceeded,
				hyperv1.EtcdSnapshotRestored,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			component := NewDeploymentComponent("test-component", nil).
				WithPreconditions(tc.preconditions...).
				Build()
			workload := component.(*controlPlaneWorkload[*appsv1.Deployment])

			cpContext := ControlPlaneContext{
				HCP: &hyperv1.HostedControlPlane{
					Status: hyperv1.HostedControlPlaneStatus{
						Conditions: tc.hcpConditions,
					},
				},
			}

			unmet := workload.checkPreconditions(cpContext)

			gotTypes := make([]hyperv1.ConditionType, 0, len(unmet))
			for _, p := range unmet {
				gotTypes = append(gotTypes, p.ConditionType)
			}
			if len(tc.expectedUnmet) == 0 {
				g.Expect(gotTypes).To(BeEmpty())
			} else {
				g.Expect(gotTypes).To(Equal(tc.expectedUnmet))
			}
		})
	}
}

// TestReconcileSkipsWorkloadWhenPreconditionUnmet asserts the gating guarantee:
// when a precondition is unmet, the component does not create its workload, and
// once the precondition is satisfied the workload is created.
func TestReconcileSkipsWorkloadWhenPreconditionUnmet(t *testing.T) {
	componentName := "cluster-storage-operator"
	namespace := "test-namespace"

	newContext := func(configOpSucceeded metav1.ConditionStatus) ControlPlaneContext {
		client := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		return ControlPlaneContext{
			Context: t.Context(),
			Client:  client,
			HCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.ConfigOperatorReconciliationSucceeded),
							Status: configOpSucceeded,
						},
					},
				},
			},
			ReleaseImageProvider: testutil.FakeImageProvider(),
			SkipPredicate:        true,
		}
	}

	t.Run("workload not created while precondition is unmet", func(t *testing.T) {
		g := NewGomegaWithT(t)

		cpContext := newContext(metav1.ConditionFalse)
		component := NewDeploymentComponent(componentName, &fakeComponentOptions{}).
			WithPreconditions(Precondition{
				ConditionType: hyperv1.ConfigOperatorReconciliationSucceeded,
				Context:       "initialKMSKeyARN is configured",
			}).
			Build()

		err := component.Reconcile(cpContext)
		g.Expect(err).NotTo(HaveOccurred())

		// The ControlPlaneComponent CR is created and reports the precondition wait.
		cpc := &hyperv1.ControlPlaneComponent{}
		g.Expect(cpContext.Client.Get(cpContext, client.ObjectKey{Namespace: namespace, Name: componentName}, cpc)).To(Succeed())
		rollout := meta.FindStatusCondition(cpc.Status.Conditions, string(hyperv1.ControlPlaneComponentRolloutComplete))
		g.Expect(rollout).NotTo(BeNil())
		g.Expect(rollout.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(rollout.Reason).To(Equal(hyperv1.WaitingForPreconditionsReason))

		// The workload Deployment must NOT have been created.
		deploy := &appsv1.Deployment{}
		err = cpContext.Client.Get(cpContext, client.ObjectKey{Namespace: namespace, Name: componentName}, deploy)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "workload Deployment should not exist while precondition is unmet")
	})
}

type fakeComponentOptions struct{}

func (f *fakeComponentOptions) IsRequestServing() bool         { return false }
func (f *fakeComponentOptions) MultiZoneSpread() bool          { return false }
func (f *fakeComponentOptions) NeedsManagementKASAccess() bool { return false }
