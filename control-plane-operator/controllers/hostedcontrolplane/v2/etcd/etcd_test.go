package etcd

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"go.etcd.io/etcd/client/pkg/v3/tlsutil"
)

func TestBuildEtcdInitContainer(t *testing.T) {
	t.Parallel()

	const (
		testNamespace      = "test-hcp-namespace"
		testInitialCluster = "etcd-0=https://etcd-0.etcd-discovery.test-hcp-namespace.svc:2380,etcd-1=https://etcd-1.etcd-discovery.test-hcp-namespace.svc:2380,etcd-2=https://etcd-2.etcd-discovery.test-hcp-namespace.svc:2380"
	)

	testCases := []struct {
		name       string
		restoreUrl string
		validate   func(g Gomega, c corev1.Container)
	}{
		{
			name:       "When restoreUrl is provided, it should set RESTORE_URL_ETCD env var to that URL",
			restoreUrl: "https://etcd-backup-bucket.s3.us-east-1.amazonaws.com/backups/etcd-snapshot-2024-01-15.db",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Env).To(ContainElement(corev1.EnvVar{
					Name:  "RESTORE_URL_ETCD",
					Value: "https://etcd-backup-bucket.s3.us-east-1.amazonaws.com/backups/etcd-snapshot-2024-01-15.db",
				}))
			},
		},
		{
			name:       "When called, it should set HOSTNAME from pod metadata.name via downward API",
			restoreUrl: "",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Env).To(ContainElement(corev1.EnvVar{
					Name: "HOSTNAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
					},
				}))
			},
		},
		{
			name:       "When called, it should set HCP_NAMESPACE env var",
			restoreUrl: "",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Env).To(ContainElement(corev1.EnvVar{
					Name:  "HCP_NAMESPACE",
					Value: testNamespace,
				}))
			},
		},
		{
			name:       "When called, it should set ETCD_INITIAL_CLUSTER env var",
			restoreUrl: "",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Env).To(ContainElement(corev1.EnvVar{
					Name:  "ETCD_INITIAL_CLUSTER",
					Value: testInitialCluster,
				}))
			},
		},
		{
			name:       "When called, it should set the container name to etcd-init",
			restoreUrl: "",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Name).To(Equal("etcd-init"))
			},
		},
		{
			name:       "When called, it should set image to etcd",
			restoreUrl: "",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Image).To(Equal("etcd"))
			},
		},
		{
			name:       "When called, it should set ImagePullPolicy to PullIfNotPresent",
			restoreUrl: "",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.ImagePullPolicy).To(Equal(corev1.PullIfNotPresent))
			},
		},
		{
			name:       "When called, it should mount /var/lib volume named data",
			restoreUrl: "",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.VolumeMounts).To(ContainElement(corev1.VolumeMount{
					Name:      "data",
					MountPath: "/var/lib",
				}))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			c := buildEtcdInitContainer(tc.restoreUrl, testNamespace, testInitialCluster)
			tc.validate(g, c)
		})
	}
}

func TestBuildEtcdDefragControllerContainer(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		namespace string
		validate  func(g Gomega, c corev1.Container)
	}{
		{
			name:      "When called with a namespace, it should pass the namespace as an arg",
			namespace: "test-namespace",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Args).To(Equal([]string{
					"etcd-defrag-controller",
					"--namespace",
					"test-namespace",
				}))
			},
		},
		{
			name:      "When called, it should set container name to etcd-defrag",
			namespace: "any-namespace",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Name).To(Equal("etcd-defrag"))
			},
		},
		{
			name:      "When called, it should mount client-tls and etcd-ca volumes",
			namespace: "any-namespace",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.VolumeMounts).To(ConsistOf(
					corev1.VolumeMount{
						Name:      "client-tls",
						MountPath: "/etc/etcd/tls/client",
					},
					corev1.VolumeMount{
						Name:      "etcd-ca",
						MountPath: "/etc/etcd/tls/etcd-ca",
					},
				))
			},
		},
		{
			name:      "When called, it should set resource requests for CPU and memory",
			namespace: "any-namespace",
			validate: func(g Gomega, c corev1.Container) {
				g.Expect(c.Resources.Requests).To(HaveKeyWithValue(corev1.ResourceCPU, resource.MustParse("10m")))
				g.Expect(c.Resources.Requests).To(HaveKeyWithValue(corev1.ResourceMemory, resource.MustParse("50Mi")))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			c := buildEtcdDefragControllerContainer(tc.namespace)
			tc.validate(g, c)
		})
	}
}

func TestAdaptStatefulSetEtcdLogLevel(t *testing.T) {
	t.Parallel()

	logLevel := func(l hyperv1.LogLevel) hyperv1.EtcdOperatorSpec {
		return hyperv1.EtcdOperatorSpec{
			ComponentLogLevelSpec: hyperv1.ComponentLogLevelSpec{LogLevel: l},
		}
	}

	findEnvVar := func(envs []corev1.EnvVar, name string) *corev1.EnvVar {
		for i := range envs {
			if envs[i].Name == name {
				return &envs[i]
			}
		}
		return nil
	}

	testCases := []struct {
		name          string
		hcp           *hyperv1.HostedControlPlane
		expectedValue string
	}{
		{
			name: "When LogLevel is nil it should default ETCD_LOG_LEVEL to info",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("10.0.0.0/8")}},
					},
				},
			},
			expectedValue: "info",
		},
		{
			name: "When operatorConfiguration exists but Etcd is zero value it should default ETCD_LOG_LEVEL to info",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{},
					Etcd:                  hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("10.0.0.0/8")}},
					},
				},
			},
			expectedValue: "info",
		},
		{
			name: "When LogLevel is Normal it should set ETCD_LOG_LEVEL to info",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						Etcd: logLevel(hyperv1.Normal),
					},
					Etcd: hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("10.0.0.0/8")}},
					},
				},
			},
			expectedValue: "info",
		},
		{
			name: "When LogLevel is Debug it should set ETCD_LOG_LEVEL to debug",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						Etcd: logLevel(hyperv1.Debug),
					},
					Etcd: hyperv1.EtcdSpec{ManagementType: hyperv1.Managed},
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("10.0.0.0/8")}},
					},
				},
			},
			expectedValue: "debug",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
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
					VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{}},
				},
			}

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				HCP:     tc.hcp,
			}

			err := adaptStatefulSet(cpContext, sts)
			g.Expect(err).ToNot(HaveOccurred())

			var etcdContainer *corev1.Container
			for i := range sts.Spec.Template.Spec.Containers {
				if sts.Spec.Template.Spec.Containers[i].Name == ComponentName {
					etcdContainer = &sts.Spec.Template.Spec.Containers[i]
					break
				}
			}
			g.Expect(etcdContainer).NotTo(BeNil())

			envVar := findEnvVar(etcdContainer.Env, "ETCD_LOG_LEVEL")
			g.Expect(envVar).NotTo(BeNil(), "expected ETCD_LOG_LEVEL env var to be set")
			g.Expect(envVar.Value).To(Equal(tc.expectedValue))
		})
	}
}

func TestIsManagedETCD(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		managementType hyperv1.EtcdManagementType
		expected       bool
	}{
		{
			name:           "When etcd management type is Managed, it should return true",
			managementType: hyperv1.Managed,
			expected:       true,
		},
		{
			name:           "When etcd management type is Unmanaged, it should return false",
			managementType: hyperv1.Unmanaged,
			expected:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cpContext := component.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Etcd: hyperv1.EtcdSpec{
							ManagementType: tc.managementType,
						},
					},
				},
			}

			result, err := isManagedETCD(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestDefragControllerPredicate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name               string
		availabilityPolicy hyperv1.AvailabilityPolicy
		expected           bool
	}{
		{
			name:               "When availability policy is HighlyAvailable, it should return true",
			availabilityPolicy: hyperv1.HighlyAvailable,
			expected:           true,
		},
		{
			name:               "When availability policy is SingleReplica, it should return false",
			availabilityPolicy: hyperv1.SingleReplica,
			expected:           false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cpContext := component.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						ControllerAvailabilityPolicy: tc.availabilityPolicy,
					},
				},
			}

			result := defragControllerPredicate(cpContext)
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestMinTLSVersion(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name              string
		profile           *configv1.TLSSecurityProfile
		expected          tlsutil.TLSVersion
		expectError       bool
		expectedErrSubstr string
	}{
		{
			name:     "When TLS profile is nil, it should return TLS 1.2",
			profile:  nil,
			expected: tlsutil.TLSVersion12,
		},
		{
			name: "When TLS profile is Modern, it should return TLS 1.3",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
			expected: tlsutil.TLSVersion13,
		},
		{
			name: "When TLS profile is Intermediate, it should return TLS 1.2",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			},
			expected: tlsutil.TLSVersion12,
		},
		{
			name: "When TLS profile is Old, it should return TLS 1.2",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
			},
			expected: tlsutil.TLSVersion12,
		},
		{
			name: "When TLS profile is Custom with TLS 1.3, it should return TLS 1.3",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS13,
					},
				},
			},
			expected: tlsutil.TLSVersion13,
		},
		{
			name: "When TLS profile is Custom with TLS 1.2, it should return TLS 1.2",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
					},
				},
			},
			expected: tlsutil.TLSVersion12,
		},
		{
			name: "When TLS profile is Custom with unknown TLS version, it should return TLS 1.2",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: "UnknownVersion",
					},
				},
			},
			expected: tlsutil.TLSVersion12,
		},
		{
			name: "When TLS profile is Custom with nil Custom field, it should return error",
			profile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
			},
			expectError:       true,
			expectedErrSubstr: "Custom but Custom field is nil",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			result, err := minTLSVersion(tc.profile)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrSubstr))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestAdaptStatefulSet(t *testing.T) {
	t.Parallel()

	testStatefulSet := &appsv1.StatefulSet{
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: ComponentName},
						{Name: "etcd-metrics"},
						{Name: "healthz"},
					},
				},
			},
		},
	}

	// valueForEnv returns the value of a given environment variable,
	// returns an empty string if it is not or if it is set to empty.
	valueForEnv := func(container corev1.Container, name string) string {
		for _, env := range container.Env {
			if env.Name == name {
				return env.Value
			}
		}
		return ""
	}

	// valueForFlag returns the value for a given command line flag.
	// Returns empty if the flag isn't found or if it does not have a
	// value.
	valueForFlag := func(container corev1.Container, name string) string {
		for _, arg := range container.Args {
			if strings.HasPrefix(arg, name) {
				return strings.TrimPrefix(arg, name)
			}
		}
		return ""
	}

	testCases := []struct {
		name                  string
		configuration         *hyperv1.ClusterConfiguration
		expectedTLSMinVersion string
		expectCipherSuites    bool
		expectedCipherSuites  string
	}{
		{
			name:                  "When API server config is nil, it should set TLS 1.2 and cipher suites",
			configuration:         nil,
			expectedTLSMinVersion: "TLS1.2",
			expectCipherSuites:    true,
		},
		{
			name: "When TLS profile is modern, it should set min TLS version without ciphers",
			configuration: &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileModernType},
				},
			},
			expectedTLSMinVersion: "TLS1.3",
			expectCipherSuites:    false,
		},
		{
			name: "When TLS profile is intermediate, it should set both min TLS version and ciphers",
			configuration: &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{Type: configv1.TLSProfileIntermediateType},
				},
			},
			expectedTLSMinVersion: "TLS1.2",
			expectCipherSuites:    true,
		},
		{
			name: "When TLS profile has custom cipher suites, it should set min TLS version and convert ciphers from OpenSSL to IANA",
			configuration: &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileCustomType,
						Custom: &configv1.CustomTLSProfile{
							TLSProfileSpec: configv1.TLSProfileSpec{
								MinTLSVersion: configv1.VersionTLS12,
								Ciphers: []string{
									"ECDHE-RSA-AES128-GCM-SHA256",
									"ECDHE-ECDSA-AES128-GCM-SHA256",
								},
							},
						},
					},
				},
			},
			expectedTLSMinVersion: "TLS1.2",
			expectCipherSuites:    true,
			expectedCipherSuites:  "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
		},
		{
			name: "When TLS profile has unsupported cipher suites, it should set only min TLS version",
			configuration: &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileCustomType,
						Custom: &configv1.CustomTLSProfile{
							TLSProfileSpec: configv1.TLSProfileSpec{
								MinTLSVersion: configv1.VersionTLS12,
								Ciphers: []string{
									"UNSUPPORTED-CIPHER-1",
									"UNSUPPORTED-CIPHER-2",
								},
							},
						},
					},
				},
			},
			expectedTLSMinVersion: "TLS1.2",
			expectCipherSuites:    false,
		},
		{
			name: "When TLS 1.3 is specified with TLS 1.3 cipher suites, it should set min TLS version without cipher suites",
			configuration: &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					TLSSecurityProfile: &configv1.TLSSecurityProfile{
						Type: configv1.TLSProfileCustomType,
						Custom: &configv1.CustomTLSProfile{
							TLSProfileSpec: configv1.TLSProfileSpec{
								MinTLSVersion: configv1.VersionTLS13,
								Ciphers: []string{
									"TLS_AES_128_GCM_SHA256",
									"TLS_AES_256_GCM_SHA384",
									"TLS_CHACHA20_POLY1305_SHA256",
								},
							},
						},
					},
				},
			},
			expectedTLSMinVersion: "TLS1.3",
			expectCipherSuites:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Configuration: tc.configuration,
						Networking: hyperv1.ClusterNetworking{
							ClusterNetwork: []hyperv1.ClusterNetworkEntry{
								{CIDR: *ipnet.MustParseCIDR("10.128.0.0/14")},
							},
						},
					},
				},
			}

			sts := testStatefulSet.DeepCopy()
			err := adaptStatefulSet(cpContext, sts)
			g.Expect(err).ToNot(HaveOccurred())

			etcdContainer := sts.Spec.Template.Spec.Containers[0]
			metricsContainer := sts.Spec.Template.Spec.Containers[1]
			healthzContainer := sts.Spec.Template.Spec.Containers[2]

			g.Expect(valueForEnv(etcdContainer, "ETCD_TLS_MIN_VERSION")).To(Equal(tc.expectedTLSMinVersion))
			g.Expect(valueForFlag(metricsContainer, "--tls-min-version=")).To(Equal(tc.expectedTLSMinVersion))
			g.Expect(valueForFlag(healthzContainer, "--listen-tls-min-version=")).To(Equal(tc.expectedTLSMinVersion))

			if !tc.expectCipherSuites {
				g.Expect(valueForEnv(etcdContainer, "ETCD_CIPHER_SUITES")).To(BeEmpty())
				g.Expect(valueForFlag(metricsContainer, "--listen-cipher-suites=")).To(BeEmpty())
				g.Expect(valueForFlag(healthzContainer, "--listen-cipher-suites=")).To(BeEmpty())
				return
			}

			g.Expect(valueForEnv(etcdContainer, "ETCD_CIPHER_SUITES")).ToNot(BeEmpty())
			g.Expect(valueForFlag(metricsContainer, "--listen-cipher-suites=")).ToNot(BeEmpty())
			g.Expect(valueForFlag(healthzContainer, "--listen-cipher-suites=")).ToNot(BeEmpty())

			if tc.expectedCipherSuites != "" {
				g.Expect(valueForEnv(etcdContainer, "ETCD_CIPHER_SUITES")).To(Equal(tc.expectedCipherSuites))
				g.Expect(valueForFlag(metricsContainer, "--listen-cipher-suites=")).To(Equal(tc.expectedCipherSuites))
				g.Expect(valueForFlag(healthzContainer, "--listen-cipher-suites=")).To(Equal(tc.expectedCipherSuites))
			}
		})
	}

	for _, tc := range etcdListenURLTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			sts, err := assets.LoadStatefulSetManifestTemplated(ComponentName, etcdTemplateData(ComponentName))
			g.Expect(err).ToNot(HaveOccurred())
			etcdContainer := podspec.FindContainer(ComponentName, sts.Spec.Template.Spec.Containers)
			g.Expect(etcdContainer).ToNot(BeNil())
			assetCommand := slices.Clone(etcdContainer.Command)

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Namespace: etcdTestNamespace},
					Spec: hyperv1.HostedControlPlaneSpec{
						Etcd: hyperv1.EtcdSpec{
							ManagementType: hyperv1.Managed,
							Managed: &hyperv1.ManagedEtcdSpec{
								Storage: hyperv1.ManagedEtcdStorageSpec{
									Type: hyperv1.PersistentVolumeEtcdStorage,
								},
							},
						},
						Networking: hyperv1.ClusterNetworking{
							ClusterNetwork: []hyperv1.ClusterNetworkEntry{
								{CIDR: *ipnet.MustParseCIDR(tc.guestCIDR)},
							},
						},
						ControllerAvailabilityPolicy: hyperv1.HighlyAvailable,
					},
				},
			}

			g.Expect(adaptStatefulSet(cpContext, sts)).To(Succeed())

			expectEtcdListenURLsAtStartup(t, sts, assetCommand, "etcd-0", "etcd-discovery", tc)
		})
	}

	baseInitContainers := []corev1.Container{
		{Name: "ensure-dns"},
		{Name: "reset-member"},
	}

	initContainerTestCases := []struct {
		name               string
		restoreURL         string
		snapshotRestored   bool
		baseInitContainers []corev1.Container
		expectedOrder      []string
	}{
		{
			name:          "When restoreSnapshotURL is set, it should place etcd-init before reset-member",
			restoreURL:    "https://etcd-backup-bucket.s3.us-east-1.amazonaws.com/backups/etcd-snapshot-2024-01-15.db",
			expectedOrder: []string{"ensure-dns", "etcd-init", "reset-member"},
		},
		{
			name:          "When restoreSnapshotURL is not set, it should not add etcd-init",
			restoreURL:    "",
			expectedOrder: []string{"ensure-dns", "reset-member"},
		},
		{
			name:             "When EtcdSnapshotRestored condition is true, it should not add etcd-init even if restoreSnapshotURL is set",
			restoreURL:       "https://etcd-backup-bucket.s3.us-east-1.amazonaws.com/backups/etcd-snapshot-2024-01-15.db",
			snapshotRestored: true,
			expectedOrder:    []string{"ensure-dns", "reset-member"},
		},
		{
			name:               "When reset-member is absent, it should append etcd-init at the end",
			restoreURL:         "https://etcd-backup-bucket.s3.us-east-1.amazonaws.com/backups/etcd-snapshot-2024-01-15.db",
			baseInitContainers: []corev1.Container{{Name: "ensure-dns"}},
			expectedOrder:      []string{"ensure-dns", "etcd-init"},
		},
	}

	for _, tc := range initContainerTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
						Managed: &hyperv1.ManagedEtcdSpec{
							Storage: hyperv1.ManagedEtcdStorageSpec{
								Type: hyperv1.PersistentVolumeEtcdStorage,
							},
						},
					},
					Networking: hyperv1.ClusterNetworking{
						ClusterNetwork: []hyperv1.ClusterNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("10.0.0.0/16")},
						},
					},
					ControllerAvailabilityPolicy: hyperv1.SingleReplica,
				},
			}
			if tc.restoreURL != "" {
				hcp.Spec.Etcd.Managed.Storage.RestoreSnapshotURL = []string{tc.restoreURL}
			}
			if tc.snapshotRestored {
				hcp.Status.Conditions = []metav1.Condition{
					{
						Type:   string(hyperv1.EtcdSnapshotRestored),
						Status: metav1.ConditionTrue,
					},
				}
			}

			src := tc.baseInitContainers
			if src == nil {
				src = baseInitContainers
			}
			containers := make([]corev1.Container, len(src))
			copy(containers, src)
			sts := &appsv1.StatefulSet{}
			sts.Spec.Template.Spec.InitContainers = containers
			sts.Spec.Template.Spec.Containers = []corev1.Container{{Name: ComponentName}, {Name: "etcd-metrics"}}

			err := adaptStatefulSet(component.WorkloadContext{Context: context.Background(), HCP: hcp}, sts)
			g.Expect(err).ToNot(HaveOccurred())

			names := make([]string, len(sts.Spec.Template.Spec.InitContainers))
			for i, c := range sts.Spec.Template.Spec.InitContainers {
				names[i] = c.Name
			}
			g.Expect(names).To(Equal(tc.expectedOrder))
		})
	}
}

const (
	// etcdTestNamespace is the hosted control plane namespace used by the startup tests.
	etcdTestNamespace = "clusters-example"
	// etcdBinaryPath and etcdClusterStatePath are the paths the rendered etcd container
	// command uses; the startup tests replace them with test doubles.
	etcdBinaryPath       = "/usr/bin/etcd"
	etcdClusterStatePath = "/etc/etcd/clusterstate/state"
)

// etcdListenURLTestCase is one cell of the hosted cluster network family by
// management pod IP family matrix that both the default and the shard etcd
// StatefulSets must handle.
type etcdListenURLTestCase struct {
	name                string
	guestCIDR           string
	podIP               string
	expectedPeerURLs    string
	expectedClientURLs  string
	expectedMetricsURLs string
	expectedMetricsArgs []string
}

// etcdListenURLTestCases returns the listen URL matrix shared by the default and
// shard StatefulSet tests so both paths are held to identical expectations.
func etcdListenURLTestCases() []etcdListenURLTestCase {
	ipv4MetricsArgs := []string{"--listen-addr=127.0.0.1:2383", "--metrics-addr=https://0.0.0.0:2381"}
	ipv6MetricsArgs := []string{"--listen-addr=[::1]:2383", "--metrics-addr=https://[::]:2381"}
	return []etcdListenURLTestCase{
		{
			name:                "When guest network is IPv6 and pod IP is IPv4, it should start etcd with unbracketed IPv4 listen URLs",
			guestCIDR:           "fd01::/48",
			podIP:               "10.128.2.47",
			expectedPeerURLs:    "https://10.128.2.47:2380",
			expectedClientURLs:  "https://10.128.2.47:2379,https://localhost:2379",
			expectedMetricsURLs: "https://[::]:2382",
			expectedMetricsArgs: ipv6MetricsArgs,
		},
		{
			name:                "When guest network is IPv4 and pod IP is IPv4, it should start etcd with unbracketed IPv4 listen URLs",
			guestCIDR:           "10.128.0.0/14",
			podIP:               "10.128.2.47",
			expectedPeerURLs:    "https://10.128.2.47:2380",
			expectedClientURLs:  "https://10.128.2.47:2379,https://localhost:2379",
			expectedMetricsURLs: "https://0.0.0.0:2382",
			expectedMetricsArgs: ipv4MetricsArgs,
		},
		{
			name:                "When guest network is IPv6 and pod IP is IPv6, it should start etcd with bracketed IPv6 listen URLs",
			guestCIDR:           "fd01::/48",
			podIP:               "fd00::5",
			expectedPeerURLs:    "https://[fd00::5]:2380",
			expectedClientURLs:  "https://[fd00::5]:2379,https://localhost:2379",
			expectedMetricsURLs: "https://[::]:2382",
			expectedMetricsArgs: ipv6MetricsArgs,
		},
		{
			name:                "When guest network is IPv4 and pod IP is IPv6, it should start etcd with bracketed IPv6 listen URLs",
			guestCIDR:           "10.128.0.0/14",
			podIP:               "fd00::5",
			expectedPeerURLs:    "https://[fd00::5]:2380",
			expectedClientURLs:  "https://[fd00::5]:2379,https://localhost:2379",
			expectedMetricsURLs: "https://0.0.0.0:2382",
			expectedMetricsArgs: ipv4MetricsArgs,
		},
	}
}

// expectEtcdListenURLsAtStartup verifies that an adapted etcd StatefulSet keeps the
// shared asset command, does not choose listen URLs from the hosted cluster network
// family, and that executing the rendered command gives etcd listen URLs that match
// the pod IP family, for both a new and an existing cluster state.
func expectEtcdListenURLsAtStartup(t *testing.T, sts *appsv1.StatefulSet, assetCommand []string, podName, discoveryService string, tc etcdListenURLTestCase) {
	t.Helper()
	g := NewWithT(t)

	etcdContainer := podspec.FindContainer(ComponentName, sts.Spec.Template.Spec.Containers)
	g.Expect(etcdContainer).ToNot(BeNil())
	metricsContainer := podspec.FindContainer("etcd-metrics", sts.Spec.Template.Spec.Containers)
	g.Expect(metricsContainer).ToNot(BeNil())

	g.Expect(etcdContainer.Command).To(Equal(assetCommand), "adaptation must keep the shared asset command")
	for _, name := range []string{"ETCD_LISTEN_PEER_URLS", "ETCD_LISTEN_CLIENT_URLS"} {
		g.Expect(podspec.FindEnvVar(name, etcdContainer.Env)).To(BeNil(),
			"%s must not be rendered from the hosted cluster network family", name)
	}
	g.Expect(podspec.FindEnvVar("ETCD_LISTEN_METRICS_URLS", etcdContainer.Env)).To(HaveField("Value", tc.expectedMetricsURLs))
	g.Expect(metricsContainer.Args).To(ContainElements(tc.expectedMetricsArgs))

	expectedPeerAdvertiseURL := fmt.Sprintf("https://%s.%s.%s.svc:2380", podName, discoveryService, etcdTestNamespace)
	expectedClientAdvertiseURL := fmt.Sprintf("https://%s.%s.%s.svc:2379", podName, discoveryService, etcdTestNamespace)

	for _, clusterState := range []string{"new", "existing"} {
		result := runEtcdContainerCommand(t, *etcdContainer, podName, tc.podIP, clusterState)

		g.Expect(result.stubPID).To(Equal(result.shellPID), "etcd must replace the shell through exec")
		g.Expect(result.env).To(HaveKeyWithValue("ETCD_LISTEN_PEER_URLS", tc.expectedPeerURLs))
		g.Expect(result.env).To(HaveKeyWithValue("ETCD_LISTEN_CLIENT_URLS", tc.expectedClientURLs))
		g.Expect(result.env).To(HaveKeyWithValue("ETCD_INITIAL_CLUSTER_STATE", clusterState))
		g.Expect(result.env).To(HaveKeyWithValue("ETCD_LISTEN_METRICS_URLS", tc.expectedMetricsURLs))
		g.Expect(result.env).To(HaveKeyWithValue("ETCD_INITIAL_ADVERTISE_PEER_URLS", expectedPeerAdvertiseURL))
		g.Expect(result.env).To(HaveKeyWithValue("ETCD_ADVERTISE_CLIENT_URLS", expectedClientAdvertiseURL))
		g.Expect(result.env).ToNot(HaveKey("listen_host"), "the script helper variable must not reach etcd")

		expectListenURL(g, result.env["ETCD_LISTEN_PEER_URLS"], tc.podIP, "2380")
		clientURLs := strings.Split(result.env["ETCD_LISTEN_CLIENT_URLS"], ",")
		g.Expect(clientURLs).To(HaveLen(2))
		expectListenURL(g, clientURLs[0], tc.podIP, "2379")
		g.Expect(clientURLs[1]).To(Equal("https://localhost:2379"))
	}
}

// etcdStartupResult is what a stub standing in for the etcd binary observed when
// the rendered etcd container command executed it.
type etcdStartupResult struct {
	env      map[string]string
	stubPID  int
	shellPID int
}

// runEtcdContainerCommand executes the rendered etcd container command with /bin/sh,
// the way the container runtime does, after resolving the container environment like
// the kubelet (downward API values and $(VAR) references). Only the etcd binary and
// the cluster state file paths are replaced with test doubles.
func runEtcdContainerCommand(t *testing.T, c corev1.Container, podName, podIP, clusterState string) etcdStartupResult {
	t.Helper()
	g := NewWithT(t)

	g.Expect(c.Command).To(HaveLen(3))
	g.Expect(c.Command[:2]).To(Equal([]string{"/bin/sh", "-c"}))
	script := c.Command[2]
	g.Expect(strings.Count(script, etcdBinaryPath)).To(Equal(1))
	g.Expect(strings.Count(script, etcdClusterStatePath)).To(Equal(1))
	g.Expect(script).To(ContainSubstring("exec " + etcdBinaryPath))
	// The kubelet rewrites $(NAME) references to container env vars before the shell
	// runs; the only command substitution must be the cluster state read.
	g.Expect(strings.Count(script, "$(")).To(Equal(1))
	g.Expect(script).To(ContainSubstring("$(cat " + etcdClusterStatePath + ")"))

	fieldValues := map[string]string{
		"status.podIP":       podIP,
		"metadata.name":      podName,
		"metadata.namespace": etcdTestNamespace,
	}
	resolved := map[string]string{}
	expand := func(s string) string {
		for name, value := range resolved {
			s = strings.ReplaceAll(s, "$("+name+")", value)
		}
		return s
	}
	environ := []string{"PATH=" + os.Getenv("PATH")}
	for _, env := range c.Env {
		switch {
		case env.ValueFrom == nil:
			resolved[env.Name] = expand(env.Value)
		case env.ValueFrom.FieldRef != nil:
			value, ok := fieldValues[env.ValueFrom.FieldRef.FieldPath]
			g.Expect(ok).To(BeTrue(), "unsupported downward API field %q for env %s", env.ValueFrom.FieldRef.FieldPath, env.Name)
			resolved[env.Name] = value
		default:
			t.Fatalf("unsupported env source for %s", env.Name)
		}
		environ = append(environ, env.Name+"="+resolved[env.Name])
	}

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state")
	g.Expect(os.WriteFile(statePath, []byte(clusterState+"\n"), 0o600)).To(Succeed())
	stubPath := filepath.Join(dir, "etcd")
	g.Expect(os.WriteFile(stubPath, []byte("#!/bin/sh\necho \"STUB_PID=$$\"\nexec env\n"), 0o700)).To(Succeed())

	shellQuote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	script = strings.NewReplacer(etcdBinaryPath, shellQuote(stubPath), etcdClusterStatePath, shellQuote(statePath)).Replace(expand(script))

	cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", script)
	cmd.Env = environ
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	g.Expect(cmd.Run()).To(Succeed(), "stderr: %s", stderr.String())

	result := etcdStartupResult{env: map[string]string{}, shellPID: cmd.Process.Pid}
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if key == "STUB_PID" {
			pid, err := strconv.Atoi(value)
			g.Expect(err).ToNot(HaveOccurred())
			result.stubPID = pid
			continue
		}
		result.env[key] = value
	}
	g.Expect(result.stubPID).ToNot(BeZero(), "the etcd stub did not run; stdout: %s", stdout.String())
	return result
}

// expectListenURL asserts that rawURL is an https URL whose host is exactly podIP,
// bracketed only when podIP is an IPv6 literal, so etcd can parse and bind it.
func expectListenURL(g Gomega, rawURL, podIP, port string) {
	u, err := url.Parse(rawURL)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(u.Scheme).To(Equal("https"))
	g.Expect(u.Host).To(Equal(net.JoinHostPort(podIP, port)))
	addr, err := netip.ParseAddr(u.Hostname())
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(addr).To(Equal(netip.MustParseAddr(podIP)))
}
