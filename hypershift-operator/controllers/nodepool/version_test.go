package nodepool

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNodeVersionsFromMachines(t *testing.T) {
	testCases := []struct {
		name     string
		machines []*v1beta1.Machine
		nodePool *hyperv1.NodePool
		expected []hyperv1.NodeVersion
	}{
		{
			name:     "When there are no machines it should return nil",
			machines: nil,
			nodePool: &hyperv1.NodePool{
				Status: hyperv1.NodePoolStatus{Version: "4.18.12"},
			},
			expected: nil,
		},
		{
			name: "When all machines have the same version and are healthy it should return a single entry",
			machines: []*v1beta1.Machine{
				machineWithVersionAndHealth("m1", "v1.31.4", true, map[string]string{hyperv1.NodePoolReleaseVersionAnnotation: "4.18.12"}),
				machineWithVersionAndHealth("m2", "v1.31.4", true, map[string]string{hyperv1.NodePoolReleaseVersionAnnotation: "4.18.12"}),
				machineWithVersionAndHealth("m3", "v1.31.4", true, map[string]string{hyperv1.NodePoolReleaseVersionAnnotation: "4.18.12"}),
			},
			nodePool: &hyperv1.NodePool{
				Status: hyperv1.NodePoolStatus{Version: "4.18.12"},
			},
			expected: []hyperv1.NodeVersion{
				{OCPVersion: "4.18.12", KubeletVersion: "v1.31.4", ReadyNodeCount: ptr.To[int32](3), UnreadyNodeCount: ptr.To[int32](0)},
			},
		},
		{
			name: "When there are mixed versions during rolling upgrade it should return one entry per version",
			machines: []*v1beta1.Machine{
				machineWithVersionAndHealth("m1", "v1.31.4", true, map[string]string{hyperv1.NodePoolReleaseVersionAnnotation: "4.18.12"}),
				machineWithVersionAndHealth("m2", "v1.31.4", true, map[string]string{hyperv1.NodePoolReleaseVersionAnnotation: "4.18.12"}),
				machineWithVersionAndHealth("m3", "v1.32.1", true, map[string]string{hyperv1.NodePoolReleaseVersionAnnotation: "4.19.1"}),
			},
			nodePool: &hyperv1.NodePool{
				Status: hyperv1.NodePoolStatus{Version: "4.18.12"},
			},
			expected: []hyperv1.NodeVersion{
				{OCPVersion: "4.18.12", KubeletVersion: "v1.31.4", ReadyNodeCount: ptr.To[int32](2), UnreadyNodeCount: ptr.To[int32](0)},
				{OCPVersion: "4.19.1", KubeletVersion: "v1.32.1", ReadyNodeCount: ptr.To[int32](1), UnreadyNodeCount: ptr.To[int32](0)},
			},
		},
		{
			name: "When there is mixed health it should report ready and unready counts per version",
			machines: []*v1beta1.Machine{
				machineWithVersionAndHealth("m1", "v1.31.4", true, map[string]string{hyperv1.NodePoolReleaseVersionAnnotation: "4.18.12"}),
				machineWithVersionAndHealth("m2", "v1.32.1", true, map[string]string{hyperv1.NodePoolReleaseVersionAnnotation: "4.19.1"}),
				machineWithVersionAndHealth("m3", "v1.32.1", false, map[string]string{hyperv1.NodePoolReleaseVersionAnnotation: "4.19.1"}),
			},
			nodePool: &hyperv1.NodePool{
				Status: hyperv1.NodePoolStatus{Version: "4.18.12"},
			},
			expected: []hyperv1.NodeVersion{
				{OCPVersion: "4.18.12", KubeletVersion: "v1.31.4", ReadyNodeCount: ptr.To[int32](1), UnreadyNodeCount: ptr.To[int32](0)},
				{OCPVersion: "4.19.1", KubeletVersion: "v1.32.1", ReadyNodeCount: ptr.To[int32](1), UnreadyNodeCount: ptr.To[int32](1)},
			},
		},
		{
			name: "When NodeHealthy condition is absent it should count the node as unready",
			machines: []*v1beta1.Machine{
				machineWithVersionAndConditions("m1", "v1.31.4", nil, map[string]string{hyperv1.NodePoolReleaseVersionAnnotation: "4.18.12"}),
			},
			nodePool: &hyperv1.NodePool{
				Status: hyperv1.NodePoolStatus{Version: "4.18.12"},
			},
			expected: []hyperv1.NodeVersion{
				{OCPVersion: "4.18.12", KubeletVersion: "v1.31.4", ReadyNodeCount: ptr.To[int32](0), UnreadyNodeCount: ptr.To[int32](1)},
			},
		},
		{
			name: "When some machines have no NodeInfo it should skip them",
			machines: []*v1beta1.Machine{
				machineWithVersionAndHealth("m1", "v1.31.4", true, map[string]string{hyperv1.NodePoolReleaseVersionAnnotation: "4.18.12"}),
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "m2",
						Annotations: map[string]string{hyperv1.NodePoolReleaseVersionAnnotation: "4.19.1"},
					},
					Status: v1beta1.MachineStatus{
						// NodeInfo is nil — not yet provisioned
					},
				},
			},
			nodePool: &hyperv1.NodePool{
				Status: hyperv1.NodePoolStatus{Version: "4.18.12"},
			},
			expected: []hyperv1.NodeVersion{
				{OCPVersion: "4.18.12", KubeletVersion: "v1.31.4", ReadyNodeCount: ptr.To[int32](1), UnreadyNodeCount: ptr.To[int32](0)},
			},
		},
		{
			name: "When all machines have no NodeInfo it should return nil",
			machines: []*v1beta1.Machine{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "m1"},
					Status:     v1beta1.MachineStatus{},
				},
			},
			nodePool: &hyperv1.NodePool{
				Status: hyperv1.NodePoolStatus{Version: "4.18.12"},
			},
			expected: nil,
		},
		{
			name: "When machine has release-version annotation it should use it for ocpVersion",
			machines: []*v1beta1.Machine{
				machineWithVersionAndHealth("m1", "v1.31.4", true, map[string]string{hyperv1.NodePoolReleaseVersionAnnotation: "4.18.12"}),
			},
			nodePool: &hyperv1.NodePool{
				Status: hyperv1.NodePoolStatus{Version: "4.17.0"},
			},
			expected: []hyperv1.NodeVersion{
				{OCPVersion: "4.18.12", KubeletVersion: "v1.31.4", ReadyNodeCount: ptr.To[int32](1), UnreadyNodeCount: ptr.To[int32](0)},
			},
		},
		{
			name: "When machine has no annotation it should fall back to nodePool.Status.Version",
			machines: []*v1beta1.Machine{
				machineWithVersionAndHealth("m1", "v1.31.4", true, nil),
			},
			nodePool: &hyperv1.NodePool{
				Status: hyperv1.NodePoolStatus{Version: "4.17.0"},
			},
			expected: []hyperv1.NodeVersion{
				{OCPVersion: "4.17.0", KubeletVersion: "v1.31.4", ReadyNodeCount: ptr.To[int32](1), UnreadyNodeCount: ptr.To[int32](0)},
			},
		},
		{
			name: "When there are multiple versions it should sort by ocpVersion then kubeletVersion",
			machines: []*v1beta1.Machine{
				machineWithVersionAndHealth("m1", "v1.32.1", true, map[string]string{hyperv1.NodePoolReleaseVersionAnnotation: "4.19.1"}),
				machineWithVersionAndHealth("m2", "v1.31.4", true, map[string]string{hyperv1.NodePoolReleaseVersionAnnotation: "4.18.12"}),
				machineWithVersionAndHealth("m3", "v1.31.5", true, map[string]string{hyperv1.NodePoolReleaseVersionAnnotation: "4.18.12"}),
			},
			nodePool: &hyperv1.NodePool{
				Status: hyperv1.NodePoolStatus{Version: "4.18.12"},
			},
			expected: []hyperv1.NodeVersion{
				{OCPVersion: "4.18.12", KubeletVersion: "v1.31.4", ReadyNodeCount: ptr.To[int32](1), UnreadyNodeCount: ptr.To[int32](0)},
				{OCPVersion: "4.18.12", KubeletVersion: "v1.31.5", ReadyNodeCount: ptr.To[int32](1), UnreadyNodeCount: ptr.To[int32](0)},
				{OCPVersion: "4.19.1", KubeletVersion: "v1.32.1", ReadyNodeCount: ptr.To[int32](1), UnreadyNodeCount: ptr.To[int32](0)},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			r := &NodePoolReconciler{
				Client: fakeClient,
			}

			ctx := context.Background()
			result := r.nodeVersionsFromMachines(ctx, tc.machines, tc.nodePool)
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestSetNodesInfoStatus(t *testing.T) {
	testCases := []struct {
		name              string
		machines          []client.Object
		nodePool          *hyperv1.NodePool
		expectedNodesInfo hyperv1.NodePoolNodesInfo
	}{
		{
			name: "When nodePool is scaled to zero it should clear previously set NodesInfo",
			// No machines exist.
			machines: nil,
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "clusters",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
				},
				Status: hyperv1.NodePoolStatus{
					Version: "4.18.12",
					NodesInfo: hyperv1.NodePoolNodesInfo{
						NodeVersions: []hyperv1.NodeVersion{
							{OCPVersion: "4.18.12", KubeletVersion: "v1.31.4", ReadyNodeCount: ptr.To[int32](3), UnreadyNodeCount: ptr.To[int32](0)},
						},
					},
				},
			},
			expectedNodesInfo: hyperv1.NodePoolNodesInfo{},
		},
		{
			name: "When machines exist with NodeInfo it should populate NodesInfo",
			machines: []client.Object{
				&v1beta1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "m1",
						Namespace: "clusters-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation:                       "clusters/test-nodepool",
							hyperv1.NodePoolReleaseVersionAnnotation: "4.18.12",
						},
					},
					Status: v1beta1.MachineStatus{
						NodeInfo: &corev1.NodeSystemInfo{KubeletVersion: "v1.31.4"},
						Conditions: v1beta1.Conditions{
							{Type: v1beta1.MachineNodeHealthyCondition, Status: corev1.ConditionTrue},
						},
					},
				},
			},
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "clusters",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
				},
				Status: hyperv1.NodePoolStatus{
					Version: "4.18.12",
				},
			},
			expectedNodesInfo: hyperv1.NodePoolNodesInfo{
				NodeVersions: []hyperv1.NodeVersion{
					{OCPVersion: "4.18.12", KubeletVersion: "v1.31.4", ReadyNodeCount: ptr.To[int32](1), UnreadyNodeCount: ptr.To[int32](0)},
				},
			},
		},
		{
			name: "When all machines lack NodeInfo it should clear previously set NodesInfo",
			machines: []client.Object{
				&v1beta1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "m1",
						Namespace: "clusters-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "clusters/test-nodepool",
						},
					},
					Status: v1beta1.MachineStatus{},
				},
			},
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "clusters",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
				},
				Status: hyperv1.NodePoolStatus{
					Version: "4.18.12",
					NodesInfo: hyperv1.NodePoolNodesInfo{
						NodeVersions: []hyperv1.NodeVersion{
							{OCPVersion: "4.18.12", KubeletVersion: "v1.31.4", ReadyNodeCount: ptr.To[int32](1), UnreadyNodeCount: ptr.To[int32](0)},
						},
					},
				},
			},
			expectedNodesInfo: hyperv1.NodePoolNodesInfo{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			objs := make([]client.Object, 0, len(tc.machines))
			objs = append(objs, tc.machines...)

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()
			r := &NodePoolReconciler{
				Client: fakeClient,
			}

			err := r.setNodesInfoStatus(t.Context(), tc.nodePool)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(tc.nodePool.Status.NodesInfo).To(Equal(tc.expectedNodesInfo))
		})
	}
}

func machineWithVersionAndHealth(name, kubeletVersion string, healthy bool, annotations map[string]string) *v1beta1.Machine {
	healthStatus := corev1.ConditionTrue
	if !healthy {
		healthStatus = corev1.ConditionFalse
	}
	return machineWithVersionAndConditions(name, kubeletVersion, v1beta1.Conditions{
		{Type: v1beta1.MachineNodeHealthyCondition, Status: healthStatus},
	}, annotations)
}

func machineWithVersionAndConditions(name, kubeletVersion string, conditions v1beta1.Conditions, annotations map[string]string) *v1beta1.Machine {
	return &v1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: annotations,
		},
		Status: v1beta1.MachineStatus{
			NodeInfo:   &corev1.NodeSystemInfo{KubeletVersion: kubeletVersion},
			Conditions: conditions,
		},
	}
}
