package etcd

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"
	etcdutil "github.com/openshift/hypershift/support/etcd"
	"github.com/openshift/hypershift/support/netutil"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

// TODO(etcd-sharding): Backup support for shards is not yet implemented.
// The enhancement states PVC-backed shards should be included in backup,
// but the current backup controller (HCPEtcdBackup) does not reference
// shard StatefulSets. Resources routed to shards will NOT be backed up.
// This must be addressed before promoting EtcdSharding beyond TechPreview.

type etcdShard struct {
	shard                    hyperv1.ManagedEtcdShardSpec
	needsManagementKASAccess bool
}

func (e *etcdShard) IsRequestServing() bool {
	return false
}

func (e *etcdShard) MultiZoneSpread() bool {
	return true
}

func (e *etcdShard) NeedsManagementKASAccess() bool {
	return e.needsManagementKASAccess
}

func NewShardComponent(shard hyperv1.ManagedEtcdShardSpec) component.ControlPlaneComponent {
	shardName := fmt.Sprintf("etcd-%s", shard.Name)
	s := &etcdShard{
		shard: shard,
		// Management KAS access is needed for defrag leader election leases.
		// This is conservative: defrag only runs when defragControllerPredicate
		// is also true (HA mode), but that's evaluated at reconcile time.
		needsManagementKASAccess: shard.Replicas >= 3,
	}

	return component.NewStatefulSetComponent(shardName, s).
		WithAssetDir("etcd").
		WithAdaptFunction(func(cpContext component.WorkloadContext, sts *appsv1.StatefulSet) error {
			return adaptStatefulSetForShard(cpContext, sts, shard)
		}).
		WithPredicate(isManagedETCD).
		WithManifestAdapter(
			"service.yaml",
			component.WithAdaptFunction(adaptServiceForShard(shardName)),
		).
		WithManifestAdapter(
			"discovery-service.yaml",
			component.WithAdaptFunction(adaptDiscoveryServiceForShard(shardName)),
		).
		WithManifestAdapter(
			"servicemonitor.yaml",
			component.WithAdaptFunction(func(cpContext component.WorkloadContext, sm *prometheusoperatorv1.ServiceMonitor) error {
				return adaptServiceMonitorForShard(cpContext, sm, shardName)
			}),
		).
		WithManifestAdapter(
			"pdb.yaml",
			component.WithAdaptFunction(adaptPDBForShard(shardName, shard.Replicas)),
		).
		WithManifestAdapter(
			"defrag-role.yaml",
			component.WithPredicate(func(_ component.WorkloadContext) bool { return false }),
		).
		WithManifestAdapter(
			"defrag-rolebinding.yaml",
			component.WithPredicate(func(_ component.WorkloadContext) bool { return false }),
		).
		WithManifestAdapter(
			"defrag-serviceaccount.yaml",
			component.WithPredicate(func(_ component.WorkloadContext) bool { return false }),
		).
		Build()
}

func adaptServiceForShard(shardName string) func(component.WorkloadContext, *corev1.Service) error {
	return func(_ component.WorkloadContext, svc *corev1.Service) error {
		svc.Name = etcdutil.ClientServiceName(shardName)
		svc.Labels["app"] = shardName
		svc.Spec.Selector["app"] = shardName
		return nil
	}
}

func adaptDiscoveryServiceForShard(shardName string) func(component.WorkloadContext, *corev1.Service) error {
	return func(_ component.WorkloadContext, svc *corev1.Service) error {
		svc.Name = etcdutil.DiscoveryServiceName(shardName)
		svc.Spec.Selector["app"] = shardName
		return nil
	}
}

func adaptPDBForShard(shardName string, replicas int32) func(component.WorkloadContext, *policyv1.PodDisruptionBudget) error {
	return func(_ component.WorkloadContext, pdb *policyv1.PodDisruptionBudget) error {
		pdb.Name = shardName
		pdb.Spec.Selector.MatchLabels["app"] = shardName
		// For single-replica shards, allow voluntary eviction so node drains aren't blocked.
		if replicas <= 1 {
			pdb.Spec.MinAvailable = nil
			maxUnavailable := intstr.FromInt32(1)
			pdb.Spec.MaxUnavailable = &maxUnavailable
		}
		return nil
	}
}

func adaptStatefulSetForShard(cpContext component.WorkloadContext, sts *appsv1.StatefulSet, shard hyperv1.ManagedEtcdShardSpec) error {
	hcp := cpContext.HCP
	shardName := fmt.Sprintf("etcd-%s", shard.Name)
	discoveryService := etcdutil.DiscoveryServiceName(shardName)
	clientService := etcdutil.ClientServiceName(shardName)

	sts.Spec.ServiceName = discoveryService

	sts.Spec.Selector.MatchLabels["app"] = shardName
	sts.Spec.Template.Labels["app"] = shardName

	// Use the explicit per-shard replica count from the API.
	replicas := shard.Replicas
	sts.Spec.Replicas = &replicas
	var members []string
	for i := range replicas {
		name := fmt.Sprintf("%s-%d", shardName, i)
		members = append(members, fmt.Sprintf("%s=https://%s.%s.%s.svc:2380", name, name, discoveryService, hcp.Namespace))
	}

	podspec.UpdateContainer(ComponentName, sts.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "ETCD_INITIAL_CLUSTER",
			Value: strings.Join(members, ","),
		})
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "ETCD_INITIAL_ADVERTISE_PEER_URLS",
			Value: fmt.Sprintf("https://$(HOSTNAME).%s.$(NAMESPACE).svc:2380", discoveryService),
		})
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "ETCD_ADVERTISE_CLIENT_URLS",
			Value: fmt.Sprintf("https://$(HOSTNAME).%s.$(NAMESPACE).svc:2379", discoveryService),
		})
	})

	podspec.UpdateContainer("ensure-dns", sts.Spec.Template.Spec.InitContainers, func(c *corev1.Container) {
		c.Args = []string{"-c", fmt.Sprintf("exec control-plane-operator resolve-dns ${HOSTNAME}.%s.${NAMESPACE}.svc", discoveryService)}
	})

	podspec.UpdateContainer("reset-member", sts.Spec.Template.Spec.InitContainers, func(c *corev1.Container) {
		script := fmt.Sprintf(`#!/bin/bash

set -eu

export ETCDCTL_API=3
export ETCDCTL_CACERT=/etc/etcd/tls/etcd-ca/ca.crt
export ETCDCTL_CERT=/etc/etcd/tls/server/server.crt
export ETCDCTL_KEY=/etc/etcd/tls/server/server.key
export ETCDCTL_ENDPOINTS=https://%s:2379

echo "new" > /etc/etcd/clusterstate/state

if [[ ! -f /var/lib/data/member/snap/db ]]; then
  echo "No existing etcd data found"
  echo "Checking if cluster is functional"
  if etcdctl member list; then
    echo "Cluster is functional"
    MEMBER_ID=$(etcdctl member list -w simple | grep "${HOSTNAME}" | awk -F, '{ print $1 }')
    if [[ -n "${MEMBER_ID}" ]]; then
      echo "A member with this name (${HOSTNAME}) already exists, removing"
      etcdctl member remove "${MEMBER_ID}"
      echo "Adding new member"
      etcdctl member add ${HOSTNAME} --peer-urls https://${HOSTNAME}.%s.${NAMESPACE}.svc:2380
      echo "existing" > /etc/etcd/clusterstate/state
    else
      echo "A member does not exist with name (${HOSTNAME}), nothing to do"
    fi
  else
    echo "Cannot list members in cluster, so likely not up yet"
  fi
else
  echo "Snapshot db exists, member has data"
fi
`, clientService, discoveryService)
		c.Args = []string{"-c", script}
	})

	// Update volume secret/configmap names to use shard-specific TLS secrets
	for i, v := range sts.Spec.Template.Spec.Volumes {
		switch v.Name {
		case "peer-tls":
			sts.Spec.Template.Spec.Volumes[i].Secret.SecretName = fmt.Sprintf("%s-peer-tls", shardName)
		case "server-tls":
			sts.Spec.Template.Spec.Volumes[i].Secret.SecretName = fmt.Sprintf("%s-server-tls", shardName)
			// client-tls: shards reuse the default etcd-client-tls secret since all shards
			// share the same CA and KAS uses a single --etcd-certfile/keyfile.
		}
	}

	// Apply IPv6 and metrics adaptations shared with the default etcd.
	ipv4, err := netutil.IsIPv4CIDR(hcp.Spec.Networking.ClusterNetwork[0].CIDR.String())
	if err != nil {
		return fmt.Errorf("error checking the ClusterNetworkCIDR: %w", err)
	}

	podspec.UpdateContainer(ComponentName, sts.Spec.Template.Spec.Containers, func(c *corev1.Container) {
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

	// Configure the etcd-metrics grpc-proxy sidecar.
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
	})

	// Enable defrag for multi-replica shards.
	if replicas >= 3 && defragControllerPredicate(cpContext) {
		defragContainer := buildEtcdDefragControllerContainer(hcp.Namespace)
		defragContainer.Args = append(defragContainer.Args, "--leader-election-id", fmt.Sprintf("etcd-defrag-%s-leader-elect", shard.Name))
		sts.Spec.Template.Spec.Containers = append(sts.Spec.Template.Spec.Containers, defragContainer)
		sts.Spec.Template.Spec.ServiceAccountName = manifests.EtcdDefragControllerServiceAccount("").Name
	}

	adaptShardStorage(sts, shard, hcp)
	adaptShardScheduling(sts, shard)

	return nil
}

func adaptShardStorage(sts *appsv1.StatefulSet, shard hyperv1.ManagedEtcdShardSpec, hcp *hyperv1.HostedControlPlane) {
	managedEtcdSpec := hcp.Spec.Etcd.Managed

	switch shard.Storage.Type {
	case "", hyperv1.PersistentVolumeEtcdShardStorage:
		// Default (unset) inherits PersistentVolume storage from the parent etcd spec.
		if managedEtcdSpec != nil && managedEtcdSpec.Storage.PersistentVolume != nil {
			pv := managedEtcdSpec.Storage.PersistentVolume
			if len(sts.Spec.VolumeClaimTemplates) > 0 {
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
		// Override with shard-specific storageClassName if set.
		if shard.Storage.PersistentVolume.StorageClassName != "" {
			if len(sts.Spec.VolumeClaimTemplates) > 0 {
				sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName = &shard.Storage.PersistentVolume.StorageClassName
			}
		}
		return

	case hyperv1.EmptyDirEtcdShardStorage:
		var sizeLimit resource.Quantity
		if managedEtcdSpec != nil && managedEtcdSpec.Storage.PersistentVolume != nil && managedEtcdSpec.Storage.PersistentVolume.Size != nil {
			sizeLimit = *managedEtcdSpec.Storage.PersistentVolume.Size
		} else {
			sizeLimit = hyperv1.DefaultPersistentVolumeEtcdStorageSize
		}
		sts.Spec.VolumeClaimTemplates = nil
		sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: "data",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium:    corev1.StorageMediumMemory,
					SizeLimit: &sizeLimit,
				},
			},
		})
		// Memory limit must account for both the tmpfs (which counts against the
		// container's cgroup) and etcd's own working-set memory. Without headroom
		// the pod OOMs before the "disk" is full.
		etcdHeadroom := resource.MustParse("512Mi")
		memLimit := sizeLimit.DeepCopy()
		memLimit.Add(etcdHeadroom)
		podspec.UpdateContainer(ComponentName, sts.Spec.Template.Spec.Containers, func(c *corev1.Container) {
			c.Resources.Limits = corev1.ResourceList{
				corev1.ResourceMemory: memLimit,
			}
			// Bump memory request to match the limit so the pod is Guaranteed QoS
			// for the memory dimension — tmpfs usage counts against the cgroup.
			if c.Resources.Requests == nil {
				c.Resources.Requests = corev1.ResourceList{}
			}
			c.Resources.Requests[corev1.ResourceMemory] = memLimit
		})

	}
}

func adaptShardScheduling(sts *appsv1.StatefulSet, shard hyperv1.ManagedEtcdShardSpec) {
	if len(shard.Scheduling.NodeSelector) > 0 {
		if sts.Spec.Template.Spec.NodeSelector == nil {
			sts.Spec.Template.Spec.NodeSelector = map[string]string{}
		}
		for k, v := range shard.Scheduling.NodeSelector {
			sts.Spec.Template.Spec.NodeSelector[k] = v
		}
	}
	if len(shard.Scheduling.Tolerations) > 0 {
		sts.Spec.Template.Spec.Tolerations = append(sts.Spec.Template.Spec.Tolerations, shard.Scheduling.Tolerations...)
	}
}

func adaptServiceMonitorForShard(cpContext component.WorkloadContext, sm *prometheusoperatorv1.ServiceMonitor, shardName string) error {
	if err := adaptServiceMonitor(cpContext, sm); err != nil {
		return err
	}
	sm.Name = shardName
	sm.Spec.Selector.MatchLabels["app"] = shardName
	clientSvc := etcdutil.ClientServiceName(shardName)
	sm.Spec.Endpoints[0].TLSConfig.ServerName = &clientSvc
	return nil
}
