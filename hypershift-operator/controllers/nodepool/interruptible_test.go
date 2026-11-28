package nodepool

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsInterruptibleInstanceEnabled(t *testing.T) {
	testCases := []struct {
		name     string
		nodePool *hyperv1.NodePool
		expected bool
	}{
		{
			name:     "When nodePool is nil, it should return false",
			nodePool: nil,
			expected: false,
		},
		{
			name: "When no interruptible config and no annotation, it should return false",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{},
					},
				},
			},
			expected: false,
		},
		{
			name: "When annotation is present, it should return true",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						AnnotationEnableSpot: "true",
					},
				},
			},
			expected: true,
		},
		{
			name: "When AWS API marketType is Spot, it should return true",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeSpot,
								Spot:       hyperv1.SpotOptions{},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "When AWS API marketType is Spot with MaxPrice, it should return true",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeSpot,
								Spot: hyperv1.SpotOptions{
									MaxPrice: "0.50",
								},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "When AWS marketType is OnDemand, it should return false",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeOnDemand,
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When AWS marketType is CapacityBlocks, it should return false",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{
							Placement: &hyperv1.PlacementOptions{
								MarketType: hyperv1.MarketTypeCapacityBlock,
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When AWS Placement is nil, it should return false",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						AWS: &hyperv1.AWSNodePoolPlatform{},
					},
				},
			},
			expected: false,
		},
		{
			name: "When GCP provisioning model is Spot, it should return true",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						GCP: &hyperv1.GCPNodePoolPlatform{
							ProvisioningModel: hyperv1.GCPProvisioningModelSpot,
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "When GCP provisioning model is Preemptible, it should return true",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						GCP: &hyperv1.GCPNodePoolPlatform{
							ProvisioningModel: hyperv1.GCPProvisioningModelPreemptible,
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "When GCP provisioning model is Standard, it should return false",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{
						GCP: &hyperv1.GCPNodePoolPlatform{
							ProvisioningModel: hyperv1.GCPProvisioningModelStandard,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When platform specs are nil, it should return false",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Platform: hyperv1.NodePoolPlatform{},
				},
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isInterruptibleInstanceEnabled(tc.nodePool)
			if result != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, result)
			}
		})
	}
}
