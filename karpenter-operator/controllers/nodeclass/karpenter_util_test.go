package nodeclass

import (
	"reflect"
	"testing"
	"time"

	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"

	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
)

func TestKarpenterKubeletConfigurationFromNodeClassSpec(t *testing.T) {
	testCases := []struct {
		name     string
		spec     hyperkarpenterv1.OpenshiftEC2NodeClassSpec
		expected *awskarpenterv1.KubeletConfiguration
	}{
		{
			name:     "When Kubelet is nil it should return nil",
			spec:     hyperkarpenterv1.OpenshiftEC2NodeClassSpec{},
			expected: nil,
		},
		{
			name: "When all karpenter-mapped fields are set it should map them",
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
						"memory.available": "30s",
					},
					EvictionMaxPodGracePeriod:   ptr.To(int32(60)),
					ImageGCHighThresholdPercent: ptr.To(int32(85)),
					ImageGCLowThresholdPercent:  ptr.To(int32(80)),
					CPUCFSQuota:                 ptr.To(true),
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
			name: "When only some fields are set it should map only those",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Kubelet: hyperkarpenterv1.KubeletConfiguration{
					MaxPods: 50,
				},
			},
			expected: &awskarpenterv1.KubeletConfiguration{
				MaxPods: ptr.To(int32(50)),
			},
		},
		{
			name: "When only overflow fields are set it should return nil",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Kubelet: hyperkarpenterv1.KubeletConfiguration{
					Overflow: runtime.RawExtension{Raw: []byte(`{"podPidsLimit":4096}`)},
				},
			},
			expected: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := karpenterKubeletConfigurationFromNodeClassSpec(tc.spec)
			if !reflect.DeepEqual(result, tc.expected) {
				t.Errorf("expected %+v, got %+v", tc.expected, result)
			}
		})
	}
}
