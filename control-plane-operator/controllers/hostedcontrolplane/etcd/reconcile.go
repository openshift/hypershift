package etcd

import (
	"strconv"
	"time"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/yaml"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
)

func ReconcileECOConfigMap(cm *corev1.ConfigMap, p *EtcdParams) error {
	p.OwnerRef.ApplyTo(cm)

	ecoConfig := struct {
		ECO OperatorConfig `json:"eco"`
	}{
		ECO: OperatorConfig{
			UnhealthyMemberTTL: 3 * time.Minute,
			Etcd: EtcdConfiguration{
				DataDir:                 "/var/lib/etcd",
				BackendQuota:            p.StorageSpec.PersistentVolume.Size.Value(),
				AutoCompactionMode:      "periodic",
				AutoCompactionRetention: "0",
				PeerTransportSecurity: SecurityConfig{
					CertFile:      "/etc/etcd/tls/peer/peer.crt",
					KeyFile:       "/etc/etcd/tls/peer/peer.key",
					CertAuth:      true,
					TrustedCAFile: "/etc/etcd/tls/peer/peer-ca.crt",
					AutoTLS:       false,
				},
				ClientTransportSecurity: SecurityConfig{
					CertFile:      "/etc/etcd/tls/server/server.crt",
					KeyFile:       "/etc/etcd/tls/server/server.key",
					CertAuth:      true,
					TrustedCAFile: "/etc/etcd/tls/server/server-ca.crt",
					AutoTLS:       false,
				},
			},
			ASG: ASGConfig{
				Provider: "sts",
			},
			// TODO: Add snapshot support
			Snapshot: SnapshotConfig{
				Provider: "noop",
				Interval: p.SnapshotInterval,
				TTL:      p.SnapshotTTL,
			},
		},
	}
	configBytes, err := yaml.Marshal(ecoConfig)
	if err != nil {
		return err
	}
	cm.Data = map[string]string{
		"config.yaml": string(configBytes),
	}
	return nil
}

func ReconcileDiscoveryService(service *corev1.Service, ownerRef config.OwnerRef) error {
	if service.CreationTimestamp.IsZero() {
		service.Spec.Type = corev1.ServiceTypeClusterIP
		service.Spec.ClusterIP = corev1.ClusterIPNone
	}
	ownerRef.ApplyTo(service)

	service.Spec.PublishNotReadyAddresses = true
	service.Spec.Selector = etcdPodSelector
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
		{
			Name:       "http",
			Protocol:   corev1.ProtocolTCP,
			Port:       2378,
			TargetPort: intstr.Parse("http"),
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
	service.Labels = etcdPodSelector
	service.Spec.Selector = etcdPodSelector
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

func ReconcileServiceMonitor(sm *prometheusoperatorv1.ServiceMonitor, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sm)

	sm.Spec.Selector.MatchLabels = etcdPodSelector
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
					InsecureSkipVerify: true,
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

	return nil
}

func etcdContainer() *corev1.Container {
	return &corev1.Container{
		Name: "etcd",
	}
}

func reconcileEtcdContainer(ss *appsv1.StatefulSet, c *corev1.Container, image string, replicas int) {
	c.Image = image
	c.ImagePullPolicy = corev1.PullAlways
	c.Command = []string{"/usr/bin/etcd-cloud-operator", "--log-level", "error", "--config", "/etc/etcd/config/config.yaml"}
	c.VolumeMounts = []corev1.VolumeMount{
		{
			Name:      "data",
			MountPath: "/var/lib",
		},
		{
			Name:      "config",
			MountPath: "/etc/etcd/config",
		},
		{
			Name:      "peer-tls",
			MountPath: "/etc/etcd/tls/peer",
		},
		{
			Name:      "server-tls",
			MountPath: "/etc/etcd/tls/server",
		},
	}
	c.Env = []corev1.EnvVar{
		{
			Name:  "ETCD_API",
			Value: "3",
		},
		{
			Name:  "ETCDCTL_INSECURE_SKIP_TLS_VERIFY",
			Value: "true",
		},
		{
			Name:  "STATEFULSET_SERVICE_NAME",
			Value: manifests.EtcdDiscoveryService(ss.Namespace).Name,
		},
		{
			Name:  "STATEFULSET_NAME",
			Value: ss.Name,
		},
		{
			Name:  "STATEFULSET_DNS_CLUSTER_SUFFIX",
			Value: "cluster.local",
		},
		{
			Name: "POD_IP",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "status.podIP",
				},
			},
		},
		{
			Name: "STATEFULSET_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		},
		// TODO: Find a way to avoid encoding this in env
		{
			Name:  "STATEFULSET_REPLICAS",
			Value: strconv.Itoa(replicas),
		},
	}
	c.Ports = []corev1.ContainerPort{
		{
			Name:          "client",
			ContainerPort: 2379,
			Protocol:      corev1.ProtocolTCP,
		},
		{
			Name:          "http",
			ContainerPort: 2378,
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
	// TODO: Extract probes into DeploymentConfig
	c.LivenessProbe = &corev1.Probe{
		Handler: corev1.Handler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: "/status",
				Port: intstr.Parse("http"),
			},
		},
		InitialDelaySeconds: 1,
		PeriodSeconds:       5,
		FailureThreshold:    6,
	}
	c.ReadinessProbe = &corev1.Probe{
		Handler: corev1.Handler{
			Exec: &corev1.ExecAction{
				Command: []string{"/bin/sh", "-c",
					"/usr/bin/etcdctl --cacert /etc/etcd/tls/server/server-ca.crt --cert /etc/etcd/tls/server/server.crt --key /etc/etcd/tls/server/server.key --endpoints=${HOSTNAME}:2379 endpoint health"},
			},
		},
		// TODO: The client port is mTLS secured, so TCPSocket won't work
		//Handler: corev1.Handler{
		//	TCPSocket: &corev1.TCPSocketAction{
		//		Port: intstr.Parse("client"),
		//	},
		//},
		InitialDelaySeconds: 1,
		PeriodSeconds:       5,
		FailureThreshold:    6,
	}
	c.StartupProbe = &corev1.Probe{
		// TODO: Replace with TCP wrapper
		Handler: corev1.Handler{
			Exec: &corev1.ExecAction{
				Command: []string{"/bin/sh", "-c",
					"/usr/bin/etcdctl --cacert /etc/etcd/tls/server/server-ca.crt --cert /etc/etcd/tls/server/server.crt --key /etc/etcd/tls/server/server.key --endpoints=${HOSTNAME}:2379 endpoint health"},
			},
		},
		FailureThreshold: 60,
		PeriodSeconds:    1,
	}
}

func ReconcileStatefulSet(ss *appsv1.StatefulSet, p *EtcdParams) error {
	p.OwnerRef.ApplyTo(ss)

	ss.Spec.ServiceName = manifests.EtcdDiscoveryService(ss.Namespace).Name
	ss.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: etcdPodSelector,
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
	ss.Spec.Template.Labels = etcdPodSelector

	ss.Spec.Template.Spec.Containers = []corev1.Container{*etcdContainer()}
	reconcileEtcdContainer(ss, &ss.Spec.Template.Spec.Containers[0], p.EtcdOperatorImage, p.DeploymentConfig.Replicas)

	ss.Spec.Template.Spec.Volumes = []corev1.Volume{
		{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: manifests.EtcdConfigMap(ss.Namespace).Name,
					},
				},
			},
		},
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
	}

	p.DeploymentConfig.ApplyToStatefulSet(ss)

	return nil
}
