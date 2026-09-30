package azureutil

const (
	// InternalLoadBalancerAnnotation is the Azure annotation key for internal load balancers.
	InternalLoadBalancerAnnotation = "service.beta.kubernetes.io/azure-load-balancer-internal"

	// InternalLoadBalancerValue is the value that enables internal load balancing.
	InternalLoadBalancerValue = "true"

	// PIPNameAnnotation tells the Azure cloud-provider to bind a LoadBalancer
	// Service to a specific pre-existing Azure Public IP resource. This forces
	// the cloud-provider to create a dedicated frontend IP configuration on the
	// LB, avoiding port collisions with other Services sharing the same LB.
	// Supported since cloud-provider-azure v1.24+ (OCP 4.11+).
	PIPNameAnnotation = "service.beta.kubernetes.io/azure-pip-name"
)
