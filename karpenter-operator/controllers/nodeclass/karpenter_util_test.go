package nodeclass

import (
	"encoding/json"
	"testing"

	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"github.com/stretchr/testify/require"
)

func TestKarpenterKubeletConfigurationFromNodeClassSpec(t *testing.T) {
	testCases := []struct {
		name         string
		spec         hyperkarpenterv1.OpenshiftEC2NodeClassSpec
		expectedJSON string
	}{
		{
			name:         "When Kubelet is nil, it should return nil",
			spec:         hyperkarpenterv1.OpenshiftEC2NodeClassSpec{},
			expectedJSON: "null",
		},
		{
			name: "When all karpenter-mapped fields are set, it should map them",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Kubelet: hyperkarpenterv1.KubeletConfiguration{
					MaxPods:     110,
					PodsPerCore: 10,
					SystemReserved: map[string]string{
						"cpu":    "100m",
						"memory": "256Mi",
					},
					KubeReserved: map[string]string{
						"cpu":    "200m",
						"memory": "512Mi",
					},
					EvictionHard: map[string]hyperkarpenterv1.EvictionThreshold{
						"memory.available": "100Mi",
					},
					EvictionSoft: map[string]hyperkarpenterv1.EvictionThreshold{
						"memory.available": "200Mi",
					},
					EvictionSoftGracePeriod: map[string]string{
						"memory.available": "60s",
					},
					EvictionMaxPodGracePeriod:   ptr.To(int32(60)),
					ImageGCHighThresholdPercent: ptr.To(int32(85)),
					ImageGCLowThresholdPercent:  ptr.To(int32(80)),
					CPUCFSQuota:                 ptr.To(true),
					Overflow:                    runtime.RawExtension{Raw: []byte(`{"podPidsLimit":4096}`)},
				},
			},
			expectedJSON: `{
				"maxPods": 110,
				"podsPerCore": 10,
				"systemReserved": {"cpu": "100m", "memory": "256Mi"},
				"kubeReserved": {"cpu": "200m", "memory": "512Mi"},
				"evictionHard": {"memory.available": "100Mi"},
				"evictionSoft": {"memory.available": "200Mi"},
				"evictionSoftGracePeriod": {"memory.available": "1m0s"},
				"evictionMaxPodGracePeriod": 60,
				"imageGCHighThresholdPercent": 85,
				"imageGCLowThresholdPercent": 80,
				"cpuCFSQuota": true
			}`,
		},
		{
			name: "When only some fields are set, it should map only those",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Kubelet: hyperkarpenterv1.KubeletConfiguration{
					MaxPods: 50,
				},
			},
			expectedJSON: `{"maxPods":50}`,
		},
		{
			name: "When only overflow fields are set, it should return nil",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Kubelet: hyperkarpenterv1.KubeletConfiguration{
					Overflow: runtime.RawExtension{Raw: []byte(`{"podPidsLimit":4096}`)},
				},
			},
			expectedJSON: "null",
		},
		{
			name: "When pointer fields explicitly contain zero or false, it should preserve their values",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Kubelet: hyperkarpenterv1.KubeletConfiguration{
					CPUCFSQuota:                ptr.To(false),
					EvictionMaxPodGracePeriod:  ptr.To(int32(0)),
					ImageGCLowThresholdPercent: ptr.To(int32(0)),
				},
			},
			expectedJSON: `{"cpuCFSQuota":false,"evictionMaxPodGracePeriod":0,"imageGCLowThresholdPercent":0}`,
		},
		{
			name: "When maps are empty and numeric fields are unset, it should omit them",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Kubelet: hyperkarpenterv1.KubeletConfiguration{
					CPUCFSQuota:             ptr.To(true),
					SystemReserved:          map[string]string{},
					KubeReserved:            map[string]string{},
					EvictionHard:            map[string]hyperkarpenterv1.EvictionThreshold{},
					EvictionSoft:            map[string]hyperkarpenterv1.EvictionThreshold{},
					EvictionSoftGracePeriod: map[string]string{},
				},
			},
			expectedJSON: `{"cpuCFSQuota":true}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := karpenterKubeletConfigurationFromNodeClassSpec(tc.spec)
			data, err := json.Marshal(result)
			require.NoError(t, err)
			require.JSONEq(t, tc.expectedJSON, string(data))
		})
	}
}
