package ingress

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

// When HCP has AWSLoadBalancerSubnetsAnnotation it should NOT set Service subnets annotation.
// When HCP has AWSLoadBalancerTargetNodesAnnotation it should set Service target node labels annotation.
func TestReconcileRouterServiceAnnotations(t *testing.T) {
	// Given a HostedControlPlane on AWS with annotations set
	hcp := &hyperv1.HostedControlPlane{}
	hcp.Spec.Platform.Type = hyperv1.AWSPlatform
	targetNodesLabel := "osd-fleet-manager.openshift.io/paired-nodes=serving-123"
	hcp.Annotations = map[string]string{
		hyperv1.AWSLoadBalancerSubnetsAnnotation:     "subnet-123,subnet-456",
		hyperv1.AWSLoadBalancerTargetNodesAnnotation: targetNodesLabel,
	}

	// And an empty Service to reconcile
	svc := &corev1.Service{}

	// When reconciling the router service
	if err := ReconcileRouterService(svc, true /* internal */, true /* cross-zone */, hcp); err != nil {
		t.Fatalf("ReconcileRouterService returned error: %v", err)
	}

	// Then the AWS subnets annotation must NOT be set on the Service
	const subnetsKey = "service.beta.kubernetes.io/aws-load-balancer-subnets"
	if svc.Annotations != nil {
		if _, exists := svc.Annotations[subnetsKey]; exists {
			t.Fatalf("expected %q annotation to be absent, but it was set to %q", subnetsKey, svc.Annotations[subnetsKey])
		}
	}

	// And the Service should still be configured as an internal NLB on AWS
	if got := svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-type"]; got != "nlb" {
		t.Fatalf("expected NLB annotation to be 'nlb', got %q", got)
	}
	if got := svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-internal"]; got != "true" {
		t.Fatalf("expected internal LB annotation to be 'true', got %q", got)
	}
	if got := svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled"]; got != "true" {
		t.Fatalf("expected cross-zone load balancing annotation to be 'true', got %q", got)
	}
	if got := svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-target-node-labels"]; got != targetNodesLabel {
		t.Fatalf("expected target node labels annotation to be '%s', got %q", targetNodesLabel, got)
	}
}

// Test that LoadBalancerSourceRanges is applied for external router services with allowedCIDRBlocks
func TestReconcileRouterService_AppliesLoadBalancerSourceRanges(t *testing.T) {
	// Test case 1: External router service should have LoadBalancerSourceRanges set
	t.Run("external router service sets LoadBalancerSourceRanges", func(t *testing.T) {
		// Given a HostedControlPlane on AWS with allowedCIDRBlocks
		hcp := &hyperv1.HostedControlPlane{}
		hcp.Spec.Platform.Type = hyperv1.AWSPlatform
		hcp.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{
			AllowedCIDRBlocks: []hyperv1.CIDRBlock{"10.0.0.0/8", "192.168.1.0/24"},
		}

		svc := &corev1.Service{}

		// When reconciling an external router service
		if err := ReconcileRouterService(svc, false /* internal */, true /* cross-zone */, hcp); err != nil {
			t.Fatalf("ReconcileRouterService returned error: %v", err)
		}

		// Then LoadBalancerSourceRanges should be set to match AllowedCIDRBlocks
		expectedCIDRs := sets.New(util.AllowedCIDRBlocks(hcp)...)
		actualCIDRs := sets.New(svc.Spec.LoadBalancerSourceRanges...)

		if !expectedCIDRs.Equal(actualCIDRs) {
			t.Fatalf("expected LoadBalancerSourceRanges to be %v, got %v", expectedCIDRs.UnsortedList(), actualCIDRs.UnsortedList())
		}
	})

	// Test case 2: Internal router service should NOT have LoadBalancerSourceRanges set
	t.Run("internal router service does not set LoadBalancerSourceRanges", func(t *testing.T) {
		// Given a HostedControlPlane on AWS with allowedCIDRBlocks
		hcp := &hyperv1.HostedControlPlane{}
		hcp.Spec.Platform.Type = hyperv1.AWSPlatform
		hcp.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{
			AllowedCIDRBlocks: []hyperv1.CIDRBlock{"10.0.0.0/8", "192.168.1.0/24"},
		}

		svc := &corev1.Service{}

		// When reconciling an internal router service
		if err := ReconcileRouterService(svc, true /* internal */, true /* cross-zone */, hcp); err != nil {
			t.Fatalf("ReconcileRouterService returned error: %v", err)
		}

		// Then LoadBalancerSourceRanges should NOT be set (since it's internal)
		if len(svc.Spec.LoadBalancerSourceRanges) > 0 {
			t.Fatalf("expected LoadBalancerSourceRanges to be empty for internal router, got %v", svc.Spec.LoadBalancerSourceRanges)
		}
	})

	// Test case 3: External router service with no allowedCIDRBlocks should not set LoadBalancerSourceRanges
	t.Run("external router service with no allowedCIDRBlocks does not set LoadBalancerSourceRanges", func(t *testing.T) {
		// Given a HostedControlPlane on AWS with no allowedCIDRBlocks
		hcp := &hyperv1.HostedControlPlane{}
		hcp.Spec.Platform.Type = hyperv1.AWSPlatform

		svc := &corev1.Service{}

		// When reconciling an external router service
		if err := ReconcileRouterService(svc, false /* internal */, true /* cross-zone */, hcp); err != nil {
			t.Fatalf("ReconcileRouterService returned error: %v", err)
		}

		// Then LoadBalancerSourceRanges should NOT be set
		if len(svc.Spec.LoadBalancerSourceRanges) > 0 {
			t.Fatalf("expected LoadBalancerSourceRanges to be empty when no allowedCIDRBlocks provided, got %v", svc.Spec.LoadBalancerSourceRanges)
		}
	})
}
