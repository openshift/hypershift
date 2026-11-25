package etcd

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/netutil"
	"github.com/openshift/hypershift/support/podspec"
	util "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"

	"go.etcd.io/etcd/client/pkg/v3/tlsutil"
)

// minTLSVersion assesses what is the minimum TLS version we should use. This
// function takes into account that etcd supports only 1.2 and 1.3.
func minTLSVersion(profile *configv1.TLSSecurityProfile) (tlsutil.TLSVersion, error) {
	minVer, err := config.MinTLSVersion(profile)
	if err != nil {
		return "", err
	}
	switch minVer {
	case string(configv1.VersionTLS13):
		return tlsutil.TLSVersion13, nil
	default:
		return tlsutil.TLSVersion12, nil
	}
}

// etcdTLSConfig holds the TLS settings derived from the hosted cluster's TLS
// security profile that the etcd member, its etcd-metrics grpc-proxy and its
// healthz sidecar serve with. It is shared by the default etcd StatefulSet and
// the etcd shard StatefulSets so that both render the same TLS configuration.
type etcdTLSConfig struct {
	minVersion tlsutil.TLSVersion
	// cipherSuites holds the IANA names of the profile ciphers supported by
	// etcd. When empty, no cipher suites are configured and the Go defaults
	// are used.
	cipherSuites []string
}

// newEtcdTLSConfig assesses the min TLS version and the list of cipher suites
// to be used from the hosted control plane's TLS security profile.
func newEtcdTLSConfig(cpContext component.WorkloadContext) (etcdTLSConfig, error) {
	profile := cpContext.HCP.Spec.Configuration.GetTLSSecurityProfile()

	minVersion, err := minTLSVersion(profile)
	if err != nil {
		return etcdTLSConfig{}, fmt.Errorf("failed to get min TLS version: %w", err)
	}
	ciphers, err := config.CipherSuites(profile)
	if err != nil {
		return etcdTLSConfig{}, fmt.Errorf("failed to get cipher suites: %w", err)
	}

	return etcdTLSConfig{
		minVersion:   minVersion,
		cipherSuites: config.SupportedEtcdCipherSuites(cpContext, ciphers),
	}, nil
}

// applyToEtcdContainer upserts the TLS environment variables on the etcd
// container: ETCD_TLS_MIN_VERSION always, and ETCD_CIPHER_SUITES only when
// there are cipher suites to configure. Variables that are not present yet are
// appended to the end of the container env. An existing ETCD_CIPHER_SUITES is
// not removed when there are no cipher suites; the etcd asset does not define
// it and the StatefulSet is rendered from the asset on every reconcile.
func (cfg etcdTLSConfig) applyToEtcdContainer(c *corev1.Container) {
	podspec.UpsertEnvVar(c, corev1.EnvVar{
		Name:  "ETCD_TLS_MIN_VERSION",
		Value: string(cfg.minVersion),
	})

	if len(cfg.cipherSuites) > 0 {
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "ETCD_CIPHER_SUITES",
			Value: strings.Join(cfg.cipherSuites, ","),
		})
	}
}

// metricsArgs returns the TLS flags for the etcd-metrics grpc-proxy container.
// Callers append them after the container's other flags.
func (cfg etcdTLSConfig) metricsArgs() []string {
	args := []string{fmt.Sprintf("--tls-min-version=%s", cfg.minVersion)}
	if len(cfg.cipherSuites) > 0 {
		args = append(args, fmt.Sprintf("--listen-cipher-suites=%s", strings.Join(cfg.cipherSuites, ",")))
	}
	return args
}

// healthzArgs returns the TLS flags for the healthz container. Callers append
// them after the container's other flags.
func (cfg etcdTLSConfig) healthzArgs() []string {
	args := []string{fmt.Sprintf("--listen-tls-min-version=%s", cfg.minVersion)}
	if len(cfg.cipherSuites) > 0 {
		args = append(args, fmt.Sprintf("--listen-cipher-suites=%s", strings.Join(cfg.cipherSuites, ",")))
	}
	return args
}

func adaptStatefulSet(cpContext component.WorkloadContext, sts *appsv1.StatefulSet) error {
	hcp := cpContext.HCP
	managedEtcdSpec := hcp.Spec.Etcd.Managed

	ipv4, err := netutil.IsIPv4CIDR(hcp.Spec.Networking.ClusterNetwork[0].CIDR.String())
	if err != nil {
		return fmt.Errorf("error checking the ClusterNetworkCIDR: %w", err)
	}

	tlsConfig, err := newEtcdTLSConfig(cpContext)
	if err != nil {
		return err
	}

	replicas := component.DefaultReplicas(hcp, &etcd{}, ComponentName)
	var members []string
	for i := range replicas {
		name := fmt.Sprintf("etcd-%d", i)
		members = append(members, fmt.Sprintf("%s=https://%s.etcd-discovery.%s.svc:2380", name, name, hcp.Namespace))
	}
	initialCluster := strings.Join(members, ",")

	podspec.UpdateContainer(ComponentName, sts.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Env = append(c.Env,
			corev1.EnvVar{
				Name:  "ETCD_INITIAL_CLUSTER",
				Value: initialCluster,
			},
		)

		tlsConfig.applyToEtcdContainer(c)

		if !ipv4 {
			podspec.UpsertEnvVar(c, corev1.EnvVar{
				Name:  "ETCD_LISTEN_PEER_URLS",
				Value: "https://[$(POD_IP)]:2380",
			})
			podspec.UpsertEnvVar(c, corev1.EnvVar{
				Name:  "ETCD_LISTEN_CLIENT_URLS",
				Value: "https://[$(POD_IP)]:2379,https://localhost:2379",
			})
			podspec.UpsertEnvVar(c, corev1.EnvVar{
				Name:  "ETCD_LISTEN_METRICS_URLS",
				Value: "https://[::]:2382",
			})
		}
		var etcdLogLevel hyperv1.LogLevel
		if hcp.Spec.OperatorConfiguration != nil {
			etcdLogLevel = hcp.Spec.OperatorConfiguration.Etcd.LogLevel
		}
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "ETCD_LOG_LEVEL",
			Value: util.LogLevelToEtcdLevel(etcdLogLevel),
		})
	})

	podspec.UpdateContainer("etcd-metrics", sts.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		var loInterface, allInterfaces string
		if ipv4 {
			loInterface = "127.0.0.1"
			allInterfaces = "0.0.0.0"
		} else {
			loInterface = "[::1]"
			allInterfaces = "[::]"
		}
		c.Args = append(c.Args,
			fmt.Sprintf("--listen-addr=%s:2383", loInterface),
			fmt.Sprintf("--metrics-addr=https://%s:2381", allInterfaces),
			"--advertise-client-url=",
		)
		c.Args = append(c.Args, tlsConfig.metricsArgs()...)
	})

	podspec.UpdateContainer("healthz", sts.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args, tlsConfig.healthzArgs()...)
	})

	// Use etcd SA for self-registration (all topologies) or etcd-defrag-controller SA for defrag (HA-only).
	// The etcd SA is always created and has RBAC for EndpointSlice self-registration.
	// The etcd-defrag-controller SA is only created in HA mode and has RBAC for defragmentation.
	if defragControllerPredicate(cpContext) {
		sts.Spec.Template.Spec.ServiceAccountName = manifests.EtcdDefragControllerServiceAccount("").Name
		sts.Spec.Template.Spec.Containers = append(sts.Spec.Template.Spec.Containers, buildEtcdDefragControllerContainer(hcp.Namespace))
	} else {
		sts.Spec.Template.Spec.ServiceAccountName = manifests.EtcdServiceAccount("").Name
	}

	snapshotRestored := meta.IsStatusConditionTrue(hcp.Status.Conditions, string(hyperv1.EtcdSnapshotRestored))
	if managedEtcdSpec != nil && len(managedEtcdSpec.Storage.RestoreSnapshotURL) > 0 && !snapshotRestored {
		etcdInit := buildEtcdInitContainer(managedEtcdSpec.Storage.RestoreSnapshotURL[0], hcp.Namespace, initialCluster) // RestoreSnapshotURL can only have 1 entry
		insertIdx := len(sts.Spec.Template.Spec.InitContainers)
		for i, c := range sts.Spec.Template.Spec.InitContainers {
			if c.Name == "reset-member" {
				insertIdx = i
				break
			}
		}
		sts.Spec.Template.Spec.InitContainers = slices.Insert(sts.Spec.Template.Spec.InitContainers, insertIdx, etcdInit)
	}

	// adapt PersistentVolume
	if managedEtcdSpec != nil && managedEtcdSpec.Storage.Type == hyperv1.PersistentVolumeEtcdStorage {
		if pv := managedEtcdSpec.Storage.PersistentVolume; pv != nil {
			sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName = pv.StorageClassName
			if pv.Size != nil {
				sts.Spec.VolumeClaimTemplates[0].Spec.Resources = corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: *pv.Size,
					},
				}
			}
		}
	}

	return nil
}

//go:embed etcd-init.sh
var etcdInitScript string

func buildEtcdInitContainer(restoreUrl, namespace, initialCluster string) corev1.Container {
	c := corev1.Container{
		Name: "etcd-init",
	}
	c.Env = []corev1.EnvVar{
		{
			Name: "HOSTNAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
			},
		},
		{
			Name:  "RESTORE_URL_ETCD",
			Value: restoreUrl,
		},
		{
			Name:  "HCP_NAMESPACE",
			Value: namespace,
		},
		{
			Name:  "ETCD_INITIAL_CLUSTER",
			Value: initialCluster,
		},
	}
	c.Image = "etcd"
	c.ImagePullPolicy = corev1.PullIfNotPresent
	c.Command = []string{"/bin/sh", "-ce", etcdInitScript}
	c.VolumeMounts = []corev1.VolumeMount{
		{
			Name:      "data",
			MountPath: "/var/lib",
		},
	}
	return c
}

func buildEtcdDefragControllerContainer(namespace string) corev1.Container {
	c := corev1.Container{
		Name: "etcd-defrag",
	}
	c.Image = "controlplane-operator"
	c.ImagePullPolicy = corev1.PullIfNotPresent
	c.Command = []string{"control-plane-operator"}
	c.Args = []string{
		"etcd-defrag-controller",
		"--namespace",
		namespace,
	}
	c.VolumeMounts = []corev1.VolumeMount{
		{
			Name:      "client-tls",
			MountPath: "/etc/etcd/tls/client",
		},
		{
			Name:      "etcd-ca",
			MountPath: "/etc/etcd/tls/etcd-ca",
		},
	}
	c.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("50Mi"),
		},
	}
	return c
}
