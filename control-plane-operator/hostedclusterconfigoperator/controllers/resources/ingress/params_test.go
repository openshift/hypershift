package ingress

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
	v1 "github.com/openshift/api/operator/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewIngressParams(t *testing.T) {
	type args struct {
		hcp *hyperv1.HostedControlPlane
	}
	tests := []struct {
		name string
		args args
		want *IngressParams
	}{
		{
			name: "DefaultParams",
			args: args{
				hcp: &hyperv1.HostedControlPlane{}},
			want: &IngressParams{
				IngressSubdomain:  "apps.",
				Replicas:          1,
				IsPrivate:         false,
				IBMCloudUPI:       false,
				AWSNLB:            false,
				LoadBalancerScope: v1.ExternalLoadBalancer,
			},
		},
		{
			name: "PrivateIngress",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							hyperv1.PrivateIngressControllerAnnotation: "true",
						},
					},
				},
			},
			want: &IngressParams{
				IngressSubdomain:  "apps.",
				Replicas:          1,
				IsPrivate:         true,
				IBMCloudUPI:       false,
				AWSNLB:            false,
				LoadBalancerScope: v1.ExternalLoadBalancer,
			},
		},
		{
			name: "HighlyAvailable",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						InfrastructureAvailabilityPolicy: hyperv1.HighlyAvailable,
					},
				},
			},
			want: &IngressParams{
				IngressSubdomain:  "apps.",
				Replicas:          2,
				IsPrivate:         false,
				IBMCloudUPI:       false,
				AWSNLB:            false,
				LoadBalancerScope: v1.ExternalLoadBalancer,
			},
		},
		{
			name: "IBMCloudUPI",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							IBMCloud: &hyperv1.IBMCloudPlatformSpec{
								ProviderType: configv1.IBMCloudProviderTypeUPI,
							},
						},
					},
				},
			},
			want: &IngressParams{
				Replicas:          1,
				IngressSubdomain:  "apps.",
				IsPrivate:         false,
				IBMCloudUPI:       true,
				AWSNLB:            false,
				LoadBalancerScope: v1.ExternalLoadBalancer,
			},
		},
		{
			name: "AWSNLB",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Configuration: &hyperv1.ClusterConfiguration{
							Ingress: &configv1.IngressSpec{
								LoadBalancer: configv1.LoadBalancer{
									Platform: configv1.IngressPlatformSpec{
										AWS: &configv1.AWSIngressSpec{
											Type: configv1.NLB,
										},
									},
								},
							},
						},
						Platform: hyperv1.PlatformSpec{
							AWS: &hyperv1.AWSPlatformSpec{},
						},
					},
				},
			},
			want: &IngressParams{
				IngressSubdomain:  "apps.",
				Replicas:          1,
				IsPrivate:         false,
				IBMCloudUPI:       false,
				AWSNLB:            true,
				LoadBalancerScope: v1.ExternalLoadBalancer,
			},
		},
		{
			name: "AWSInternalNLB",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Configuration: &hyperv1.ClusterConfiguration{
							Ingress: &configv1.IngressSpec{
								LoadBalancer: configv1.LoadBalancer{
									Platform: configv1.IngressPlatformSpec{
										AWS: &configv1.AWSIngressSpec{
											Type: configv1.NLB,
										},
									},
								},
							},
						},
						Platform: hyperv1.PlatformSpec{
							AWS: &hyperv1.AWSPlatformSpec{
								EndpointAccess: hyperv1.Private,
							},
						},
					},
				},
			},
			want: &IngressParams{
				IngressSubdomain:  "apps.",
				Replicas:          1,
				IsPrivate:         false,
				IBMCloudUPI:       false,
				AWSNLB:            true,
				LoadBalancerScope: v1.InternalLoadBalancer,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			got := NewIngressParams(tt.args.hcp)
			g.Expect(got).To(BeEquivalentTo(tt.want))
		})
	}
}
