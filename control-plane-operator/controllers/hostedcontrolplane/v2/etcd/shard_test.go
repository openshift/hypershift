package etcd

import (
	"slices"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestEtcdTemplateData(t *testing.T) {
	t.Parallel()

	t.Run("When name is etcd, it should produce correct template data", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		data := etcdTemplateData("etcd")
		g.Expect(data["Name"]).To(Equal("etcd"))
		g.Expect(data["ClientServiceName"]).To(Equal("etcd-client"))
		g.Expect(data["DiscoveryServiceName"]).To(Equal("etcd-discovery"))
	})

	t.Run("When name is a shard, it should produce correct template data", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		data := etcdTemplateData("etcd-events")
		g.Expect(data["Name"]).To(Equal("etcd-events"))
		g.Expect(data["ClientServiceName"]).To(Equal("etcd-client-events"))
		g.Expect(data["DiscoveryServiceName"]).To(Equal("etcd-discovery-events"))
	})
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

		// Simulate template-rendered STS: labels and serviceName already set for shard.
		sts := &appsv1.StatefulSet{
			Spec: appsv1.StatefulSetSpec{
				Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{"app": "etcd-events"}},
				ServiceName: "etcd-discovery-events",
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "etcd-events"}},
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
			Context: t.Context(),
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

	// containerByName returns the named container of a rendered StatefulSet.
	containerByName := func(g Gomega, sts *appsv1.StatefulSet, name string) corev1.Container {
		idx := slices.IndexFunc(sts.Spec.Template.Spec.Containers, func(c corev1.Container) bool { return c.Name == name })
		g.Expect(idx).WithOffset(1).To(BeNumerically(">=", 0), "container %q not found in StatefulSet", name)
		return sts.Spec.Template.Spec.Containers[idx]
	}

	// tlsEnv returns the TLS related environment variables of a container, in order.
	tlsEnv := func(c corev1.Container) []corev1.EnvVar {
		var env []corev1.EnvVar
		for _, e := range c.Env {
			if e.Name == "ETCD_TLS_MIN_VERSION" || e.Name == "ETCD_CIPHER_SUITES" {
				env = append(env, e)
			}
		}
		return env
	}

	// tlsArgs returns the TLS related flags of a container, in order.
	tlsArgs := func(c corev1.Container) []string {
		var args []string
		for _, arg := range c.Args {
			if strings.HasPrefix(arg, "--tls-min-version=") ||
				strings.HasPrefix(arg, "--listen-tls-min-version=") ||
				strings.HasPrefix(arg, "--listen-cipher-suites=") {
				args = append(args, arg)
			}
		}
		return args
	}

	// envWithoutShardSpecificVars returns the etcd container env, in order, without
	// the variables whose values legitimately differ between the default etcd and a
	// shard (member list and advertised URLs) and ETCD_LOG_LEVEL, which shards do not
	// configure. Every other variable, including any TLS setting, must match.
	envWithoutShardSpecificVars := func(c corev1.Container) []corev1.EnvVar {
		return slices.DeleteFunc(slices.Clone(c.Env), func(e corev1.EnvVar) bool {
			switch e.Name {
			case "ETCD_INITIAL_CLUSTER", "ETCD_INITIAL_ADVERTISE_PEER_URLS", "ETCD_ADVERTISE_CLIENT_URLS", "ETCD_LOG_LEVEL":
				return true
			}
			return false
		})
	}

	tlsProfileTestCases := []struct {
		name                  string
		configuration         *hyperv1.ClusterConfiguration
		clusterNetworkCIDR    string
		expectedTLSMinVersion string
		// expectedCipherSuites is empty when no cipher suites should be configured.
		expectedCipherSuites string
		expectedError        string
	}{
		{
			name:                  "When API server config is nil, it should configure TLS 1.2 and the Intermediate cipher suites on every container like the default etcd",
			configuration:         nil,
			expectedTLSMinVersion: "TLS1.2",
			expectedCipherSuites:  strings.Join(intermediateEtcdCipherSuites(), ","),
		},
		{
			name:                  "When TLS profile is Modern, it should configure TLS 1.3 without cipher suites on every container like the default etcd",
			configuration:         tlsProfileConfiguration(&configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType}),
			expectedTLSMinVersion: "TLS1.3",
		},
		{
			name: "When TLS profile is Custom with supported ciphers, it should configure the min TLS version and only the supported ciphers on every container like the default etcd",
			configuration: tlsProfileConfiguration(customTLSProfile(configv1.VersionTLS12,
				"ECDHE-RSA-AES128-GCM-SHA256", "UNSUPPORTED-CIPHER", "ECDHE-ECDSA-AES256-GCM-SHA384")),
			expectedTLSMinVersion: "TLS1.2",
			expectedCipherSuites:  "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
		},
		{
			name: "When TLS profile is Custom with TLS 1.3 and only TLS 1.3 ciphers, it should configure TLS 1.3 without cipher suites on every container like the default etcd",
			configuration: tlsProfileConfiguration(customTLSProfile(configv1.VersionTLS13,
				"TLS_AES_128_GCM_SHA256", "TLS_AES_256_GCM_SHA384")),
			expectedTLSMinVersion: "TLS1.3",
		},
		{
			name:                  "When the cluster network is IPv6 and TLS profile is Modern, it should configure TLS 1.3 without cipher suites on every container like the default etcd",
			configuration:         tlsProfileConfiguration(&configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType}),
			clusterNetworkCIDR:    "fd01::/48",
			expectedTLSMinVersion: "TLS1.3",
		},
		{
			name:          "When TLS profile is Custom with a nil Custom field, it should return an error",
			configuration: tlsProfileConfiguration(&configv1.TLSSecurityProfile{Type: configv1.TLSProfileCustomType}),
			expectedError: "failed to get min TLS version",
		},
	}

	for _, tc := range tlsProfileTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			clusterNetworkCIDR := tc.clusterNetworkCIDR
			if clusterNetworkCIDR == "" {
				clusterNetworkCIDR = "10.128.0.0/14"
			}
			cpContext := component.WorkloadContext{
				Context: t.Context(),
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Namespace: "hcp-namespace"},
					Spec: hyperv1.HostedControlPlaneSpec{
						Configuration: tc.configuration,
						Networking: hyperv1.ClusterNetworking{
							ClusterNetwork: []hyperv1.ClusterNetworkEntry{
								{CIDR: mustParseCIDR(clusterNetworkCIDR)},
							},
						},
						ControllerAvailabilityPolicy: hyperv1.HighlyAvailable,
						Etcd: hyperv1.EtcdSpec{
							ManagementType: hyperv1.Managed,
							Managed: &hyperv1.ManagedEtcdSpec{
								Storage: hyperv1.ManagedEtcdStorageSpec{Type: hyperv1.PersistentVolumeEtcdStorage},
							},
						},
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

			// Shards render the same etcd asset as the default etcd StatefulSet.
			shardSTS, err := assets.LoadStatefulSetManifestTemplated(ComponentName, etcdTemplateData("etcd-events"))
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptStatefulSetForShard(cpContext, shardSTS, shard)
			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			expectedEnv := []corev1.EnvVar{{Name: "ETCD_TLS_MIN_VERSION", Value: tc.expectedTLSMinVersion}}
			expectedMetricsArgs := []string{"--tls-min-version=" + tc.expectedTLSMinVersion}
			expectedHealthzArgs := []string{"--listen-tls-min-version=" + tc.expectedTLSMinVersion}
			if tc.expectedCipherSuites != "" {
				expectedEnv = append(expectedEnv, corev1.EnvVar{Name: "ETCD_CIPHER_SUITES", Value: tc.expectedCipherSuites})
				expectedMetricsArgs = append(expectedMetricsArgs, "--listen-cipher-suites="+tc.expectedCipherSuites)
				expectedHealthzArgs = append(expectedHealthzArgs, "--listen-cipher-suites="+tc.expectedCipherSuites)
			}

			shardEtcd := containerByName(g, shardSTS, ComponentName)
			shardMetrics := containerByName(g, shardSTS, "etcd-metrics")
			shardHealthz := containerByName(g, shardSTS, "healthz")

			// Actual TLS values on each shard container.
			g.Expect(tlsEnv(shardEtcd)).To(Equal(expectedEnv))
			g.Expect(tlsArgs(shardMetrics)).To(Equal(expectedMetricsArgs))
			g.Expect(tlsArgs(shardHealthz)).To(Equal(expectedHealthzArgs))

			// Parity with the default etcd StatefulSet rendered for the same hosted control
			// plane. The comparisons are ordered and cover the full args and env, so any TLS
			// setting added to only one of the renderers is caught.
			defaultSTS, err := assets.LoadStatefulSetManifestTemplated(ComponentName, etcdTemplateData(ComponentName))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(adaptStatefulSet(cpContext, defaultSTS)).To(Succeed())

			g.Expect(envWithoutShardSpecificVars(shardEtcd)).To(Equal(envWithoutShardSpecificVars(containerByName(g, defaultSTS, ComponentName))))
			g.Expect(shardMetrics.Args).To(Equal(containerByName(g, defaultSTS, "etcd-metrics").Args))
			g.Expect(shardHealthz.Args).To(Equal(containerByName(g, defaultSTS, "healthz").Args))
		})
	}
}

func TestAdaptStatefulSetForShard_IPv6WithDefrag(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	// Simulate template-rendered STS for shard.
	sts := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{"app": "etcd-events"}},
			ServiceName: "etcd-discovery-events",
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "etcd-events"}},
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
		Context: t.Context(),
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

// mustParseCIDR parses a CIDR string for test use.
func mustParseCIDR(cidr string) ipnet.IPNet {
	return *ipnet.MustParseCIDR(cidr)
}
