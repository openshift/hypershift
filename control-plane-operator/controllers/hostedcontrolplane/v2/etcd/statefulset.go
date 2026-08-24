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

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"

	"go.etcd.io/etcd/client/pkg/v3/tlsutil"
)

// minTLSVersion assesses what is the minimum TLS version we should use. This
// function takes into account that etcd supports only 1.2 and 1.3.
func minTLSVersion(profile *configv1.TLSSecurityProfile) tlsutil.TLSVersion {
	switch config.MinTLSVersion(profile) {
	case string(configv1.VersionTLS13):
		return tlsutil.TLSVersion13
	default:
		return tlsutil.TLSVersion12
	}
}

func adaptStatefulSet(cpContext component.WorkloadContext, sts *appsv1.StatefulSet) error {
	hcp := cpContext.HCP
	managedEtcdSpec := hcp.Spec.Etcd.Managed
	profile := cpContext.HCP.Spec.Configuration.GetTLSSecurityProfile()

	ipv4, err := netutil.IsIPv4CIDR(hcp.Spec.Networking.ClusterNetwork[0].CIDR.String())
	if err != nil {
		return fmt.Errorf("error checking the ClusterNetworkCIDR: %w", err)
	}

	// assess what is the min tls version to be used and also the list of
	// cipher suites. if the cipher list is empty then the go default's
	// cipher will be used.
	tlsMinVersion := minTLSVersion(profile)
	cipherSuites := config.SupportedEtcdCipherSuites(cpContext, config.CipherSuites(profile))

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

		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "ETCD_TLS_MIN_VERSION",
			Value: string(tlsMinVersion),
		})

		if len(cipherSuites) > 0 {
			podspec.UpsertEnvVar(c, corev1.EnvVar{
				Name:  "ETCD_CIPHER_SUITES",
				Value: strings.Join(cipherSuites, ","),
			})
		}

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
			fmt.Sprintf("--tls-min-version=%s", tlsMinVersion),
		)

		if len(cipherSuites) > 0 {
			c.Args = append(c.Args, fmt.Sprintf("--listen-cipher-suites=%s", strings.Join(cipherSuites, ",")))
		}
	})

	podspec.UpdateContainer("healthz", sts.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args, fmt.Sprintf("--listen-tls-min-version=%s", tlsMinVersion))
		if len(cipherSuites) > 0 {
			c.Args = append(c.Args, fmt.Sprintf("--listen-cipher-suites=%s", strings.Join(cipherSuites, ",")))
		}
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
