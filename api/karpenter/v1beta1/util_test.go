package v1beta1

import (
	"reflect"
	"testing"
	"time"

	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestKarpenterKubeletConfiguration(t *testing.T) {
	testCases := []struct {
		name     string
		spec     OpenshiftEC2NodeClassSpec
		expected *awskarpenterv1.KubeletConfiguration
	}{
		{
			name:     "When Kubelet is nil it should return nil",
			spec:     OpenshiftEC2NodeClassSpec{},
			expected: nil,
		},
		{
			name: "When all karpenter-mapped fields are set it should map only upstream-supported fields",
			spec: OpenshiftEC2NodeClassSpec{
				Kubelet: &KubeletConfiguration{
					MaxPods:     ptr.To(int32(110)),
					PodsPerCore: ptr.To(int32(10)),
					SystemReserved: map[string]string{
						"cpu":    "100m",
						"memory": "256Mi",
					},
					KubeReserved: map[string]string{
						"cpu":    "200m",
						"memory": "512Mi",
					},
					EvictionHard: map[string]string{
						"memory.available": "100Mi",
					},
					EvictionSoft: map[string]string{
						"memory.available": "200Mi",
					},
					EvictionSoftGracePeriod: map[string]metav1.Duration{
						"memory.available": {Duration: 30 * time.Second},
					},
					EvictionMaxPodGracePeriod:   ptr.To(int32(60)),
					ImageGCHighThresholdPercent: ptr.To(int32(85)),
					ImageGCLowThresholdPercent:  ptr.To(int32(80)),
					CPUCFSQuota:                 ptr.To(true),
					// Fields below exist in our struct but NOT in Karpenter's
					PodPidsLimit:         ptr.To(int64(4096)),
					OOMScoreAdj:          ptr.To(int32(-999)),
					AllowedUnsafeSysctls: []string{"net.ipv4.ip_forward"},
					SeccompDefault:       ptr.To(true),
				},
			},
			expected: &awskarpenterv1.KubeletConfiguration{
				MaxPods:     ptr.To(int32(110)),
				PodsPerCore: ptr.To(int32(10)),
				SystemReserved: map[string]string{
					"cpu":    "100m",
					"memory": "256Mi",
				},
				KubeReserved: map[string]string{
					"cpu":    "200m",
					"memory": "512Mi",
				},
				EvictionHard: map[string]string{
					"memory.available": "100Mi",
				},
				EvictionSoft: map[string]string{
					"memory.available": "200Mi",
				},
				EvictionSoftGracePeriod: map[string]metav1.Duration{
					"memory.available": {Duration: 30 * time.Second},
				},
				EvictionMaxPodGracePeriod:   ptr.To(int32(60)),
				ImageGCHighThresholdPercent: ptr.To(int32(85)),
				ImageGCLowThresholdPercent:  ptr.To(int32(80)),
				CPUCFSQuota:                 ptr.To(true),
			},
		},
		{
			name: "When only non-karpenter fields are set it should return non-nil but effectively empty struct",
			spec: OpenshiftEC2NodeClassSpec{
				Kubelet: &KubeletConfiguration{
					PodPidsLimit:         ptr.To(int64(4096)),
					OOMScoreAdj:          ptr.To(int32(-999)),
					AllowedUnsafeSysctls: []string{"net.ipv4.ip_forward"},
				},
			},
			expected: &awskarpenterv1.KubeletConfiguration{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.spec.KarpenterKubeletConfiguration()
			if !reflect.DeepEqual(result, tc.expected) {
				t.Errorf("expected %+v, got %+v", tc.expected, result)
			}
		})
	}
}
