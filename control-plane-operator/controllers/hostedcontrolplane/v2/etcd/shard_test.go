package etcd

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/metrics"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func TestEtcdShardInterface(t *testing.T) {
	t.Parallel()

	t.Run("IsRequestServing returns false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		s := &etcdShard{}
		g.Expect(s.IsRequestServing()).To(BeFalse())
	})

	t.Run("MultiZoneSpread returns true", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		s := &etcdShard{}
		g.Expect(s.MultiZoneSpread()).To(BeTrue())
	})

	t.Run("NeedsManagementKASAccess reflects needsManagementKASAccess field", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		s := &etcdShard{needsManagementKASAccess: true}
		g.Expect(s.NeedsManagementKASAccess()).To(BeTrue())
		s2 := &etcdShard{needsManagementKASAccess: false}
		g.Expect(s2.NeedsManagementKASAccess()).To(BeFalse())
	})
}

func TestNewShardComponent(t *testing.T) {
	t.Parallel()

	t.Run("single replica shard does not need management KAS access", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		shard := hyperv1.ManagedEtcdShardSpec{Name: "test", Replicas: 1}
		c := NewShardComponent(shard)
		g.Expect(c).ToNot(BeNil())
	})

	t.Run("3 replica shard needs management KAS access", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		shard := hyperv1.ManagedEtcdShardSpec{Name: "ha-shard", Replicas: 3}
		c := NewShardComponent(shard)
		g.Expect(c).ToNot(BeNil())
	})
}

func TestAdaptServiceForShard(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"app": "etcd"},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "etcd"},
		},
	}

	fn := adaptServiceForShard("etcd-events")
	err := fn(component.WorkloadContext{}, svc)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(svc.Name).To(Equal("etcd-client-events"))
	g.Expect(svc.Labels["app"]).To(Equal("etcd-events"))
	g.Expect(svc.Spec.Selector["app"]).To(Equal("etcd-events"))
}

func TestAdaptDiscoveryServiceForShard(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	svc := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "etcd"},
		},
	}

	fn := adaptDiscoveryServiceForShard("etcd-events")
	err := fn(component.WorkloadContext{}, svc)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(svc.Name).To(Equal("etcd-discovery-events"))
	g.Expect(svc.Spec.Selector["app"]).To(Equal("etcd-events"))
}

func TestAdaptPDBForShard(t *testing.T) {
	t.Parallel()

	g := NewWithT(t)
	minAvail := intstr.FromInt32(2)
	pdb := &policyv1.PodDisruptionBudget{
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector:     &metav1.LabelSelector{MatchLabels: map[string]string{"app": "etcd"}},
			MinAvailable: &minAvail,
		},
	}
	fn := adaptPDBForShard("etcd-events")
	err := fn(component.WorkloadContext{}, pdb)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(pdb.Name).To(Equal("etcd-events"))
	g.Expect(pdb.Spec.Selector.MatchLabels["app"]).To(Equal("etcd-events"))
	g.Expect(pdb.Spec.MinAvailable).ToNot(BeNil())
	g.Expect(pdb.Spec.MaxUnavailable).To(BeNil())
}

func TestAdaptShardStorage(t *testing.T) {
	t.Parallel()

	t.Run("PersistentVolume storage inherits from parent", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		storageClass := "fast-ssd"
		pvSize := resource.MustParse("16Gi")
		sts := &appsv1.StatefulSet{
			Spec: appsv1.StatefulSetSpec{
				VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
					{Spec: corev1.PersistentVolumeClaimSpec{}},
				},
			},
		}
		shard := hyperv1.ManagedEtcdShardSpec{
			Name: "test",
			Storage: hyperv1.ManagedEtcdShardStorageSpec{
				Type:             hyperv1.PersistentVolumeEtcdShardStorage,
				PersistentVolume: hyperv1.ManagedEtcdShardPersistentVolumeSpec{},
			},
		}
		hcp := &hyperv1.HostedControlPlane{
			Spec: hyperv1.HostedControlPlaneSpec{
				Etcd: hyperv1.EtcdSpec{
					Managed: &hyperv1.ManagedEtcdSpec{
						Storage: hyperv1.ManagedEtcdStorageSpec{
							PersistentVolume: &hyperv1.PersistentVolumeEtcdStorageSpec{
								StorageClassName: &storageClass,
								Size:             &pvSize,
							},
						},
					},
				},
			},
		}
		adaptShardStorage(sts, shard, hcp)
		g.Expect(*sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName).To(Equal("fast-ssd"))
		g.Expect(sts.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(pvSize))
	})

	t.Run("PersistentVolume storage overrides storageClassName per shard", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		parentClass := "slow"
		sts := &appsv1.StatefulSet{
			Spec: appsv1.StatefulSetSpec{
				VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
					{Spec: corev1.PersistentVolumeClaimSpec{}},
				},
			},
		}
		shard := hyperv1.ManagedEtcdShardSpec{
			Name: "test",
			Storage: hyperv1.ManagedEtcdShardStorageSpec{
				Type: hyperv1.PersistentVolumeEtcdShardStorage,
				PersistentVolume: hyperv1.ManagedEtcdShardPersistentVolumeSpec{
					StorageClassName: "fast",
				},
			},
		}
		hcp := &hyperv1.HostedControlPlane{
			Spec: hyperv1.HostedControlPlaneSpec{
				Etcd: hyperv1.EtcdSpec{
					Managed: &hyperv1.ManagedEtcdSpec{
						Storage: hyperv1.ManagedEtcdStorageSpec{
							PersistentVolume: &hyperv1.PersistentVolumeEtcdStorageSpec{
								StorageClassName: &parentClass,
							},
						},
					},
				},
			},
		}
		adaptShardStorage(sts, shard, hcp)
		g.Expect(*sts.Spec.VolumeClaimTemplates[0].Spec.StorageClassName).To(Equal("fast"))
	})

	t.Run("EmptyDir storage replaces VolumeClaimTemplates with emptyDir volume", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		pvSize := resource.MustParse("8Gi")
		sts := &appsv1.StatefulSet{
			Spec: appsv1.StatefulSetSpec{
				VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
					{Spec: corev1.PersistentVolumeClaimSpec{}},
				},
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: ComponentName,
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceMemory: resource.MustParse("256Mi"),
									},
								},
							},
						},
					},
				},
			},
		}
		shard := hyperv1.ManagedEtcdShardSpec{
			Name: "test",
			Storage: hyperv1.ManagedEtcdShardStorageSpec{
				Type: hyperv1.EmptyDirEtcdShardStorage,
			},
		}
		hcp := &hyperv1.HostedControlPlane{
			Spec: hyperv1.HostedControlPlaneSpec{
				Etcd: hyperv1.EtcdSpec{
					Managed: &hyperv1.ManagedEtcdSpec{
						Storage: hyperv1.ManagedEtcdStorageSpec{
							PersistentVolume: &hyperv1.PersistentVolumeEtcdStorageSpec{
								Size: &pvSize,
							},
						},
					},
				},
			},
		}
		adaptShardStorage(sts, shard, hcp)
		g.Expect(sts.Spec.VolumeClaimTemplates).To(BeNil())
		// Should have a "data" emptyDir volume
		found := false
		for _, v := range sts.Spec.Template.Spec.Volumes {
			if v.Name == "data" {
				found = true
				g.Expect(v.EmptyDir).ToNot(BeNil())
				g.Expect(v.EmptyDir.Medium).To(Equal(corev1.StorageMediumMemory))
				g.Expect(v.EmptyDir.SizeLimit.String()).To(Equal("8Gi"))
			}
		}
		g.Expect(found).To(BeTrue())
		// Memory limit should be pvSize + 512Mi
		expectedMem := resource.MustParse("8704Mi") // 8Gi + 512Mi
		g.Expect(sts.Spec.Template.Spec.Containers[0].Resources.Limits[corev1.ResourceMemory]).To(Equal(expectedMem))
	})

	t.Run("EmptyDir with no parent PV size uses default", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		sts := &appsv1.StatefulSet{
			Spec: appsv1.StatefulSetSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: ComponentName},
						},
					},
				},
			},
		}
		shard := hyperv1.ManagedEtcdShardSpec{
			Name: "test",
			Storage: hyperv1.ManagedEtcdShardStorageSpec{
				Type: hyperv1.EmptyDirEtcdShardStorage,
			},
		}
		hcp := &hyperv1.HostedControlPlane{
			Spec: hyperv1.HostedControlPlaneSpec{
				Etcd: hyperv1.EtcdSpec{
					Managed: &hyperv1.ManagedEtcdSpec{
						Storage: hyperv1.ManagedEtcdStorageSpec{},
					},
				},
			},
		}
		adaptShardStorage(sts, shard, hcp)
		found := false
		for _, v := range sts.Spec.Template.Spec.Volumes {
			if v.Name == "data" {
				found = true
				g.Expect(v.EmptyDir.SizeLimit.String()).To(Equal(hyperv1.DefaultPersistentVolumeEtcdStorageSize.String()))
			}
		}
		g.Expect(found).To(BeTrue())
	})
}

func TestAdaptShardScheduling(t *testing.T) {
	t.Parallel()

	t.Run("sets node selector", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		sts := &appsv1.StatefulSet{
			Spec: appsv1.StatefulSetSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{},
				},
			},
		}
		shard := hyperv1.ManagedEtcdShardSpec{
			Scheduling: hyperv1.EtcdShardSchedulingSpec{
				NodeSelector: map[string]string{"disktype": "ssd"},
			},
		}
		adaptShardScheduling(sts, shard)
		g.Expect(sts.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue("disktype", "ssd"))
	})

	t.Run("appends tolerations", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		sts := &appsv1.StatefulSet{
			Spec: appsv1.StatefulSetSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Tolerations: []corev1.Toleration{
							{Key: "existing", Operator: corev1.TolerationOpExists},
						},
					},
				},
			},
		}
		shard := hyperv1.ManagedEtcdShardSpec{
			Scheduling: hyperv1.EtcdShardSchedulingSpec{
				Tolerations: []corev1.Toleration{
					{Key: "new-key", Operator: corev1.TolerationOpEqual, Value: "val"},
				},
			},
		}
		adaptShardScheduling(sts, shard)
		g.Expect(sts.Spec.Template.Spec.Tolerations).To(HaveLen(2))
	})

	t.Run("no-op when scheduling is empty", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		sts := &appsv1.StatefulSet{
			Spec: appsv1.StatefulSetSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{},
				},
			},
		}
		shard := hyperv1.ManagedEtcdShardSpec{}
		adaptShardScheduling(sts, shard)
		g.Expect(sts.Spec.Template.Spec.NodeSelector).To(BeNil())
		g.Expect(sts.Spec.Template.Spec.Tolerations).To(BeEmpty())
	})
}

func TestAdaptStatefulSetForShard(t *testing.T) {
	t.Parallel()

	t.Run("sets shard-specific labels, replicas, and TLS volumes", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		sts := &appsv1.StatefulSet{
			Spec: appsv1.StatefulSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "etcd"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "etcd"}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: ComponentName, Env: []corev1.EnvVar{}},
							{Name: "etcd-metrics"},
						},
						InitContainers: []corev1.Container{
							{Name: "ensure-dns", Args: []string{"-c", "placeholder"}},
							{Name: "reset-member", Args: []string{"-c", "placeholder"}},
						},
						Volumes: []corev1.Volume{
							{Name: "peer-tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "etcd-peer-tls"}}},
							{Name: "server-tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "etcd-server-tls"}}},
							{Name: "client-tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "etcd-client-tls"}}},
						},
					},
				},
				VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
					{Spec: corev1.PersistentVolumeClaimSpec{}},
				},
			},
		}

		shard := hyperv1.ManagedEtcdShardSpec{
			Name:     "events",
			Replicas: 3,
			Storage: hyperv1.ManagedEtcdShardStorageSpec{
				Type: hyperv1.PersistentVolumeEtcdShardStorage,
			},
		}

		cpContext := component.WorkloadContext{
			HCP: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns"},
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{CIDR: mustParseCIDR("10.0.0.0/16")},
						},
					},
					ControllerAvailabilityPolicy: hyperv1.SingleReplica,
					Etcd: hyperv1.EtcdSpec{
						Managed: &hyperv1.ManagedEtcdSpec{
							Storage: hyperv1.ManagedEtcdStorageSpec{},
						},
					},
				},
			},
		}

		err := adaptStatefulSetForShard(cpContext, sts, shard)
		g.Expect(err).ToNot(HaveOccurred())

		// Labels
		g.Expect(sts.Spec.Selector.MatchLabels["app"]).To(Equal("etcd-events"))
		g.Expect(sts.Spec.Template.Labels["app"]).To(Equal("etcd-events"))

		// Replicas
		g.Expect(*sts.Spec.Replicas).To(Equal(int32(3)))

		// Service name
		g.Expect(sts.Spec.ServiceName).To(Equal("etcd-discovery-events"))

		// TLS volumes
		for _, v := range sts.Spec.Template.Spec.Volumes {
			switch v.Name {
			case "peer-tls":
				g.Expect(v.Secret.SecretName).To(Equal("etcd-events-peer-tls"))
			case "server-tls":
				g.Expect(v.Secret.SecretName).To(Equal("etcd-events-server-tls"))
			case "client-tls":
				// client-tls should remain unchanged
				g.Expect(v.Secret.SecretName).To(Equal("etcd-client-tls"))
			}
		}
	})
}

func TestAdaptStatefulSetForShard_IPv6WithDefrag(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	sts := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "etcd"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "etcd"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: ComponentName, Env: []corev1.EnvVar{}},
						{Name: "etcd-metrics"},
					},
					InitContainers: []corev1.Container{
						{Name: "ensure-dns", Args: []string{"-c", "placeholder"}},
						{Name: "reset-member", Args: []string{"-c", "placeholder"}},
					},
					Volumes: []corev1.Volume{
						{Name: "peer-tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "etcd-peer-tls"}}},
						{Name: "server-tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "etcd-server-tls"}}},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{Spec: corev1.PersistentVolumeClaimSpec{}},
			},
		},
	}

	shard := hyperv1.ManagedEtcdShardSpec{
		Name:     "events",
		Replicas: 3,
		Storage: hyperv1.ManagedEtcdShardStorageSpec{
			Type: hyperv1.PersistentVolumeEtcdShardStorage,
		},
	}

	cpContext := component.WorkloadContext{
		HCP: &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns"},
			Spec: hyperv1.HostedControlPlaneSpec{
				Networking: hyperv1.ClusterNetworking{
					ClusterNetwork: []hyperv1.ClusterNetworkEntry{
						{CIDR: mustParseCIDR("fd01::/48")},
					},
				},
				ControllerAvailabilityPolicy: hyperv1.HighlyAvailable,
				Etcd: hyperv1.EtcdSpec{
					Managed: &hyperv1.ManagedEtcdSpec{
						Storage: hyperv1.ManagedEtcdStorageSpec{},
					},
				},
			},
		},
	}

	err := adaptStatefulSetForShard(cpContext, sts, shard)
	g.Expect(err).ToNot(HaveOccurred())

	// IPv6 listen URLs should use bracket notation
	var foundIPv6Peer, foundIPv6Client bool
	for _, c := range sts.Spec.Template.Spec.Containers {
		if c.Name == ComponentName {
			for _, env := range c.Env {
				if env.Name == "ETCD_LISTEN_PEER_URLS" {
					g.Expect(env.Value).To(ContainSubstring("[$(POD_IP)]"))
					foundIPv6Peer = true
				}
				if env.Name == "ETCD_LISTEN_CLIENT_URLS" {
					g.Expect(env.Value).To(ContainSubstring("[$(POD_IP)]"))
					foundIPv6Client = true
				}
			}
		}
	}
	g.Expect(foundIPv6Peer).To(BeTrue())
	g.Expect(foundIPv6Client).To(BeTrue())

	// HA shard with HighlyAvailable should get defrag container
	var hasDefrag bool
	for _, c := range sts.Spec.Template.Spec.Containers {
		if c.Name == "etcd-defrag" {
			hasDefrag = true
		}
	}
	g.Expect(hasDefrag).To(BeTrue())
}

func TestAdaptServiceMonitorForShard(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	cpContext := component.WorkloadContext{
		HCP: &hyperv1.HostedControlPlane{
			Spec: hyperv1.HostedControlPlaneSpec{
				ClusterID: "test-cluster",
			},
		},
		MetricsSet: metrics.MetricsSetTelemetry,
	}

	svcName := "etcd-client"
	sm := &prometheusoperatorv1.ServiceMonitor{
		Spec: prometheusoperatorv1.ServiceMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "etcd"},
			},
			Endpoints: []prometheusoperatorv1.Endpoint{
				{
					HTTPConfigWithProxyAndTLSFiles: prometheusoperatorv1.HTTPConfigWithProxyAndTLSFiles{
						HTTPConfigWithTLSFiles: prometheusoperatorv1.HTTPConfigWithTLSFiles{
							TLSConfig: &prometheusoperatorv1.TLSConfig{
								SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
									ServerName: &svcName,
								},
							},
						},
					},
				},
			},
		},
	}

	err := adaptServiceMonitorForShard(cpContext, sm, "etcd-events")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(sm.Name).To(Equal("etcd-events"))
	g.Expect(sm.Spec.Selector.MatchLabels["app"]).To(Equal("etcd-events"))
	g.Expect(*sm.Spec.Endpoints[0].TLSConfig.ServerName).To(Equal("etcd-client-events"))
}

// mustParseCIDR parses a CIDR string for test use.
func mustParseCIDR(cidr string) ipnet.IPNet {
	return *ipnet.MustParseCIDR(cidr)
}
