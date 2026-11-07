package hostedcluster

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileInstallationStage(t *testing.T) {
	const cpNamespace = "clusters-test"

	tests := []struct {
		name          string
		hcluster      *hyperv1.HostedCluster
		hcp           *hyperv1.HostedControlPlane
		cpcs          []client.Object
		nodePools     []client.Object
		expectedStage hyperv1.InstallationStage
	}{
		{
			name: "HCP nil should set Initializing",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
			},
			hcp:           nil,
			expectedStage: hyperv1.InstallationStageInitializing,
		},
		{
			name: "Fresh cluster with no conditions should set Initializing",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: cpNamespace},
			},
			expectedStage: hyperv1.InstallationStageInitializing,
		},
		{
			name: "InfrastructureReady=True should set InfrastructureReady",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: cpNamespace},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.InfrastructureReady),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
					},
				},
			},
			expectedStage: hyperv1.InstallationStageInfrastructureReady,
		},
		{
			name: "CPCs exist should set ControlPlaneProvisioning",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: cpNamespace},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.InfrastructureReady),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
					},
				},
			},
			cpcs: []client.Object{
				&hyperv1.ControlPlaneComponent{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: cpNamespace},
				},
			},
			expectedStage: hyperv1.InstallationStageControlPlaneProvisioning,
		},
		{
			name: "KubeAPIServerAvailable=True should set APIServerAvailable",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: cpNamespace},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.InfrastructureReady),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
						{
							Type:   string(hyperv1.KubeAPIServerAvailable),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
					},
				},
			},
			cpcs: []client.Object{
				&hyperv1.ControlPlaneComponent{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: cpNamespace},
				},
			},
			expectedStage: hyperv1.InstallationStageAPIServerAvailable,
		},
		{
			name: "NodePool with healthy machine should set WorkerAvailable",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: cpNamespace},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.InfrastructureReady),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
						{
							Type:   string(hyperv1.KubeAPIServerAvailable),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
					},
				},
			},
			cpcs: []client.Object{
				&hyperv1.ControlPlaneComponent{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: cpNamespace},
				},
			},
			nodePools: []client.Object{
				&hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-workers",
						Namespace: "clusters",
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "test",
					},
					Status: hyperv1.NodePoolStatus{
						Conditions: []hyperv1.NodePoolCondition{
							{
								Type:   hyperv1.NodePoolAllMachinesReadyConditionType,
								Status: corev1.ConditionTrue,
							},
						},
					},
				},
			},
			expectedStage: hyperv1.InstallationStageWorkerAvailable,
		},
		{
			name: "NodePool with ReadyNodeCount > 0 should set WorkerAvailable",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: cpNamespace},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.InfrastructureReady),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
						{
							Type:   string(hyperv1.KubeAPIServerAvailable),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
					},
				},
			},
			cpcs: []client.Object{
				&hyperv1.ControlPlaneComponent{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: cpNamespace},
				},
			},
			nodePools: []client.Object{
				&hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-workers",
						Namespace: "clusters",
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "test",
					},
					Status: hyperv1.NodePoolStatus{
						NodesInfo: hyperv1.NodePoolNodesInfo{
							NodeVersions: []hyperv1.NodeVersion{
								{
									OCPVersion:     "4.16.0",
									KubeletVersion: "v1.29.0",
									ReadyNodeCount: ptr.To[int32](1),
								},
							},
						},
					},
				},
			},
			expectedStage: hyperv1.InstallationStageWorkerAvailable,
		},
		{
			name: "KubeConfig available should set WaitingForOperators",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: cpNamespace},
				Status: hyperv1.HostedControlPlaneStatus{
					KubeConfig: &hyperv1.KubeconfigSecretRef{
						Name: "admin-kubeconfig",
						Key:  "kubeconfig",
					},
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.InfrastructureReady),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
						{
							Type:   string(hyperv1.KubeAPIServerAvailable),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
					},
				},
			},
			cpcs: []client.Object{
				&hyperv1.ControlPlaneComponent{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: cpNamespace},
				},
			},
			nodePools: []client.Object{
				&hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-workers",
						Namespace: "clusters",
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "test",
					},
					Status: hyperv1.NodePoolStatus{
						Conditions: []hyperv1.NodePoolCondition{
							{
								Type:   hyperv1.NodePoolAllMachinesReadyConditionType,
								Status: corev1.ConditionTrue,
							},
						},
					},
				},
			},
			expectedStage: hyperv1.InstallationStageWaitingForOperators,
		},
		{
			name: "ClusterVersionAvailable=True should set InstallationComplete",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: cpNamespace},
				Status: hyperv1.HostedControlPlaneStatus{
					KubeConfig: &hyperv1.KubeconfigSecretRef{
						Name: "admin-kubeconfig",
						Key:  "kubeconfig",
					},
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.ClusterVersionAvailable),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
					},
				},
			},
			expectedStage: hyperv1.InstallationStageInstallationComplete,
		},
		{
			name: "Backfill: stage empty and HCP Available=True should set InstallationComplete",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: cpNamespace},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.HostedControlPlaneAvailable),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
					},
				},
			},
			expectedStage: hyperv1.InstallationStageInstallationComplete,
		},
		{
			name: "Forward-only: cannot regress from WaitingForOperators",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
				Status: hyperv1.HostedClusterStatus{
					Installation: hyperv1.InstallationStatus{
						Stage: hyperv1.InstallationStageWaitingForOperators,
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: cpNamespace},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.InfrastructureReady),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
					},
				},
			},
			expectedStage: hyperv1.InstallationStageWaitingForOperators,
		},
		{
			name: "Multiple triggers should pick highest stage",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: cpNamespace},
				Status: hyperv1.HostedControlPlaneStatus{
					KubeConfig: &hyperv1.KubeconfigSecretRef{
						Name: "admin-kubeconfig",
						Key:  "kubeconfig",
					},
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.InfrastructureReady),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
						{
							Type:   string(hyperv1.KubeAPIServerAvailable),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
					},
				},
			},
			cpcs: []client.Object{
				&hyperv1.ControlPlaneComponent{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: cpNamespace},
				},
			},
			expectedStage: hyperv1.InstallationStageWaitingForOperators,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			objs := []client.Object{}
			objs = append(objs, tc.cpcs...)
			objs = append(objs, tc.nodePools...)

			c := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objs...).
				Build()

			reconcileInstallationStage(context.Background(), c, tc.hcluster, tc.hcp, cpNamespace)

			if tc.hcluster.Status.Installation.Stage != tc.expectedStage {
				t.Errorf("expected stage %q, got %q", tc.expectedStage, tc.hcluster.Status.Installation.Stage)
			}
		})
	}
}

func TestHasHealthyWorker(t *testing.T) {
	tests := []struct {
		name      string
		nodePools []client.Object
		expected  bool
	}{
		{
			name:     "When no node pools exist, it should return false",
			expected: false,
		},
		{
			name: "When NodePool has no ready machines, it should return false",
			nodePools: []client.Object{
				&hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-workers",
						Namespace: "clusters",
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "test",
					},
				},
			},
			expected: false,
		},
		{
			name: "When NodePool has AllMachinesReady=True, it should return true",
			nodePools: []client.Object{
				&hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-workers",
						Namespace: "clusters",
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "test",
					},
					Status: hyperv1.NodePoolStatus{
						Conditions: []hyperv1.NodePoolCondition{
							{
								Type:   hyperv1.NodePoolAllMachinesReadyConditionType,
								Status: corev1.ConditionTrue,
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "When NodePool has ReadyNodeCount=0, it should return false",
			nodePools: []client.Object{
				&hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-workers",
						Namespace: "clusters",
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "test",
					},
					Status: hyperv1.NodePoolStatus{
						NodesInfo: hyperv1.NodePoolNodesInfo{
							NodeVersions: []hyperv1.NodeVersion{
								{
									OCPVersion:     "4.16.0",
									KubeletVersion: "v1.29.0",
									ReadyNodeCount: ptr.To[int32](0),
								},
							},
						},
					},
				},
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(tc.nodePools...).
				Build()

			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
			}

			got := hasHealthyWorker(context.Background(), c, hcluster)
			if got != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}
