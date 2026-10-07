package controlplanecomponent

import (
	"errors"
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

// alwaysEnforce is a gate that always enforces its preconditions.
func alwaysEnforce(WorkloadContext) (bool, error) { return true, nil }

// neverEnforce is a gate that always skips its preconditions.
func neverEnforce(WorkloadContext) (bool, error) { return false, nil }

// TestCheckPreconditions covers the condition-reading logic that decides which
// preconditions are unmet based on the HCP status conditions, and the gate logic
// that decides whether a precondition group is enforced.
func TestCheckPreconditions(t *testing.T) {
	testCases := []struct {
		name          string
		gate          PreconditionGate
		preconditions []hyperv1.ConditionType
		hcpConditions []metav1.Condition
		expectedUnmet []hyperv1.ConditionType
		expectErr     bool
	}{
		{
			name:          "When there are no preconditions, it should return none unmet",
			gate:          alwaysEnforce,
			preconditions: nil,
			hcpConditions: nil,
			expectedUnmet: nil,
		},
		{
			name: "When the condition is True, it should be satisfied",
			gate: alwaysEnforce,
			preconditions: []hyperv1.ConditionType{
				hyperv1.ConfigOperatorReconciliationSucceeded,
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
			name: "When the condition is False, it should be unmet",
			gate: alwaysEnforce,
			preconditions: []hyperv1.ConditionType{
				hyperv1.ConfigOperatorReconciliationSucceeded,
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
			name: "When the condition is absent, it should be unmet",
			gate: alwaysEnforce,
			preconditions: []hyperv1.ConditionType{
				hyperv1.ConfigOperatorReconciliationSucceeded,
			},
			hcpConditions: nil,
			expectedUnmet: []hyperv1.ConditionType{hyperv1.ConfigOperatorReconciliationSucceeded},
		},
		{
			name: "When the condition status is Unknown, it should be unmet",
			gate: alwaysEnforce,
			preconditions: []hyperv1.ConditionType{
				hyperv1.ConfigOperatorReconciliationSucceeded,
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
			name: "When some conditions are met and others are not, it should return only the unmet ones",
			gate: alwaysEnforce,
			preconditions: []hyperv1.ConditionType{
				hyperv1.ConfigOperatorReconciliationSucceeded,
				hyperv1.EtcdSnapshotRestored,
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
			name: "When all conditions are unmet, it should return them in order",
			gate: alwaysEnforce,
			preconditions: []hyperv1.ConditionType{
				hyperv1.ConfigOperatorReconciliationSucceeded,
				hyperv1.EtcdSnapshotRestored,
			},
			hcpConditions: nil,
			expectedUnmet: []hyperv1.ConditionType{
				hyperv1.ConfigOperatorReconciliationSucceeded,
				hyperv1.EtcdSnapshotRestored,
			},
		},
		{
			name: "When the gate returns false, it should treat the preconditions as met",
			gate: neverEnforce,
			preconditions: []hyperv1.ConditionType{
				hyperv1.ConfigOperatorReconciliationSucceeded,
			},
			hcpConditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ConfigOperatorReconciliationSucceeded),
					Status: metav1.ConditionFalse,
				},
			},
			expectedUnmet: nil,
		},
		{
			name: "When the gate returns true, it should enforce the preconditions",
			gate: alwaysEnforce,
			preconditions: []hyperv1.ConditionType{
				hyperv1.ConfigOperatorReconciliationSucceeded,
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
			name: "When the gate is nil, it should enforce the preconditions",
			gate: nil,
			preconditions: []hyperv1.ConditionType{
				hyperv1.ConfigOperatorReconciliationSucceeded,
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
			name: "When the gate returns an error, it should propagate the error",
			gate: func(WorkloadContext) (bool, error) { return false, errors.New("boom") },
			preconditions: []hyperv1.ConditionType{
				hyperv1.ConfigOperatorReconciliationSucceeded,
			},
			hcpConditions: nil,
			expectErr:     true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			component := NewDeploymentComponent("test-component", nil).
				WithGatedPreconditions(tc.gate, tc.preconditions...).
				Build()
			workload := component.(*controlPlaneWorkload[*appsv1.Deployment])

			cpContext := ControlPlaneContext{
				HCP: &hyperv1.HostedControlPlane{
					Status: hyperv1.HostedControlPlaneStatus{
						Conditions: tc.hcpConditions,
					},
				},
			}

			unmet, err := workload.checkPreconditions(cpContext)

			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			if len(tc.expectedUnmet) == 0 {
				g.Expect(unmet).To(BeEmpty())
			} else {
				g.Expect(unmet).To(Equal(tc.expectedUnmet))
			}
		})
	}
}

// TestReconcileSkipsWorkloadWhenPreconditionUnmet asserts the gating guarantee:
// when a precondition is unmet, the component does not create its workload.
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

	t.Run("When the precondition is unmet, it should not create the workload", func(t *testing.T) {
		g := NewGomegaWithT(t)

		cpContext := newContext(metav1.ConditionFalse)
		component := NewDeploymentComponent(componentName, &fakeComponentOptions{}).
			WithGatedPreconditions(alwaysEnforce, hyperv1.ConfigOperatorReconciliationSucceeded).
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

// TestReconcileFailsWhenGateErrors asserts that a gate error aborts the reconcile
// and no workload is created.
func TestReconcileFailsWhenGateErrors(t *testing.T) {
	componentName := "cluster-storage-operator"
	namespace := "test-namespace"

	t.Run("When the gate returns an error, it should fail the reconcile", func(t *testing.T) {
		g := NewGomegaWithT(t)

		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		cpContext := ControlPlaneContext{
			Context:              t.Context(),
			Client:               fakeClient,
			HCP:                  &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}},
			ReleaseImageProvider: testutil.FakeImageProvider(),
			SkipPredicate:        true,
		}

		component := NewDeploymentComponent(componentName, &fakeComponentOptions{}).
			WithGatedPreconditions(
				func(WorkloadContext) (bool, error) { return false, errors.New("gate failure") },
				hyperv1.ConfigOperatorReconciliationSucceeded,
			).
			Build()

		err := component.Reconcile(cpContext)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed checking preconditions"))

		deploy := &appsv1.Deployment{}
		err = fakeClient.Get(cpContext, client.ObjectKey{Namespace: namespace, Name: componentName}, deploy)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "workload Deployment should not exist when the gate errors")
	})
}

type fakeComponentOptions struct{}

func (f *fakeComponentOptions) IsRequestServing() bool         { return false }
func (f *fakeComponentOptions) MultiZoneSpread() bool          { return false }
func (f *fakeComponentOptions) NeedsManagementKASAccess() bool { return false }
