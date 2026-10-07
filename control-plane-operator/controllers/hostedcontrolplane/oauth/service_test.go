package oauth

import (
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	supportazureutil "github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestOauthServiceReconcile(t *testing.T) {
	testCases := []struct {
		name              string
		platform          v1beta1.PlatformType
		strategy          v1beta1.ServicePublishingStrategy
		isPrivate         bool
		svc_in            corev1.Service
		svc_out           corev1.Service
		absentAnnotations []string
		err               error
	}{
		{
			name:     "When IBM Cloud platform uses NodePort strategy with a port, it should populate the NodePort from the strategy",
			platform: v1beta1.IBMCloudPlatform,
			strategy: v1beta1.ServicePublishingStrategy{Type: v1beta1.NodePort, NodePort: &v1beta1.NodePortPublishingStrategy{Port: 1125}},
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       6443,
						TargetPort: intstr.IntOrString{IntVal: 6443},
						NodePort:   1125,
					},
				},
			}},
			err: nil,
		},
		{
			name:     "When IBM Cloud platform uses Route strategy on an existing NodePort service, it should preserve the existing service type and port",
			platform: v1beta1.IBMCloudPlatform,
			strategy: v1beta1.ServicePublishingStrategy{Type: v1beta1.Route},
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       6443,
						TargetPort: intstr.IntOrString{IntVal: 6443},
						NodePort:   1125,
					},
				},
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeNodePort,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       6443,
						TargetPort: intstr.IntOrString{IntVal: 6443},
						NodePort:   1125,
					},
				},
			}},
			err: nil,
		},
		{
			name:     "When non-IBM Cloud platform uses Route strategy, it should set ClusterIP type with port 6443",
			platform: v1beta1.AWSPlatform,
			strategy: v1beta1.ServicePublishingStrategy{Type: v1beta1.Route},
			svc_in: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
			}},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       6443,
						TargetPort: intstr.IntOrString{IntVal: 6443},
					},
				},
			}},
			err: nil,
		},
		{
			name:     "When an invalid publishing strategy is used, it should return an error",
			strategy: v1beta1.ServicePublishingStrategy{Type: v1beta1.S3},
			err:      fmt.Errorf("invalid publishing strategy for OAuth service: S3"),
		},
		{
			name:     "When non-Azure platform uses LoadBalancer strategy, it should return an error",
			platform: v1beta1.AWSPlatform,
			strategy: v1beta1.ServicePublishingStrategy{Type: v1beta1.LoadBalancer},
			err:      fmt.Errorf("LoadBalancer publishing strategy for OAuth service is only supported on self-managed Azure, got platform: AWS"),
		},
		{
			name:     "When public Azure platform uses LoadBalancer strategy, it should set service type to LoadBalancer without the internal LB annotation",
			platform: v1beta1.AzurePlatform,
			strategy: v1beta1.ServicePublishingStrategy{Type: v1beta1.LoadBalancer},
			svc_in:   corev1.Service{},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeLoadBalancer,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       443,
						TargetPort: intstr.IntOrString{IntVal: 6443},
					},
				},
			}},
			absentAnnotations: []string{supportazureutil.InternalLoadBalancerAnnotation},
			err:               nil,
		},
		{
			name:     "When Azure platform uses LoadBalancer strategy with hostname, it should set the ExternalDNS annotation",
			platform: v1beta1.AzurePlatform,
			strategy: v1beta1.ServicePublishingStrategy{
				Type: v1beta1.LoadBalancer,
				LoadBalancer: &v1beta1.LoadBalancerPublishingStrategy{
					Hostname: "oauth-mycluster.example.com",
				},
			},
			svc_in: corev1.Service{},
			svc_out: corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						v1beta1.ExternalDNSHostnameAnnotation: "oauth-mycluster.example.com",
					},
				},
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeLoadBalancer,
					Ports: []corev1.ServicePort{
						{
							Protocol:   corev1.ProtocolTCP,
							Port:       443,
							TargetPort: intstr.IntOrString{IntVal: 6443},
						},
					},
				},
			},
			err: nil,
		},
		{
			name:      "When private Azure platform uses LoadBalancer strategy, it should set the internal LB annotation",
			platform:  v1beta1.AzurePlatform,
			isPrivate: true,
			strategy: v1beta1.ServicePublishingStrategy{
				Type: v1beta1.LoadBalancer,
				LoadBalancer: &v1beta1.LoadBalancerPublishingStrategy{
					Hostname: "oauth-mycluster.example.com",
				},
			},
			svc_in: corev1.Service{},
			svc_out: corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						v1beta1.ExternalDNSHostnameAnnotation:           "oauth-mycluster.example.com",
						supportazureutil.InternalLoadBalancerAnnotation: supportazureutil.InternalLoadBalancerValue,
					},
				},
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeLoadBalancer,
					Ports: []corev1.ServicePort{
						{
							Protocol:   corev1.ProtocolTCP,
							Port:       443,
							TargetPort: intstr.IntOrString{IntVal: 6443},
						},
					},
				},
			},
			err: nil,
		},
		{
			name:     "When public Azure LB service has a stale internal LB annotation, it should remove it",
			platform: v1beta1.AzurePlatform,
			strategy: v1beta1.ServicePublishingStrategy{Type: v1beta1.LoadBalancer},
			svc_in: corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						supportazureutil.InternalLoadBalancerAnnotation: supportazureutil.InternalLoadBalancerValue,
					},
				},
			},
			svc_out: corev1.Service{Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeLoadBalancer,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       443,
						TargetPort: intstr.IntOrString{IntVal: 6443},
					},
				},
			}},
			absentAnnotations: []string{supportazureutil.InternalLoadBalancerAnnotation},
			err:               nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			err := ReconcileService(&tc.svc_in, config.OwnerRef{}, &tc.strategy, tc.platform, tc.isPrivate)

			if tc.err == nil {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(tc.svc_in.Spec.Type).To(Equal(tc.svc_out.Spec.Type))
				g.Expect(tc.svc_in.Spec.Ports).To(Equal(tc.svc_out.Spec.Ports))
				for k, v := range tc.svc_out.Annotations {
					g.Expect(tc.svc_in.Annotations).To(HaveKeyWithValue(k, v))
				}
				for _, k := range tc.absentAnnotations {
					g.Expect(tc.svc_in.Annotations).ToNot(HaveKey(k))
				}
			} else {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tc.err.Error()))
			}
		})
	}
}

func TestReconcileServiceStatus(t *testing.T) {
	testCases := []struct {
		name            string
		svc             *corev1.Service
		route           *routev1.Route
		strategy        *v1beta1.ServicePublishingStrategy
		expectedHost    string
		expectedPort    int32
		expectedMessage string
		expectedErr     error
	}{
		{
			name:  "When LoadBalancer strategy has a configured hostname, it should return the hostname and port 443",
			svc:   &corev1.Service{},
			route: &routev1.Route{},
			strategy: &v1beta1.ServicePublishingStrategy{
				Type: v1beta1.LoadBalancer,
				LoadBalancer: &v1beta1.LoadBalancerPublishingStrategy{
					Hostname: "oauth.example.com",
				},
			},
			expectedHost: "oauth.example.com",
			expectedPort: 443,
		},
		{
			name: "When LoadBalancer has an ingress IP address, it should return the IP and port 443",
			svc: &corev1.Service{
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{IP: "10.0.0.1"},
						},
					},
				},
			},
			route: &routev1.Route{},
			strategy: &v1beta1.ServicePublishingStrategy{
				Type: v1beta1.LoadBalancer,
			},
			expectedHost: "10.0.0.1",
			expectedPort: 443,
		},
		{
			name: "When LoadBalancer has an ingress hostname, it should return the hostname and port 443",
			svc: &corev1.Service{
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{Hostname: "internal-lb.azure.example.com"},
						},
					},
				},
			},
			route: &routev1.Route{},
			strategy: &v1beta1.ServicePublishingStrategy{
				Type: v1beta1.LoadBalancer,
			},
			expectedHost: "internal-lb.azure.example.com",
			expectedPort: 443,
		},
		{
			name: "When LoadBalancer has no ingress yet, it should return a message indicating the LB is not provisioned",
			svc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
				},
			},
			route: &routev1.Route{},
			strategy: &v1beta1.ServicePublishingStrategy{
				Type: v1beta1.LoadBalancer,
			},
			expectedHost:    "",
			expectedPort:    0,
			expectedMessage: "OAuth LoadBalancer not yet provisioned; 5m since creation",
		},
		{
			name: "When LoadBalancer ingress has neither hostname nor IP, it should return a message indicating blank ingress",
			svc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.NewTime(time.Now().Add(-3 * time.Minute)),
				},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{},
						},
					},
				},
			},
			route: &routev1.Route{},
			strategy: &v1beta1.ServicePublishingStrategy{
				Type: v1beta1.LoadBalancer,
			},
			expectedHost:    "",
			expectedPort:    0,
			expectedMessage: "OAuth LoadBalancer ingress has no hostname or IP",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			host, port, message, err := ReconcileServiceStatus(tc.svc, tc.route, tc.strategy)
			if tc.expectedErr != nil {
				g.Expect(err).To(MatchError(tc.expectedErr))
			} else {
				g.Expect(err).To(BeNil())
			}
			g.Expect(host).To(Equal(tc.expectedHost))
			g.Expect(port).To(Equal(tc.expectedPort))
			if tc.expectedMessage != "" {
				g.Expect(message).To(ContainSubstring(tc.expectedMessage))
			} else {
				g.Expect(message).To(BeEmpty())
			}
		})
	}
}
