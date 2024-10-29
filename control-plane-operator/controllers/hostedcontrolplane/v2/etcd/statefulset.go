package etcd

import (
	_ "embed"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
)

func adaptStatefulSet(cpContext component.ControlPlaneContext, sts *appsv1.StatefulSet) error {
	hcp := cpContext.HCP
	managedEtcdSpec := hcp.Spec.Etcd.Managed

	ipv4, err := util.IsIPv4(hcp.Spec.Networking.ClusterNetwork[0].CIDR.String())
	if err != nil {
		return fmt.Errorf("error checking the ClusterNetworkCIDR: %v", err)
	}

	util.UpdateContainer(ComponentName, sts.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		replicas := config.DefaultReplicas(hcp, false)
		var members []string
		for i := 0; i < replicas; i++ {
			name := fmt.Sprintf("etcd-%d", i)
			members = append(members, fmt.Sprintf("%s=https://%s.etcd-discovery.%s.svc:2380", name, name, hcp.Namespace))
		}
		c.Env = append(c.Env,
			corev1.EnvVar{
				Name:  "ETCD_INITIAL_CLUSTER",
				Value: strings.Join(members, ","),
			},
		)

		if !ipv4 {
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

	util.UpdateContainer("etcd-metrics", sts.Spec.Template.Spec.Containers, func(c *corev1.Container) {
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
		)
	})

	if defragControllerPredicate(cpContext) {
		sts.Spec.Template.Spec.Containers = append(sts.Spec.Template.Spec.Containers, buildEtcdDefragControllerContainer(hcp.Namespace))
		sts.Spec.Template.Spec.ServiceAccountName = manifests.EtcdDefragControllerServiceAccount("").Name
	}

	snapshotRestored := meta.IsStatusConditionTrue(hcp.Status.Conditions, string(hyperv1.EtcdSnapshotRestored))
	if managedEtcdSpec != nil && len(managedEtcdSpec.Storage.RestoreSnapshotURL) > 0 && !snapshotRestored {
		sts.Spec.Template.Spec.InitContainers = append(sts.Spec.Template.Spec.InitContainers,
			buildEtcdInitContainer(managedEtcdSpec.Storage.RestoreSnapshotURL[0]), // RestoreSnapshotURL can only have 1 entry
		)
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

func buildEtcdInitContainer(restoreUrl string) corev1.Container {
	c := corev1.Container{
		Name: "etcd-init",
	}
	c.Env = []corev1.EnvVar{
		{
			Name:  "RESTORE_URL_ETCD",
			Value: restoreUrl,
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
