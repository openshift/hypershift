package etcd

import (
	_ "embed"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:embed etcd-init.sh
var etcdInitScript string

// ReconcileStatefulSet reconciles an etcd StatefulSet for a specific shard
// This function modifies the StatefulSet in place and should be used with createOrUpdate pattern
func ReconcileStatefulSet(
	sts *appsv1.StatefulSet,
	hcp *hyperv1.HostedControlPlane,
	shard hyperv1.ManagedEtcdShardSpec,
	params *ShardParams,
) error {
	managedEtcdSpec := hcp.Spec.Etcd.Managed

	// Determine replica count for this shard
	replicas := int32(1)
	if params.AvailabilityPolicy == hyperv1.HighlyAvailable {
		replicas = 3
	}
	if shard.Replicas != nil {
		replicas = *shard.Replicas
	}
	sts.Spec.Replicas = &replicas

	// Update StatefulSet service name to point to the shard's discovery service
	// Note: StatefulSet name and namespace are set by the manifest constructor
	// Must match the service name from EtcdDiscoveryServiceForShard
	if shard.Name == "default" {
		sts.Spec.ServiceName = "etcd-discovery"
	} else {
		sts.Spec.ServiceName = fmt.Sprintf("etcd-discovery-%s", shard.Name)
	}

	// Add shard label to pod template
	if sts.Spec.Template.Labels == nil {
		sts.Spec.Template.Labels = make(map[string]string)
	}
	sts.Spec.Template.Labels["hypershift.openshift.io/etcd-shard"] = shard.Name
	sts.Spec.Template.Labels["hypershift.openshift.io/etcd-priority"] = string(shard.Priority)

	// Update selector to include shard label
	if sts.Spec.Selector == nil {
		sts.Spec.Selector = &metav1.LabelSelector{}
	}
	if sts.Spec.Selector.MatchLabels == nil {
		sts.Spec.Selector.MatchLabels = make(map[string]string)
	}
	sts.Spec.Selector.MatchLabels["hypershift.openshift.io/etcd-shard"] = shard.Name

	// Update init container images and commands to use shard-specific discovery service
	util.UpdateContainer("ensure-dns", sts.Spec.Template.Spec.InitContainers, func(c *corev1.Container) {
		c.Image = params.ControlPlaneOperatorImage
		// Update command to use shard-specific discovery service
		discoveryService := sts.Spec.ServiceName  // This is already set to shard-specific discovery service
		c.Args = []string{
			"-c",
			fmt.Sprintf("exec control-plane-operator resolve-dns ${HOSTNAME}.%s.${NAMESPACE}.svc", discoveryService),
		}
	})
	util.UpdateContainer("reset-member", sts.Spec.Template.Spec.InitContainers, func(c *corev1.Container) {
		c.Image = params.EtcdImage

		// Determine shard-specific service names
		var discoveryService, clientService string
		if shard.Name == "default" {
			discoveryService = "etcd-discovery"
			clientService = "etcd-client"
		} else {
			discoveryService = fmt.Sprintf("etcd-discovery-%s", shard.Name)
			clientService = fmt.Sprintf("etcd-client-%s", shard.Name)
		}

		// Update the script to use shard-specific service names
		// The script is in Args[1] - we need to replace hardcoded service names
		if len(c.Args) >= 2 {
			script := c.Args[1]
			// Replace ETCDCTL_ENDPOINTS
			script = strings.ReplaceAll(script, "export ETCDCTL_ENDPOINTS=https://etcd-client:2379",
				fmt.Sprintf("export ETCDCTL_ENDPOINTS=https://%s:2379", clientService))
			// Replace member add peer-urls
			script = strings.ReplaceAll(script, "https://${HOSTNAME}.etcd-discovery.${NAMESPACE}.svc:2380",
				fmt.Sprintf("https://${HOSTNAME}.%s.${NAMESPACE}.svc:2380", discoveryService))

			c.Args = []string{"-c", script}
		}
	})

	// Update main container images
	util.UpdateContainer("etcd", sts.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Image = params.EtcdImage
	})
	util.UpdateContainer("etcd-metrics", sts.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Image = params.EtcdImage
	})
	util.UpdateContainer("healthz", sts.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Image = params.ClusterEtcdOperatorImage
	})

	// Update etcd container configuration
	util.UpdateContainer("etcd", sts.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		var members []string
		var podPrefix, discoveryService string

		// For backward compatibility with default shard
		// IMPORTANT: Service names must match resourceNameForShard() pattern in manifests/etcd.go
		if shard.Name == "default" {
			podPrefix = "etcd"
			discoveryService = "etcd-discovery"
		} else {
			podPrefix = fmt.Sprintf("etcd-%s", shard.Name)
			discoveryService = fmt.Sprintf("etcd-discovery-%s", shard.Name)  // Fixed: was etcd-%s-discovery
		}

		for i := range replicas {
			name := fmt.Sprintf("%s-%d", podPrefix, i)
			members = append(members, fmt.Sprintf("%s=https://%s.%s.%s.svc:2380", name, name, discoveryService, params.Namespace))
		}
		util.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "ETCD_INITIAL_CLUSTER",
			Value: strings.Join(members, ","),
		})

		// Override hardcoded values from YAML asset with shard-specific service names
		util.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "ETCD_INITIAL_ADVERTISE_PEER_URLS",
			Value: fmt.Sprintf("https://$(HOSTNAME).%s.$(NAMESPACE).svc:2380", discoveryService),
		})
		util.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "ETCD_ADVERTISE_CLIENT_URLS",
			Value: fmt.Sprintf("https://$(HOSTNAME).%s.$(NAMESPACE).svc:2379", discoveryService),
		})

		if !params.IPv4 {
			util.UpsertEnvVar(c, corev1.EnvVar{
				Name:  "ETCD_LISTEN_PEER_URLS",
				Value: "https://[$(POD_IP)]:2380",
			})
			util.UpsertEnvVar(c, corev1.EnvVar{
				Name:  "ETCD_LISTEN_CLIENT_URLS",
				Value: "https://[$(POD_IP)]:2379,https://localhost:2379",
			})
			util.UpsertEnvVar(c, corev1.EnvVar{
				Name:  "ETCD_LISTEN_METRICS_URLS",
				Value: "https://[::]:2382",
			})
		}
	})

	// Update etcd-metrics container configuration
	util.UpdateContainer("etcd-metrics", sts.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		var loInterface, allInterfaces string
		if params.IPv4 {
			loInterface = "127.0.0.1"
			allInterfaces = "0.0.0.0"
		} else {
			loInterface = "[::1]"
			allInterfaces = "[::]"
		}
		// REPLACE args completely to avoid duplicates on reconciliation
		c.Args = []string{
			"grpc-proxy",
			"start",
			"--endpoints=https://localhost:2382",
			"--advertise-client-url=",
			"--key=/etc/etcd/tls/peer/peer.key",
			"--key-file=/etc/etcd/tls/server/server.key",
			"--cert=/etc/etcd/tls/peer/peer.crt",
			"--cert-file=/etc/etcd/tls/server/server.crt",
			"--cacert=/etc/etcd/tls/etcd-ca/ca.crt",
			"--trusted-ca-file=/etc/etcd/tls/etcd-metrics-ca/ca.crt",
			fmt.Sprintf("--listen-addr=%s:2383", loInterface),
			fmt.Sprintf("--metrics-addr=https://%s:2381", allInterfaces),
		}
	})

	// Add defrag controller container if needed (only if not already present)
	if params.NeedsDefragController {
		if !hasContainer(sts.Spec.Template.Spec.Containers, "etcd-defrag") {
			sts.Spec.Template.Spec.Containers = append(sts.Spec.Template.Spec.Containers, buildEtcdDefragControllerContainer(params.Namespace, params.ControlPlaneOperatorImage))
		}
		sts.Spec.Template.Spec.ServiceAccountName = manifests.EtcdDefragControllerServiceAccount("").Name
	}

	// Add snapshot restore init container if needed (only if not already present)
	if len(params.RestoreSnapshotURL) > 0 && !params.SnapshotRestored {
		if !hasContainer(sts.Spec.Template.Spec.InitContainers, "etcd-init") {
			sts.Spec.Template.Spec.InitContainers = append(sts.Spec.Template.Spec.InitContainers,
				buildEtcdInitContainer(params.RestoreSnapshotURL[0], params.EtcdImage), // RestoreSnapshotURL can only have 1 entry
			)
		}
	}

	// Adapt PersistentVolume using shard-specific storage or default
	storage := managedEtcdSpec.Storage
	if shard.Storage != nil {
		storage = *shard.Storage
	}

	if storage.Type == hyperv1.PersistentVolumeEtcdStorage {
		if pv := storage.PersistentVolume; pv != nil {
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

// buildEtcdInitContainer creates the init container for etcd snapshot restore
func buildEtcdInitContainer(restoreURL, etcdImage string) corev1.Container {
	c := corev1.Container{
		Name: "etcd-init",
	}
	c.Env = []corev1.EnvVar{
		{
			Name:  "RESTORE_URL_ETCD",
			Value: restoreURL,
		},
	}
	c.Image = etcdImage
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

// buildEtcdDefragControllerContainer creates the etcd defrag controller sidecar container
func buildEtcdDefragControllerContainer(namespace, controlPlaneOperatorImage string) corev1.Container {
	c := corev1.Container{
		Name: "etcd-defrag",
	}
	c.Image = controlPlaneOperatorImage
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

// hasContainer checks if a container with the given name exists in a container list
func hasContainer(containers []corev1.Container, name string) bool {
	for _, c := range containers {
		if c.Name == name {
			return true
		}
	}
	return false
}
