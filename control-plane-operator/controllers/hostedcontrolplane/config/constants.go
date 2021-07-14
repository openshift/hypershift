package config

const (
	DefaultPriorityClass         = "system-node-critical"
	DefaultServiceAccountIssuer  = "https://kubernetes.default.svc"
	DefaultImageRegistryHostname = "image-registry.openshift-image-registry.svc:5000"
	DefaultAdvertiseAddress      = "172.20.0.1"
	DefaultEtcdURL               = "https://etcd-client:2379"
	DefaultAPIServerPort         = 6443
	DefaultEtcdClusterVersion    = "3.4.9"
	DefaultServiceNodePortRange  = "30000-32767"
)
