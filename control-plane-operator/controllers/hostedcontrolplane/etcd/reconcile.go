package etcd

import (
	"fmt"
	"strconv"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	//TODO: Switch to k8s.io/api/policy/v1 when all management clusters at 1.21+ OR 4.8_openshift+
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
)

func etcdContainer() *corev1.Container {
	return &corev1.Container{
		Name: "etcd",
	}
}

func ReconcileStatefulSet(ss *appsv1.StatefulSet, p *EtcdParams) error {
	p.OwnerRef.ApplyTo(ss)

	ss.Spec.ServiceName = manifests.EtcdDiscoveryService(ss.Namespace).Name
	ss.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: etcdPodSelector(),
	}
	ss.Spec.Replicas = pointer.Int32Ptr(int32(p.DeploymentConfig.Replicas))
	ss.Spec.PodManagementPolicy = appsv1.ParallelPodManagement
	ss.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "data",
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: p.StorageSpec.PersistentVolume.StorageClassName,
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.ResourceRequirements{
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
	}

	p.DeploymentConfig.ApplyToStatefulSet(ss)

	return nil
}

func buildEtcdContainer(p *EtcdParams, namespace string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		script := `
/usr/bin/etcd \
--data-dir=/var/lib/data \
--name=${HOSTNAME} \
--initial-advertise-peer-urls=https://${HOSTNAME}.etcd-discovery.${NAMESPACE}.svc:2380 \
--listen-peer-urls=https://${POD_IP}:2380 \
--listen-client-urls=https://${POD_IP}:2379,https://localhost:2379 \
--advertise-client-urls=https://${HOSTNAME}.etcd-client.${NAMESPACE}.svc:2379 \
--listen-metrics-urls=https://${POD_IP}:2381,https://localhost:2381 \
--initial-cluster-token=etcd-cluster \
--initial-cluster=${INITIAL_CLUSTER} \
--initial-cluster-state=new \
--quota-backend-bytes=${QUOTA_BACKEND_BYTES} \
--peer-client-cert-auth=true \
--peer-cert-file=/etc/etcd/tls/peer/peer.crt \
--peer-key-file=/etc/etcd/tls/peer/peer.key \
--peer-trusted-ca-file=/etc/etcd/tls/peer/peer-ca.crt \
--client-cert-auth=true \
--cert-file=/etc/etcd/tls/server/server.crt \
--key-file=/etc/etcd/tls/server/server.key \
--trusted-ca-file=/etc/etcd/tls/server/server-ca.crt
`

		var members []string
		for i := 0; i < p.DeploymentConfig.Replicas; i++ {
			name := fmt.Sprintf("etcd-%d", i)
			members = append(members, fmt.Sprintf("%s=https://%s.etcd-discovery.%s.svc:2380", name, name, namespace))
		}
		initialCluster := strings.Join(members, ",")

		c.Image = p.EtcdImage
		c.ImagePullPolicy = corev1.PullAlways
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
				Value: strconv.FormatInt(p.StorageSpec.PersistentVolume.Size.Value(), 10),
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
			{
				Name:          "metrics",
				ContainerPort: 2381,
				Protocol:      corev1.ProtocolTCP,
			},
		}
		c.ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"/bin/sh", "-c",
						"/usr/bin/etcdctl --cacert /etc/etcd/tls/client/etcd-client-ca.crt --cert /etc/etcd/tls/client/etcd-client.crt --key /etc/etcd/tls/client/etcd-client.key --endpoints=localhost:2379 endpoint health"},
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       5,
			FailureThreshold:    6,
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
// TODO: Exposing the client cert to monitoring isn't great, but metrics
// TLS can't yet be independently configured. See: https://github.com/etcd-io/etcd/pull/10504
func ReconcileServiceMonitor(sm *prometheusoperatorv1.ServiceMonitor, ownerRef config.OwnerRef, clusterID string) error {
	ownerRef.ApplyTo(sm)

	sm.Spec.Selector.MatchLabels = etcdPodSelector()
	sm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{sm.Namespace},
	}
	sm.Spec.Endpoints = []prometheusoperatorv1.Endpoint{
		{
			Interval: "15s",
			Port:     "metrics",
			Scheme:   "https",
			TLSConfig: &prometheusoperatorv1.TLSConfig{
				SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
					ServerName: "etcd-client",
					Cert: prometheusoperatorv1.SecretOrConfigMap{
						Secret: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: manifests.EtcdClientSecret(sm.Namespace).Name,
							},
							Key: "etcd-client.crt",
						},
					},
					KeySecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: manifests.EtcdClientSecret(sm.Namespace).Name,
						},
						Key: "etcd-client.key",
					},
					CA: prometheusoperatorv1.SecretOrConfigMap{
						Secret: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: manifests.EtcdClientSecret(sm.Namespace).Name,
							},
							Key: "etcd-client-ca.crt",
						},
					},
				},
			},
		},
	}

	util.ApplyClusterIDLabel(&sm.Spec.Endpoints[0], clusterID)

	return nil
}

func ReconcilePodDisruptionBudget(pdb *policyv1beta1.PodDisruptionBudget, p *EtcdParams) error {
	if pdb.CreationTimestamp.IsZero() {
		pdb.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: etcdPodSelector(),
		}
	}

	p.OwnerRef.ApplyTo(pdb)

	var minAvailable int
	switch p.Availability {
	case hyperv1.SingleReplica:
		minAvailable = 0
	case hyperv1.HighlyAvailable:
		// For HA clusters, only tolerate disruption of a minority of members
		minAvailable = p.DeploymentConfig.Replicas/2 + 1
	}
	pdb.Spec.MinAvailable = &intstr.IntOrString{Type: intstr.Int, IntVal: int32(minAvailable)}

	return nil
}
