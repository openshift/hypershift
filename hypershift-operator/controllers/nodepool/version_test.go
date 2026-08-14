package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

			r := &NodePoolReconciler{}
			result := r.nodeVersionsFromMachines(tc.machines, tc.nodePool)
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

			machines := make([]*v1beta1.Machine, 0, len(tc.machines))
			for _, obj := range tc.machines {
				machines = append(machines, obj.(*v1beta1.Machine))
			}

			r := &NodePoolReconciler{}
			r.setNodesInfoStatus(tc.nodePool, machines)
			g.Expect(tc.nodePool.Status.NodesInfo).To(Equal(tc.expectedNodesInfo))
		})
	}
}

func TestRhcosStreamFromOSImage(t *testing.T) {
	testCases := []struct {
		name     string
		osImage  string
		expected string
	}{
		{
			name:     "When OSImage is RHCOS 4xx it should return rhel-9",
			osImage:  "Red Hat Enterprise Linux CoreOS 419.97.202505081234-0 (Plow)",
			expected: StreamRHEL9,
		},
		{
			name:     "When OSImage is RHCOS 5xx it should return rhel-10",
			osImage:  "Red Hat Enterprise Linux CoreOS 510.97.202506011200-0 (Plow)",
			expected: StreamRHEL10,
		},
		{
			name:     "When OSImage has different 4xx version it should return rhel-9",
			osImage:  "Red Hat Enterprise Linux CoreOS 418.94.202501011200-0 (Plow)",
			expected: StreamRHEL9,
		},
		{
			name:     "When OSImage is empty it should return empty string",
			osImage:  "",
			expected: "",
		},
		{
			name:     "When OSImage is unrecognized it should return empty string",
			osImage:  "Ubuntu 22.04 LTS",
			expected: "",
		},
		{
			name:     "When OSImage has unknown major version it should return empty string",
			osImage:  "Red Hat Enterprise Linux CoreOS 300.97.202505081234-0 (Plow)",
			expected: "",
		},
		{
			name:     "When OSImage uses new OCP 5.0 format with RHEL 9 it should return rhel-9",
			osImage:  "Red Hat Enterprise Linux CoreOS 9.8.20260721-0 (Plow)",
			expected: StreamRHEL9,
		},
		{
			name:     "When OSImage uses new OCP 5.0 format with RHEL 10 it should return rhel-10",
			osImage:  "Red Hat Enterprise Linux CoreOS 10.2.20260801-0 (Plow)",
			expected: StreamRHEL10,
		},
		{
			name:     "When OSImage uses new format with unknown major it should return empty string",
			osImage:  "Red Hat Enterprise Linux CoreOS 8.5.20260101-0 (Plow)",
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(rhcosStreamFromOSImage(tc.osImage)).To(Equal(tc.expected))
		})
	}
}

func TestOsImageStreamFromMachines(t *testing.T) {
	testCases := []struct {
		name     string
		machines []*v1beta1.Machine
		expected string
	}{
		{
			name:     "When there are no machines it should return empty string",
			machines: nil,
			expected: "",
		},
		{
			name: "When a single machine reports RHEL 9 it should return rhel-9",
			machines: []*v1beta1.Machine{
				machineWithOSImage("m1", "Red Hat Enterprise Linux CoreOS 419.97.202505081234-0 (Plow)"),
			},
			expected: StreamRHEL9,
		},
		{
			name: "When all machines report RHEL 9 it should return rhel-9",
			machines: []*v1beta1.Machine{
				machineWithOSImage("m1", "Red Hat Enterprise Linux CoreOS 419.97.202505081234-0 (Plow)"),
				machineWithOSImage("m2", "Red Hat Enterprise Linux CoreOS 419.97.202505081234-0 (Plow)"),
				machineWithOSImage("m3", "Red Hat Enterprise Linux CoreOS 419.97.202505081234-0 (Plow)"),
			},
			expected: StreamRHEL9,
		},
		{
			name: "When all machines report RHEL 10 it should return rhel-10",
			machines: []*v1beta1.Machine{
				machineWithOSImage("m1", "Red Hat Enterprise Linux CoreOS 510.97.202506011200-0 (Plow)"),
				machineWithOSImage("m2", "Red Hat Enterprise Linux CoreOS 510.97.202506011200-0 (Plow)"),
			},
			expected: StreamRHEL10,
		},
		{
			name: "When a majority reports RHEL 10 during rolling upgrade it should return rhel-10",
			machines: []*v1beta1.Machine{
				machineWithOSImage("m1", "Red Hat Enterprise Linux CoreOS 419.97.202505081234-0 (Plow)"),
				machineWithOSImage("m2", "Red Hat Enterprise Linux CoreOS 510.97.202506011200-0 (Plow)"),
				machineWithOSImage("m3", "Red Hat Enterprise Linux CoreOS 510.97.202506011200-0 (Plow)"),
			},
			expected: StreamRHEL10,
		},
		{
			name: "When streams are evenly split it should return empty string",
			machines: []*v1beta1.Machine{
				machineWithOSImage("m1", "Red Hat Enterprise Linux CoreOS 419.97.202505081234-0 (Plow)"),
				machineWithOSImage("m2", "Red Hat Enterprise Linux CoreOS 510.97.202506011200-0 (Plow)"),
			},
			expected: "",
		},
		{
			name: "When machines have no NodeInfo it should return empty string",
			machines: []*v1beta1.Machine{
				{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: v1beta1.MachineStatus{}},
			},
			expected: "",
		},
		{
			name: "When machines have unrecognized OSImage it should return empty string",
			machines: []*v1beta1.Machine{
				machineWithOSImage("m1", "Ubuntu 22.04 LTS"),
			},
			expected: "",
		},
		{
			name: "When some machines have no NodeInfo it should count only those with NodeInfo",
			machines: []*v1beta1.Machine{
				machineWithOSImage("m1", "Red Hat Enterprise Linux CoreOS 510.97.202506011200-0 (Plow)"),
				{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: v1beta1.MachineStatus{}},
				{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: v1beta1.MachineStatus{}},
			},
			expected: StreamRHEL10,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(osImageStreamFromMachines(tc.machines)).To(Equal(tc.expected))
		})
	}
}

func TestSetOSImageStreamStatus(t *testing.T) {
	testCases := []struct {
		name                  string
		machines              []client.Object
		nodePool              *hyperv1.NodePool
		expectedOSImageStream hyperv1.OSImageStreamReference
	}{
		{
			name:     "When no machines exist it should not change OSImageStream status",
			machines: nil,
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nodepool",
					Namespace: "clusters",
				},
				Spec: hyperv1.NodePoolSpec{
					ClusterName: "test-cluster",
				},
			},
			expectedOSImageStream: hyperv1.OSImageStreamReference{},
		},
		{
			name: "When all machines report RHEL 9 it should set status to rhel-9",
			machines: []client.Object{
				&v1beta1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "m1",
						Namespace: "clusters-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "clusters/test-nodepool",
						},
					},
					Status: v1beta1.MachineStatus{
						NodeInfo: &corev1.NodeSystemInfo{
							OSImage: "Red Hat Enterprise Linux CoreOS 419.97.202505081234-0 (Plow)",
						},
					},
				},
				&v1beta1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "m2",
						Namespace: "clusters-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "clusters/test-nodepool",
						},
					},
					Status: v1beta1.MachineStatus{
						NodeInfo: &corev1.NodeSystemInfo{
							OSImage: "Red Hat Enterprise Linux CoreOS 419.97.202505081234-0 (Plow)",
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
			},
			expectedOSImageStream: hyperv1.OSImageStreamReference{Name: StreamRHEL9},
		},
		{
			name: "When majority reports RHEL 10 it should set status to rhel-10",
			machines: []client.Object{
				&v1beta1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "m1",
						Namespace: "clusters-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "clusters/test-nodepool",
						},
					},
					Status: v1beta1.MachineStatus{
						NodeInfo: &corev1.NodeSystemInfo{
							OSImage: "Red Hat Enterprise Linux CoreOS 419.97.202505081234-0 (Plow)",
						},
					},
				},
				&v1beta1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "m2",
						Namespace: "clusters-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "clusters/test-nodepool",
						},
					},
					Status: v1beta1.MachineStatus{
						NodeInfo: &corev1.NodeSystemInfo{
							OSImage: "Red Hat Enterprise Linux CoreOS 510.97.202506011200-0 (Plow)",
						},
					},
				},
				&v1beta1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "m3",
						Namespace: "clusters-test-cluster",
						Annotations: map[string]string{
							nodePoolAnnotation: "clusters/test-nodepool",
						},
					},
					Status: v1beta1.MachineStatus{
						NodeInfo: &corev1.NodeSystemInfo{
							OSImage: "Red Hat Enterprise Linux CoreOS 510.97.202506011200-0 (Plow)",
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
			},
			expectedOSImageStream: hyperv1.OSImageStreamReference{Name: StreamRHEL10},
		},
		{
			name: "When previous status exists and no machines have NodeInfo it should preserve previous status",
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
					OSImageStream: hyperv1.OSImageStreamReference{Name: StreamRHEL9},
				},
			},
			expectedOSImageStream: hyperv1.OSImageStreamReference{Name: StreamRHEL9},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			machines := make([]*v1beta1.Machine, 0, len(tc.machines))
			for _, obj := range tc.machines {
				machines = append(machines, obj.(*v1beta1.Machine))
			}

			r := &NodePoolReconciler{}
			r.setOSImageStreamStatus(tc.nodePool, machines)
			g.Expect(tc.nodePool.Status.OSImageStream).To(Equal(tc.expectedOSImageStream))
		})
	}
}

func machineWithOSImage(name, osImage string) *v1beta1.Machine {
	return &v1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Status: v1beta1.MachineStatus{
			NodeInfo: &corev1.NodeSystemInfo{
				OSImage: osImage,
			},
		},
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
