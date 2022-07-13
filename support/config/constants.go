package config

const (
	// EtcdPriorityClass is for etcd pods.
	EtcdPriorityClass = "hypershift-etcd"

	// APICriticalPriorityClass is for pods that are required for API calls and
	// resource admission to succeed. This includes pods like kube-apiserver,
	// aggregated API servers, and webhooks.
	APICriticalPriorityClass = "hypershift-api-critical"

	// DefaultPriorityClass is for pods in the Hypershift control plane that are
	// not API critical but still need elevated priority.
	DefaultPriorityClass = "hypershift-control-plane"

	DefaultServiceAccountIssuer  = "https://kubernetes.default.svc"
	DefaultImageRegistryHostname = "image-registry.openshift-image-registry.svc:5000"
	DefaultAdvertiseAddress      = "172.20.0.1"
	DefaultEtcdURL               = "https://etcd-client:2379"
	DefaultAPIServerPort         = 6443
	DefaultServiceNodePortRange  = "30000-32767"
	DefaultSecurityContextUser   = 1001
	RecommendedLeaseDuration     = "137s"
	RecommendedRenewDeadline     = "107s"
	RecommendedRetryPeriod       = "26s"

	DefaultIngressDomainEnvVar = "DEFAULT_INGRESS_DOMAIN"
)
