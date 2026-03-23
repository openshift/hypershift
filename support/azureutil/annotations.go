package azureutil

const (
	// InternalLoadBalancerAnnotation is the Azure annotation key for internal load balancers.
	InternalLoadBalancerAnnotation = "service.beta.kubernetes.io/azure-load-balancer-internal"

	// InternalLoadBalancerValue is the value that enables internal load balancing.
	InternalLoadBalancerValue = "true"
)
