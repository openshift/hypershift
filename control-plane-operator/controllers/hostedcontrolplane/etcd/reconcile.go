package etcd

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/util"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func etcdContainer() *corev1.Container {
	return &corev1.Container{
		Name: "etcd",
	}
}

func etcdInitContainer() *corev1.Container {
	return &corev1.Container{
		Name: "etcd-init",
	}
}

func ensureDNSContainer() *corev1.Container {
	return &corev1.Container{
		Name: "ensure-dns",
	}
}

func resetMemberContainer() *corev1.Container {
	return &corev1.Container{
		Name: "reset-member",
	}
}

func etcdMetricsContainer() *corev1.Container {
	return &corev1.Container{
		Name: "etcd-metrics",
	}
}

func etcdHealthzContainer() *corev1.Container {
	return &corev1.Container{
		Name: "healthz",
	}
}

func etcdDefragControllerContainer() *corev1.Container {
	return &corev1.Container{
		Name: "etcd-defrag",
	}
}

//go:embed etcd-init.sh
var etcdInitScript string

func ReconcileStatefulSet(ss *appsv1.StatefulSet, p *EtcdParams) error {
	p.OwnerRef.ApplyTo(ss)

	ss.Spec.ServiceName = manifests.EtcdDiscoveryService(ss.Namespace).Name
	ss.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: etcdPodSelector(),
	}
	ss.Spec.Replicas = ptr.To(int32(p.DeploymentConfig.Replicas))
	ss.Spec.PodManagementPolicy = appsv1.ParallelPodManagement
	ss.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "data",
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: p.StorageSpec.PersistentVolume.StorageClassName,
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: *p.StorageSpec.PersistentVolume.Size,
					},
				},
			},
		},
	}
	ss.Spec.Template.Labels = etcdPodSelector()

	ss.Spec.Template.Spec.Containers = []corev1.Container{
		util.BuildContainer(etcdContainer(), buildEtcdContainer(p, ss.Namespace)),
		util.BuildContainer(etcdMetricsContainer(), buildEtcdMetricsContainer(p, ss.Namespace)),
		util.BuildContainer(etcdHealthzContainer(), buildEtcdHealthzContainer(p, ss.Namespace)),
	}

	// Only deploy etcd-defrag-controller container in HA mode.
	// When we perform defragmentation it takes the etcd instance offline for a short amount of time.
	// Therefore we only want to do this when there are multiple etcd instances.
	if p.DeploymentConfig.Replicas > 1 {
		ss.Spec.Template.Spec.Containers = append(ss.Spec.Template.Spec.Containers,
			util.BuildContainer(etcdDefragControllerContainer(), buildEtcdDefragControllerContainer(p, ss.Namespace)))

		ss.Spec.Template.Spec.ServiceAccountName = manifests.EtcdDefragControllerServiceAccount("").Name

		if p.DeploymentConfig.AdditionalLabels == nil {
			p.DeploymentConfig.AdditionalLabels = make(map[string]string)
		}
		p.DeploymentConfig.AdditionalLabels[config.NeedManagementKASAccessLabel] = "true"
	}

	ss.Spec.Template.Spec.InitContainers = []corev1.Container{
		util.BuildContainer(ensureDNSContainer(), buildEnsureDNSContainer(p, ss.Namespace)),
		util.BuildContainer(resetMemberContainer(), buildResetMemberContainer(p, ss.Namespace)),
	}

	if len(p.StorageSpec.RestoreSnapshotURL) > 0 && !p.SnapshotRestored {
		ss.Spec.Template.Spec.InitContainers = append(ss.Spec.Template.Spec.InitContainers,
			util.BuildContainer(etcdInitContainer(), buildEtcdInitContainer(p)))
	}

	ss.Spec.Template.Spec.Volumes = []corev1.Volume{
		{
			Name: "peer-tls",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: manifests.EtcdPeerSecret(ss.Namespace).Name,
				},
			},
		},
		{
			Name: "server-tls",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: manifests.EtcdServerSecret(ss.Namespace).Name,
				},
			},
		},
		{
			Name: "client-tls",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: manifests.EtcdClientSecret(ss.Namespace).Name,
				},
			},
		},
		{
			Name: "etcd-ca",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: manifests.EtcdSignerCAConfigMap(ss.Namespace).Name,
					},
				},
			},
		},
		{
			Name: "etcd-metrics-ca",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: manifests.EtcdMetricsSignerCAConfigMap(ss.Namespace).Name,
					},
				},
			},
		},
		{
			Name: "cluster-state",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}

	p.DeploymentConfig.ApplyToStatefulSet(ss)

	return nil
}

func buildEtcdInitContainer(p *EtcdParams) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Env = []corev1.EnvVar{
			{
				Name:  "RESTORE_URL_ETCD",
				Value: p.StorageSpec.RestoreSnapshotURL[0], // RestoreSnapshotURL can only hve 1 entry
			},
		}
		c.Image = p.EtcdImage
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = []string{"/bin/sh", "-ce", etcdInitScript}
		c.VolumeMounts = []corev1.VolumeMount{
			{
				Name:      "data",
				MountPath: "/var/lib",
			},
		}
	}
}

func buildEnsureDNSContainer(p *EtcdParams, ns string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Env = []corev1.EnvVar{
			{
				Name:  "NAMESPACE",
				Value: ns,
			},
		}
		c.Image = p.CPOImage
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = []string{"/bin/bash"}
		c.Args = []string{"-c", "exec control-plane-operator resolve-dns ${HOSTNAME}.etcd-discovery.${NAMESPACE}.svc"}
	}
}

const resetMemberScript = `
#!/bin/bash

set -eu

# This script checks whether the data directory of this etcd member is empty.
# If it is, and there is a functional etcd cluster, then it ensures that a member
# corresponding to this pod does not exist in the cluster so it can be added
# as a new member.

# Setup the etcdctl environment
export ETCDCTL_API=3
export ETCDCTL_CACERT=/etc/etcd/tls/etcd-ca/ca.crt
export ETCDCTL_CERT=/etc/etcd/tls/server/server.crt
export ETCDCTL_KEY=/etc/etcd/tls/server/server.key
export ETCDCTL_ENDPOINTS=https://etcd-client:2379

if [[ -f /etc/etcd/clusterstate/existing ]]; then
  rm /etc/etcd/clusterstate/existing
fi

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
	  etcdctl member add ${HOSTNAME} --peer-urls https://${HOSTNAME}.etcd-discovery.${NAMESPACE}.svc:2380
	  echo "existing" > /etc/etcd/clusterstate/existing
	else
	  echo "A member does not exist with name (${HOSTNAME}), nothing to do"
	fi
  else
    echo "Cannot list members in cluster, so likely not up yet"
  fi
else
  echo "Snapshot db exists, member has data"
fi
`

func buildResetMemberContainer(p *EtcdParams, ns string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Name = "reset-member"
		c.Image = p.EtcdImage
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = []string{"/bin/bash"}
		c.Args = []string{"-c", resetMemberScript}
		c.VolumeMounts = []corev1.VolumeMount{
			{
				Name:      "data",
				MountPath: "/var/lib",
			},
			{
				Name:      "server-tls",
				MountPath: "/etc/etcd/tls/server",
			},
			{
				Name:      "etcd-ca",
				MountPath: "/etc/etcd/tls/etcd-ca",
			},
			{
				Name:      "cluster-state",
				MountPath: "/etc/etcd/clusterstate",
			},
		}
		c.Env = []corev1.EnvVar{
			{
				Name: "NAMESPACE",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.namespace",
					},
				},
			},
		}
	}
}

func buildEtcdContainer(p *EtcdParams, namespace string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		var podIP, allInterfaces string

		scriptTemplate := `
CLUSTER_STATE="new"
if [[ -f /etc/etcd/clusterstate/existing ]]; then
  CLUSTER_STATE="existing"
fi

/usr/bin/etcd \
--data-dir=/var/lib/data \
--name=${HOSTNAME} \
--initial-advertise-peer-urls=https://${HOSTNAME}.etcd-discovery.${NAMESPACE}.svc:2380 \
--listen-peer-urls=https://%s:2380 \
--listen-client-urls=https://%s:2379,https://localhost:2379 \
--advertise-client-urls=https://${HOSTNAME}.etcd-discovery.${NAMESPACE}.svc:2379 \
--listen-metrics-urls=https://%s:2382 \
--initial-cluster-token=etcd-cluster \
--initial-cluster=${INITIAL_CLUSTER} \
--initial-cluster-state=${CLUSTER_STATE} \
--quota-backend-bytes=${QUOTA_BACKEND_BYTES} \
--snapshot-count=10000 \
--peer-client-cert-auth=true \
--peer-cert-file=/etc/etcd/tls/peer/peer.crt \
--peer-key-file=/etc/etcd/tls/peer/peer.key \
--peer-trusted-ca-file=/etc/etcd/tls/etcd-ca/ca.crt \
--client-cert-auth=true \
--cert-file=/etc/etcd/tls/server/server.crt \
--key-file=/etc/etcd/tls/server/server.key \
--trusted-ca-file=/etc/etcd/tls/etcd-ca/ca.crt
`

		if p.IPv6 {
			podIP = "[${POD_IP}]"
			allInterfaces = "[::]"

		} else {
			podIP = "${POD_IP}"
			allInterfaces = "0.0.0.0"
		}

		script := fmt.Sprintf(scriptTemplate, podIP, podIP, allInterfaces)

		var members []string
		for i := 0; i < p.DeploymentConfig.Replicas; i++ {
			name := fmt.Sprintf("etcd-%d", i)
			members = append(members, fmt.Sprintf("%s=https://%s.etcd-discovery.%s.svc:2380", name, name, namespace))
		}
		initialCluster := strings.Join(members, ",")

		c.Image = p.EtcdImage
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = []string{"/bin/sh", "-c", script}
		c.VolumeMounts = []corev1.VolumeMount{
			{
				Name:      "data",
				MountPath: "/var/lib",
			},
			{
				Name:      "peer-tls",
				MountPath: "/etc/etcd/tls/peer",
			},
			{
				Name:      "server-tls",
				MountPath: "/etc/etcd/tls/server",
			},
			{
				Name:      "client-tls",
				MountPath: "/etc/etcd/tls/client",
			},
			{
				Name:      "etcd-ca",
				MountPath: "/etc/etcd/tls/etcd-ca",
			},
			{
				Name:      "cluster-state",
				MountPath: "/etc/etcd/clusterstate",
			},
		}
		c.Env = []corev1.EnvVar{
			{
				Name: "POD_IP",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "status.podIP",
					},
				},
			},
			{
				Name: "NAMESPACE",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.namespace",
					},
				},
			},
			{
				Name:  "INITIAL_CLUSTER",
				Value: initialCluster,
			},
			{
				Name:  "QUOTA_BACKEND_BYTES",
				Value: strconv.FormatInt(EtcdSTSQuotaBackendSize, 10),
			},
		}
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "client",
				ContainerPort: 2379,
				Protocol:      corev1.ProtocolTCP,
			},
			{
				Name:          "peer",
				ContainerPort: 2380,
				Protocol:      corev1.ProtocolTCP,
			},
		}
		c.LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Port:   intstr.FromInt(9980),
					Path:   "healthz",
					Scheme: corev1.URISchemeHTTPS,
				},
			},
			TimeoutSeconds:   30,
			FailureThreshold: 5,
			PeriodSeconds:    5,
			SuccessThreshold: 1,
		}
		c.ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Port:   intstr.FromInt(9980),
					Path:   "readyz",
					Scheme: corev1.URISchemeHTTPS,
				},
			},
			TimeoutSeconds:   10,
			FailureThreshold: 15,
			PeriodSeconds:    5,
			SuccessThreshold: 1,
		}
		c.StartupProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Port:   intstr.FromInt(9980),
					Path:   "readyz",
					Scheme: corev1.URISchemeHTTPS,
				},
			},
			TimeoutSeconds:   10,
			PeriodSeconds:    10,
			SuccessThreshold: 1,
			FailureThreshold: 18,
		}
	}
}

func buildEtcdHealthzContainer(p *EtcdParams, namespace string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = p.EtcdOperatorImage
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = []string{"cluster-etcd-operator"}
		c.Args = []string{"readyz",
			"--target=https://localhost:2379",
			"--listen-port=9980",
			"--serving-cert-file=/etc/etcd/tls/server/server.crt",
			"--serving-key-file=/etc/etcd/tls/server/server.key",
			"--client-cert-file=/etc/etcd/tls/client/etcd-client.crt",
			"--client-key-file=/etc/etcd/tls/client/etcd-client.key",
			"--client-cacert-file=/etc/etcd/tls/etcd-ca/ca.crt",
		}
		c.VolumeMounts = []corev1.VolumeMount{
			{
				Name:      "server-tls",
				MountPath: "/etc/etcd/tls/server",
			},
			{
				Name:      "client-tls",
				MountPath: "/etc/etcd/tls/client",
			},
			{
				Name:      "etcd-ca",
				MountPath: "/etc/etcd/tls/etcd-ca",
			},
		}
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "healthz",
				ContainerPort: 9980,
				Protocol:      corev1.ProtocolTCP,
			},
		}
		c.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("50Mi"),
			},
		}
	}
}

func buildEtcdDefragControllerContainer(p *EtcdParams, namespace string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = p.CPOImage
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
	}
}

func buildEtcdMetricsContainer(p *EtcdParams, namespace string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		var loInterface, allInterfaces string

		scriptTemplate := `
etcd grpc-proxy start \
--endpoints https://localhost:2382 \
--metrics-addr https://%s:2381 \
--listen-addr %s:2383 \
--advertise-client-url ""  \
--key /etc/etcd/tls/peer/peer.key \
--key-file /etc/etcd/tls/server/server.key \
--cert /etc/etcd/tls/peer/peer.crt \
--cert-file /etc/etcd/tls/server/server.crt \
--cacert /etc/etcd/tls/etcd-ca/ca.crt \
--trusted-ca-file /etc/etcd/tls/etcd-metrics-ca/ca.crt
`

		if p.IPv6 {
			loInterface = "[::1]"
			allInterfaces = "[::]"
		} else {
			loInterface = "127.0.0.1"
			allInterfaces = "0.0.0.0"
		}

		script := fmt.Sprintf(scriptTemplate, allInterfaces, loInterface)

		c.Image = p.EtcdImage
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = []string{"/bin/sh", "-c", script}
		c.VolumeMounts = []corev1.VolumeMount{
			{
				Name:      "peer-tls",
				MountPath: "/etc/etcd/tls/peer",
			},
			{
				Name:      "server-tls",
				MountPath: "/etc/etcd/tls/server",
			},
			{
				Name:      "etcd-ca",
				MountPath: "/etc/etcd/tls/etcd-ca", // our own peer client cert
			},
			{
				Name:      "etcd-metrics-ca",
				MountPath: "/etc/etcd/tls/etcd-metrics-ca", // incoming client certs
			},
		}
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "metrics",
				ContainerPort: 2381,
				Protocol:      corev1.ProtocolTCP,
			},
		}
		c.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("40m"),
				corev1.ResourceMemory: resource.MustParse("200Mi"),
			},
		}
	}
}

func ReconcileDiscoveryService(service *corev1.Service, ownerRef config.OwnerRef) error {
	if service.CreationTimestamp.IsZero() {
		service.Spec.Type = corev1.ServiceTypeClusterIP
		service.Spec.ClusterIP = corev1.ClusterIPNone
	}
	ownerRef.ApplyTo(service)

	service.Spec.PublishNotReadyAddresses = true
	service.Spec.Selector = etcdPodSelector()
	service.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "peer",
			Protocol:   corev1.ProtocolTCP,
			Port:       2380,
			TargetPort: intstr.Parse("peer"),
		},
		{
			Name:       "etcd-client",
			Protocol:   corev1.ProtocolTCP,
			Port:       2379,
			TargetPort: intstr.Parse("client"),
		},
	}
	return nil
}

func ReconcileClientService(service *corev1.Service, ownerRef config.OwnerRef) error {
	if service.CreationTimestamp.IsZero() {
		service.Spec.Type = corev1.ServiceTypeClusterIP
		service.Spec.ClusterIP = corev1.ClusterIPNone
	}
	ownerRef.ApplyTo(service)
	service.Labels = etcdPodSelector()
	service.Spec.Selector = etcdPodSelector()
	service.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "etcd-client",
			Protocol:   corev1.ProtocolTCP,
			Port:       2379,
			TargetPort: intstr.Parse("client"),
		},
		{
			Name:       "metrics",
			Protocol:   corev1.ProtocolTCP,
			Port:       2381,
			TargetPort: intstr.Parse("metrics"),
		},
	}
	return nil
}

// ReconcileServiceMonitor
func ReconcileServiceMonitor(sm *prometheusoperatorv1.ServiceMonitor, ownerRef config.OwnerRef, clusterID string, metricsSet metrics.MetricsSet) error {
	ownerRef.ApplyTo(sm)

	sm.Spec.Selector.MatchLabels = etcdPodSelector()
	sm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{sm.Namespace},
	}
	sm.Spec.Endpoints = []prometheusoperatorv1.Endpoint{
		{
			Port:   "metrics",
			Scheme: "https",
			TLSConfig: &prometheusoperatorv1.TLSConfig{
				SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
					ServerName: ptr.To("etcd-client"),
					Cert: prometheusoperatorv1.SecretOrConfigMap{
						Secret: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: manifests.EtcdMetricsClientSecret(sm.Namespace).Name,
							},
							Key: pki.EtcdClientCrtKey,
						},
					},
					KeySecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: manifests.EtcdMetricsClientSecret(sm.Namespace).Name,
						},
						Key: pki.EtcdClientKeyKey,
					},
					CA: prometheusoperatorv1.SecretOrConfigMap{
						ConfigMap: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: manifests.EtcdSignerCAConfigMap(sm.Namespace).Name,
							},
							Key: certs.CASignerCertMapKey,
						},
					},
				},
			},
			MetricRelabelConfigs: metrics.EtcdRelabelConfigs(metricsSet),
		},
	}

	util.ApplyClusterIDLabel(&sm.Spec.Endpoints[0], clusterID)

	return nil
}

func ReconcilePodDisruptionBudget(pdb *policyv1.PodDisruptionBudget, p *EtcdParams) error {
	if pdb.CreationTimestamp.IsZero() {
		pdb.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: etcdPodSelector(),
		}
	}

	p.OwnerRef.ApplyTo(pdb)
	util.ReconcilePodDisruptionBudget(pdb, p.Availability)
	return nil
}

func ReconcileDefragControllerRole(role *rbacv1.Role, p *EtcdParams) error {
	p.OwnerRef.ApplyTo(role)

	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"coordination.k8s.io"},
			Resources: []string{
				"leases",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs: []string{
				"create",
				"patch",
				"update",
			},
		},
	}
	return nil
}

func ReconcileDefragControllerRoleBinding(roleBinding *rbacv1.RoleBinding, p *EtcdParams) error {
	p.OwnerRef.ApplyTo(roleBinding)

	roleBinding.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "Role",
		Name:     manifests.EtcdDefragControllerRole("").Name,
	}
	roleBinding.Subjects = []rbacv1.Subject{
		{
			Kind: "ServiceAccount",
			Name: manifests.EtcdDefragControllerServiceAccount("").Name,
		},
	}
	return nil
}
