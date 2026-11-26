package hostedcluster

import (
	"context"
	"errors"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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
			name: "Backfill: stage empty and HCP guest VersionStatus completed history should set InstallationComplete",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: cpNamespace},
				Status: hyperv1.HostedControlPlaneStatus{
					VersionStatus: &hyperv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:   configv1.CompletedUpdate,
								Version: "4.20.0",
							},
						},
					},
				},
			},
			expectedStage: hyperv1.InstallationStageInstallationComplete,
		},
		{
			name: "Backfill: legacy HCP without VersionStatus and synthesized HC Version CompletedUpdate should not jump to InstallationComplete",
			hcluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
				Status: hyperv1.HostedClusterStatus{
					Version: &hyperv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{
							{
								State:   configv1.CompletedUpdate,
								Version: "4.20.0",
							},
						},
					},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: cpNamespace},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.KubeAPIServerAvailable),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
					},
				},
			},
			expectedStage: hyperv1.InstallationStageAPIServerAvailable,
		},
		{
			name: "Backfill: stage empty and HCP Available=True alone without completed version history should not jump to InstallationComplete",
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
						{
							Type:   string(hyperv1.KubeAPIServerAvailable),
							Status: metav1.ConditionTrue,
							Reason: "AsExpected",
						},
					},
				},
			},
			expectedStage: hyperv1.InstallationStageAPIServerAvailable,
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
			name: "KubeConfig available without KAS should not skip to WaitingForOperators",
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
			name: "KubeConfig and KAS available without NodePools should set WaitingForOperators (zero-worker flow)",
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
		{
			name: "KubeConfig and KAS available with replicas:0 NodePool should set WaitingForOperators (zero-worker flow)",
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
						Replicas:    ptr.To[int32](0),
					},
				},
			},
			expectedStage: hyperv1.InstallationStageWaitingForOperators,
		},
		{
			name: "KubeConfig and KAS available with autoscaling min:0 should set WaitingForOperators (scale-from-zero flow)",
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
						AutoScaling: &hyperv1.NodePoolAutoScaling{
							Min: ptr.To[int32](0),
							Max: 5,
						},
					},
				},
			},
			expectedStage: hyperv1.InstallationStageWaitingForOperators,
		},
		{
			name: "KubeConfig and KAS available with nonzero-replica NodePool and zero ready workers should stay at APIServerAvailable",
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
						Replicas:    ptr.To[int32](3),
					},
				},
			},
			expectedStage: hyperv1.InstallationStageAPIServerAvailable,
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

	readyHCP := &hyperv1.HostedControlPlane{
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
	}

	t.Run("When listing NodePools fails, it should stay at APIServerAvailable and not advance to WaitingForOperators", func(t *testing.T) {
		hcluster := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
		}
		cpc := &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: cpNamespace},
		}

		c := fake.NewClientBuilder().
			WithScheme(api.Scheme).
			WithObjects(cpc).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if _, ok := list.(*hyperv1.NodePoolList); ok {
						return errors.New("transient apiserver error")
					}
					return cl.List(ctx, list, opts...)
				},
			}).
			Build()

		reconcileInstallationStage(context.Background(), c, hcluster, readyHCP, cpNamespace)

		if hcluster.Status.Installation.Stage != hyperv1.InstallationStageAPIServerAvailable {
			t.Fatalf("expected stage %q on NodePool list error, got %q",
				hyperv1.InstallationStageAPIServerAvailable, hcluster.Status.Installation.Stage)
		}
	})

	t.Run("When workers are expected and first worker becomes healthy while KAS and KubeConfig are ready, it should report WorkerAvailable before WaitingForOperators", func(t *testing.T) {
		hcluster := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
			Status: hyperv1.HostedClusterStatus{
				Installation: hyperv1.InstallationStatus{
					Stage: hyperv1.InstallationStageAPIServerAvailable,
				},
			},
		}
		cpc := &hyperv1.ControlPlaneComponent{
			ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: cpNamespace},
		}
		np := &hyperv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-workers",
				Namespace: "clusters",
			},
			Spec: hyperv1.NodePoolSpec{
				ClusterName: "test",
				Replicas:    ptr.To[int32](3),
			},
			Status: hyperv1.NodePoolStatus{
				Conditions: []hyperv1.NodePoolCondition{
					{
						Type:   hyperv1.NodePoolAllMachinesReadyConditionType,
						Status: corev1.ConditionTrue,
					},
				},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(api.Scheme).
			WithObjects(cpc, np).
			Build()

		// First reconcile: must land on WorkerAvailable so HC status and metrics observe it.
		reconcileInstallationStage(context.Background(), c, hcluster, readyHCP, cpNamespace)
		if hcluster.Status.Installation.Stage != hyperv1.InstallationStageWorkerAvailable {
			t.Fatalf("first reconcile: expected stage %q, got %q",
				hyperv1.InstallationStageWorkerAvailable, hcluster.Status.Installation.Stage)
		}

		// Second reconcile: WorkerAvailable was already observed, so advance to WaitingForOperators.
		reconcileInstallationStage(context.Background(), c, hcluster, readyHCP, cpNamespace)
		if hcluster.Status.Installation.Stage != hyperv1.InstallationStageWaitingForOperators {
			t.Fatalf("second reconcile: expected stage %q, got %q",
				hyperv1.InstallationStageWaitingForOperators, hcluster.Status.Installation.Stage)
		}
	})
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
			name: "When AllMachinesReady=True has stale ObservedGeneration, it should return false",
			nodePools: []client.Object{
				&hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "test-workers",
						Namespace:  "clusters",
						Generation: 3,
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "test",
					},
					Status: hyperv1.NodePoolStatus{
						Conditions: []hyperv1.NodePoolCondition{
							{
								Type:               hyperv1.NodePoolAllMachinesReadyConditionType,
								Status:             corev1.ConditionTrue,
								ObservedGeneration: 2, // stale
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When AllMachinesReady=True has zero ObservedGeneration (legacy), it should return true",
			nodePools: []client.Object{
				&hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "test-workers",
						Namespace:  "clusters",
						Generation: 3,
					},
					Spec: hyperv1.NodePoolSpec{
						ClusterName: "test",
					},
					Status: hyperv1.NodePoolStatus{
						Conditions: []hyperv1.NodePoolCondition{
							{
								Type:               hyperv1.NodePoolAllMachinesReadyConditionType,
								Status:             corev1.ConditionTrue,
								ObservedGeneration: 0, // legacy controller
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
