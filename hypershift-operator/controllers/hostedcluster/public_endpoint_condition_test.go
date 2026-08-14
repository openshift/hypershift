package hostedcluster

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	api "github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func sharedIngressService(clusterIP string, lbHostname string) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "router",
			Namespace: "hypershift-sharedingress",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: clusterIP,
		},
	}
	if lbHostname != "" {
		svc.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{
			{Hostname: lbHostname},
		}
	}
	return svc
}

func swiftHostedCluster(name string, topology hyperv1.AzureTopologyType, kasHostname string) *hyperv1.HostedCluster {
	return &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  "clusters",
			Generation: 1,
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AzurePlatform,
				Azure: &hyperv1.AzurePlatformSpec{
					Topology: topology,
					Private: hyperv1.AzurePrivateSpec{
						Type: hyperv1.AzurePrivateTypeSwift,
						Swift: hyperv1.AzureSwiftSpec{
							PodNetworkInstance: "test-pni",
						},
					},
					AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
						AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities,
					},
				},
			},
			Services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service: hyperv1.APIServer,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type: hyperv1.Route,
						Route: &hyperv1.RoutePublishingStrategy{
							Hostname: kasHostname,
						},
					},
				},
			},
		},
	}
}

func TestReconcilePublicEndpointExposedCondition(t *testing.T) {
	tests := []struct {
		name            string
		hc              *hyperv1.HostedCluster
		svc             *corev1.Service
		probeResult     bool
		expectCondition bool
		expectStatus    metav1.ConditionStatus
		expectReason    string
		expectRequeue   bool
	}{
		{
			name: "When cluster is not Swift it should not set condition",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters", Generation: 1},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			expectCondition: false,
		},
		{
			name:            "When shared ingress service is not found it should not set condition",
			hc:              swiftHostedCluster("test", hyperv1.AzureTopologyPublicAndPrivate, "api.test.example.com"),
			svc:             nil,
			expectCondition: false,
		},
		{
			name:            "When KAS hostname is not configured it should not set condition",
			hc:              swiftHostedCluster("test", hyperv1.AzureTopologyPublicAndPrivate, ""),
			svc:             sharedIngressService("10.0.0.1", "lb.example.com"),
			expectCondition: false,
		},
		{
			name:            "When LB is not ready and cluster uses shared ingress it should set False with ConvergenceInProgress",
			hc:              swiftHostedCluster("test", hyperv1.AzureTopologyPublicAndPrivate, "api.test.example.com"),
			svc:             sharedIngressService("10.0.0.1", ""),
			expectCondition: true,
			expectStatus:    metav1.ConditionFalse,
			expectReason:    hyperv1.PublicEndpointConvergenceInProgressReason,
			expectRequeue:   true,
		},
		{
			name:            "When probe succeeds and cluster uses shared ingress it should set True with SharedIngressConfigured",
			hc:              swiftHostedCluster("test", hyperv1.AzureTopologyPublicAndPrivate, "api.test.example.com"),
			svc:             sharedIngressService("10.0.0.1", "lb.example.com"),
			probeResult:     true,
			expectCondition: true,
			expectStatus:    metav1.ConditionTrue,
			expectReason:    hyperv1.PublicEndpointSharedIngressConfiguredReason,
			expectRequeue:   false,
		},
		{
			name:            "When probe fails and cluster uses shared ingress it should set False with ConvergenceInProgress",
			hc:              swiftHostedCluster("test", hyperv1.AzureTopologyPublicAndPrivate, "api.test.example.com"),
			svc:             sharedIngressService("10.0.0.1", "lb.example.com"),
			probeResult:     false,
			expectCondition: true,
			expectStatus:    metav1.ConditionFalse,
			expectReason:    hyperv1.PublicEndpointConvergenceInProgressReason,
			expectRequeue:   true,
		},
		{
			name:            "When probe succeeds but cluster is Private it should set True with ConvergenceInProgress",
			hc:              swiftHostedCluster("test", hyperv1.AzureTopologyPrivate, "api.test.example.com"),
			svc:             sharedIngressService("10.0.0.1", "lb.example.com"),
			probeResult:     true,
			expectCondition: true,
			expectStatus:    metav1.ConditionTrue,
			expectReason:    hyperv1.PublicEndpointConvergenceInProgressReason,
			expectRequeue:   true,
		},
		{
			name:            "When probe fails and cluster is Private it should set False with TopologyPrivate",
			hc:              swiftHostedCluster("test", hyperv1.AzureTopologyPrivate, "api.test.example.com"),
			svc:             sharedIngressService("10.0.0.1", "lb.example.com"),
			probeResult:     false,
			expectCondition: true,
			expectStatus:    metav1.ConditionFalse,
			expectReason:    hyperv1.PublicEndpointTopologyPrivateReason,
			expectRequeue:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			objects := []crclient.Object{tt.hc}
			if tt.svc != nil {
				objects = append(objects, tt.svc)
			}
			cl := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objects...).
				WithStatusSubresource(tt.hc).
				Build()

			r := &HostedClusterReconciler{
				Client: cl,
				ProbeSharedIngressEndpoint: func(context context.Context, serviceIP string, servicePort int, kasHostname string) bool {
					return tt.probeResult
				},
			}

			requeue, err := r.reconcilePublicEndpointExposedCondition(t.Context(), tt.hc)
			g.Expect(err).ToNot(HaveOccurred())

			if tt.expectRequeue {
				g.Expect(requeue).ToNot(BeNil(), "expected requeue")
				g.Expect(*requeue).To(Equal(10 * time.Second))
			} else {
				g.Expect(requeue).To(BeNil(), "expected no requeue")
			}

			updatedHC := &hyperv1.HostedCluster{}
			g.Expect(cl.Get(t.Context(), crclient.ObjectKeyFromObject(tt.hc), updatedHC)).To(Succeed())

			condition := meta.FindStatusCondition(updatedHC.Status.Conditions, string(hyperv1.PublicEndpointExposed))
			if !tt.expectCondition {
				g.Expect(condition).To(BeNil(), "expected no PublicEndpointExposed condition")
				return
			}

			g.Expect(condition).ToNot(BeNil(), "expected PublicEndpointExposed condition to be set")
			g.Expect(condition.Status).To(Equal(tt.expectStatus), "unexpected condition status")
			g.Expect(condition.Reason).To(Equal(tt.expectReason), "unexpected condition reason")
			g.Expect(condition.ObservedGeneration).To(Equal(tt.hc.Generation))
		})
	}
}
