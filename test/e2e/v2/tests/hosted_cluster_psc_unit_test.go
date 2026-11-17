//go:build e2ev2

package tests

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"google.golang.org/api/compute/v1"
)

func TestMatchesRouterForwardingRule(t *testing.T) {
	const pool = "projects/project/regions/us-east1/targetPools/router-pool"
	tests := []struct {
		name        string
		annotations map[string]string
		ingressIPs  []string
		rule        *compute.ForwardingRule
		want        bool
	}{
		{
			name:        "When the current backend annotation matches, it should select the forwarding rule",
			annotations: map[string]string{backendServiceAnnotation: "router-backend"},
			rule:        &compute.ForwardingRule{BackendService: "projects/project/regions/us-east1/backendServices/router-backend"},
			want:        true,
		},
		{
			name:        "When the legacy backend annotation matches, it should select the forwarding rule",
			annotations: map[string]string{legacyBackendServiceAnnotation: "router-backend"},
			rule:        &compute.ForwardingRule{BackendService: "projects/project/regions/us-east1/backendServices/router-backend"},
			want:        true,
		},
		{
			name:        "When a backend annotation contains a resource URL, it should select the forwarding rule",
			annotations: map[string]string{legacyBackendServiceAnnotation: "projects/project/regions/us-east1/backendServices/router-backend"},
			rule:        &compute.ForwardingRule{BackendService: "projects/project/regions/us-east1/backendServices/router-backend"},
			want:        true,
		},
		{
			name:        "When both backend annotations exist, it should prefer the current annotation",
			annotations: map[string]string{backendServiceAnnotation: "current", legacyBackendServiceAnnotation: "legacy"},
			rule:        &compute.ForwardingRule{BackendService: "projects/project/regions/us-east1/backendServices/legacy"},
		},
		{
			name:        "When a target pool and Service IP match, it should select the forwarding rule",
			annotations: map[string]string{targetPoolAnnotation: "router-pool"},
			ingressIPs:  []string{"192.0.2.1"},
			rule:        &compute.ForwardingRule{Target: pool, IPAddress: "192.0.2.1"},
			want:        true,
		},
		{
			name:        "When a target pool matches a different Service IP, it should reject the forwarding rule",
			annotations: map[string]string{targetPoolAnnotation: "router-pool"},
			ingressIPs:  []string{"192.0.2.2"},
			rule:        &compute.ForwardingRule{Target: pool, IPAddress: "192.0.2.1"},
		},
		{
			name:        "When a different target pool uses the same IP, it should reject the forwarding rule",
			annotations: map[string]string{targetPoolAnnotation: "router-pool"},
			ingressIPs:  []string{"192.0.2.1"},
			rule:        &compute.ForwardingRule{Target: "projects/project/regions/us-east1/targetPools/other", IPAddress: "192.0.2.1"},
		},
		{
			name:        "When the target-pool rule and Service have empty IPs, it should reject the forwarding rule",
			annotations: map[string]string{targetPoolAnnotation: "router-pool"},
			ingressIPs:  []string{""},
			rule:        &compute.ForwardingRule{Target: pool},
		},
		{
			name:        "When a target has the same name but is not a target pool, it should reject the forwarding rule",
			annotations: map[string]string{targetPoolAnnotation: "router-pool"},
			ingressIPs:  []string{"192.0.2.1"},
			rule:        &compute.ForwardingRule{Target: "projects/project/regions/us-east1/targetInstances/router-pool", IPAddress: "192.0.2.1"},
		},
		{
			name:        "When the Service IP is not available, it should reject the forwarding rule",
			annotations: map[string]string{targetPoolAnnotation: "router-pool"},
			rule:        &compute.ForwardingRule{Target: pool, IPAddress: "192.0.2.1"},
		},
		{
			name: "When no discovery annotation exists, it should reject the forwarding rule",
			rule: &compute.ForwardingRule{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Annotations: tt.annotations}}
			for _, ip := range tt.ingressIPs {
				service.Status.LoadBalancer.Ingress = append(service.Status.LoadBalancer.Ingress, corev1.LoadBalancerIngress{IP: ip})
			}
			if got := matchesRouterForwardingRule(service, tt.rule); got != tt.want {
				t.Fatalf("matchesRouterForwardingRule = %t, want %t", got, tt.want)
			}
		})
	}
}
