package hostedcontrolplane

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileInstallationStage(t *testing.T) {
	previousTime := metav1.NewTime(time.Now().Add(-time.Hour))
	const testNamespace = "test-hcp-ns"

	// Helper to build a CPC with Available=True.
	availableCPC := func(name string) *hyperv1.ControlPlaneComponent {
		cpc := &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNamespace,
			},
		}
		meta.SetStatusCondition(&cpc.Status.Conditions, metav1.Condition{
			Type:   string(hyperv1.ControlPlaneComponentAvailable),
			Status: metav1.ConditionTrue,
			Reason: "AsExpected",
		})
		return cpc
	}

	// Helper to build a CPC with Available=False.
	unavailableCPC := func(name string) *hyperv1.ControlPlaneComponent {
		cpc := &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNamespace,
			},
		}
		meta.SetStatusCondition(&cpc.Status.Conditions, metav1.Condition{
			Type:   string(hyperv1.ControlPlaneComponentAvailable),
			Status: metav1.ConditionFalse,
			Reason: "WaitingForAvailable",
		})
		return cpc
	}

	testCases := []struct {
		name          string
		hcp           *hyperv1.HostedControlPlane
		objects       []client.Object
		expectedStage hyperv1.InstallationStage
	}{
		{
			name: "When HCP is fresh it should set Initializing",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace},
				Status:     hyperv1.HostedControlPlaneStatus{},
			},
			expectedStage: hyperv1.InstallationStageInitializing,
		},
		{
			name: "When Available condition exists but is False it should set ControlPlaneProvisioning",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace},
				}
				meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
					Type:   string(hyperv1.HostedControlPlaneAvailable),
					Status: metav1.ConditionFalse,
					Reason: "NotReady",
				})
				return hcp
			}(),
			objects:       []client.Object{unavailableCPC("etcd"), unavailableCPC("kube-apiserver")},
			expectedStage: hyperv1.InstallationStageControlPlaneProvisioning,
		},
		{
			name: "When all CPCs are Available and no kubeconfig it should set WaitingForKubeconfig",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace},
					Status: hyperv1.HostedControlPlaneStatus{
						Installation: &hyperv1.InstallationStatus{
							Stage:              hyperv1.InstallationStageControlPlaneProvisioning,
							LastTransitionTime: previousTime,
						},
					},
				}
				meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
					Type:   string(hyperv1.HostedControlPlaneAvailable),
					Status: metav1.ConditionFalse,
					Reason: "KubeconfigWaitingForCreate",
				})
				return hcp
			}(),
			objects:       []client.Object{availableCPC("etcd"), availableCPC("kube-apiserver")},
			expectedStage: hyperv1.InstallationStageWaitingForKubeconfig,
		},
		{
			name: "When some CPCs are not Available it should stay at ControlPlaneProvisioning",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace},
					Status: hyperv1.HostedControlPlaneStatus{
						Installation: &hyperv1.InstallationStatus{
							Stage:              hyperv1.InstallationStageControlPlaneProvisioning,
							LastTransitionTime: previousTime,
						},
					},
				}
				meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
					Type:   string(hyperv1.HostedControlPlaneAvailable),
					Status: metav1.ConditionFalse,
					Reason: "ComponentsNotAvailable",
				})
				return hcp
			}(),
			objects:       []client.Object{availableCPC("etcd"), unavailableCPC("kube-apiserver")},
			expectedStage: hyperv1.InstallationStageControlPlaneProvisioning,
		},
		{
			name: "When kubeconfig exists but ClusterVersionAvailable is not True it should set WaitingForOperators",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace},
					Status: hyperv1.HostedControlPlaneStatus{
						Installation: &hyperv1.InstallationStatus{
							Stage:              hyperv1.InstallationStageWaitingForKubeconfig,
							LastTransitionTime: previousTime,
						},
						KubeConfig: &hyperv1.KubeconfigSecretRef{
							Name: "admin-kubeconfig",
							Key:  "kubeconfig",
						},
					},
				}
				return hcp
			}(),
			objects:       []client.Object{availableCPC("etcd"), availableCPC("kube-apiserver")},
			expectedStage: hyperv1.InstallationStageWaitingForOperators,
		},
		{
			name: "When ClusterVersionAvailable is True it should set InstallationComplete",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace},
					Status: hyperv1.HostedControlPlaneStatus{
						Installation: &hyperv1.InstallationStatus{
							Stage:              hyperv1.InstallationStageWaitingForOperators,
							LastTransitionTime: previousTime,
						},
						KubeConfig: &hyperv1.KubeconfigSecretRef{
							Name: "admin-kubeconfig",
							Key:  "kubeconfig",
						},
					},
				}
				meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
					Type:   string(hyperv1.ClusterVersionAvailable),
					Status: metav1.ConditionTrue,
					Reason: "AsExpected",
				})
				return hcp
			}(),
			expectedStage: hyperv1.InstallationStageInstallationComplete,
		},
		{
			name: "When stage is empty and Available is True it should backfill to InstallationComplete",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace},
				}
				meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
					Type:   string(hyperv1.HostedControlPlaneAvailable),
					Status: metav1.ConditionTrue,
					Reason: "AsExpected",
				})
				return hcp
			}(),
			expectedStage: hyperv1.InstallationStageInstallationComplete,
		},
		{
			name: "When stage is already set to a later stage it should not regress",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace},
					Status: hyperv1.HostedControlPlaneStatus{
						Installation: &hyperv1.InstallationStatus{
							Stage:              hyperv1.InstallationStageWaitingForOperators,
							LastTransitionTime: previousTime,
						},
					},
				}
				// Available is False — normally would indicate ControlPlaneProvisioning,
				// but monotonicity prevents regression.
				meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
					Type:   string(hyperv1.HostedControlPlaneAvailable),
					Status: metav1.ConditionFalse,
					Reason: "NotReady",
				})
				return hcp
			}(),
			objects:       []client.Object{unavailableCPC("etcd")},
			expectedStage: hyperv1.InstallationStageWaitingForOperators,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			builder := fake.NewClientBuilder().WithScheme(api.Scheme)
			for _, obj := range tc.objects {
				builder = builder.WithObjects(obj)
			}

			r := &HostedControlPlaneReconciler{
				Client: builder.Build(),
			}

			// Compute CPC availability once and pass to the plain function,
			// matching the production call site.
			componentsMsg, componentsErr, componentCount := r.controlPlaneComponentsAvailable(context.Background(), tc.hcp)
			reconcileInstallationStage(tc.hcp, componentsMsg, componentsErr, componentCount)
			g.Expect(tc.hcp.Status.Installation).NotTo(BeNil())
			g.Expect(tc.hcp.Status.Installation.Stage).To(Equal(tc.expectedStage))

			// Transition time should always be set.
			g.Expect(tc.hcp.Status.Installation.LastTransitionTime.IsZero()).To(BeFalse())
		})
	}
}
