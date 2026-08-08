package ignitionserverproxy

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	testCases := []struct {
		name                    string
		additionalTrustBundle   *corev1.LocalObjectReference
		expectTrustBundleVolume bool
		expectTrustBundleMount  bool
		expectedVolumeName      string
		expectedConfigMapName   string
	}{
		{
			name:                    "When no additional trust bundle is set, it should not add trust bundle volume",
			additionalTrustBundle:   nil,
			expectTrustBundleVolume: false,
			expectTrustBundleMount:  false,
		},
		{
			name: "When additional trust bundle is set, it should add trust bundle volume and mount",
			additionalTrustBundle: &corev1.LocalObjectReference{
				Name: "custom-ca-bundle",
			},
			expectTrustBundleVolume: true,
			expectTrustBundleMount:  true,
			expectedVolumeName:      "trusted-ca",
			expectedConfigMapName:   "custom-ca-bundle",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					AdditionalTrustBundle: tc.additionalTrustBundle,
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify trust bundle volume configuration
			if tc.expectTrustBundleVolume {
				volume := podspec.FindVolume(tc.expectedVolumeName, deployment.Spec.Template.Spec.Volumes)
				g.Expect(volume).ToNot(BeNil(), "trust bundle volume should exist")
				g.Expect(volume.ConfigMap).ToNot(BeNil(), "volume should use ConfigMap source")
				g.Expect(volume.ConfigMap.Name).To(Equal(tc.expectedConfigMapName))
				g.Expect(volume.ConfigMap.Items).To(HaveLen(1))
				g.Expect(volume.ConfigMap.Items[0].Key).To(Equal("ca-bundle.crt"))
				g.Expect(volume.ConfigMap.Items[0].Path).To(Equal("user-ca-bundle.pem"))
			} else {
				// Verify trust bundle volume is NOT present when not configured
				g.Expect(podspec.FindVolume("trusted-ca", deployment.Spec.Template.Spec.Volumes)).To(BeNil(), "trust bundle volume should not exist")
			}

			// Verify trust bundle mount on first container (DeploymentAddTrustBundleVolume adds to first container)
			if tc.expectTrustBundleMount {
				g.Expect(deployment.Spec.Template.Spec.Containers).ToNot(BeEmpty(), "deployment should have containers")

				firstContainer := &deployment.Spec.Template.Spec.Containers[0]
				mount := podspec.FindVolumeMount(tc.expectedVolumeName, firstContainer.VolumeMounts)
				g.Expect(mount).ToNot(BeNil(), "trust bundle volume mount should exist on first container")
				g.Expect(mount.MountPath).To(Equal("/etc/pki/tls/certs"))
				g.Expect(mount.ReadOnly).To(BeTrue())
			} else {
				// Verify trust bundle mount is NOT present
				if len(deployment.Spec.Template.Spec.Containers) > 0 {
					firstContainer := &deployment.Spec.Template.Spec.Containers[0]
					g.Expect(podspec.FindVolumeMount("trusted-ca", firstContainer.VolumeMounts)).To(BeNil(), "trust bundle volume mount should not exist")
				}
			}
		})
	}
}

func TestAdaptDeploymentWithProxyEnvVars(t *testing.T) {
	g := NewWithT(t)

	// Set proxy environment variables for this test
	t.Setenv("HTTP_PROXY", "http://proxy.example.com:8080")
	t.Setenv("HTTPS_PROXY", "https://proxy.example.com:8443")
	t.Setenv("NO_PROXY", "localhost,127.0.0.1")

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{},
	}

	cpContext := component.WorkloadContext{
		HCP: hcp,
	}

	deployment, err := assets.LoadDeploymentManifest(ComponentName)
	g.Expect(err).ToNot(HaveOccurred())

	err = adaptDeployment(cpContext, deployment)
	g.Expect(err).ToNot(HaveOccurred())

	// Find haproxy container
	haproxyContainer := podspec.FindContainer("haproxy", deployment.Spec.Template.Spec.Containers)
	g.Expect(haproxyContainer).ToNot(BeNil(), "haproxy container should exist")

	// Verify proxy env vars are set
	httpProxy := podspec.FindEnvVar("HTTP_PROXY", haproxyContainer.Env)
	g.Expect(httpProxy).ToNot(BeNil())
	g.Expect(httpProxy.Value).To(Equal("http://proxy.example.com:8080"))

	httpsProxy := podspec.FindEnvVar("HTTPS_PROXY", haproxyContainer.Env)
	g.Expect(httpsProxy).ToNot(BeNil())
	g.Expect(httpsProxy.Value).To(Equal("https://proxy.example.com:8443"))

	noProxy := podspec.FindEnvVar("NO_PROXY", haproxyContainer.Env)
	g.Expect(noProxy).ToNot(BeNil())
	g.Expect(noProxy.Value).To(ContainSubstring("localhost"))
	g.Expect(noProxy.Value).To(ContainSubstring("kube-apiserver"))
}

func TestAdaptHAProxyConfig(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                 string
		tlsProfile           *configv1.TLSSecurityProfile
		expectedMinTLSVer    string
		expectedCiphers      string
		expectCiphersPresent bool
	}{
		{
			name:                 "Nil profile defaults to Intermediate",
			expectedMinTLSVer:    "TLSv1.2",
			expectedCiphers:      "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305",
			expectCiphersPresent: true,
		},
		{
			name: "Modern profile has TLS13 and no ciphers",
			tlsProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
			expectedMinTLSVer: "TLSv1.3",
		},
		{
			name: "Intermediate profile has TLS12 and intermediate ciphers",
			tlsProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileIntermediateType,
			},
			expectedMinTLSVer:    "TLSv1.2",
			expectedCiphers:      "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305",
			expectCiphersPresent: true,
		},
		{
			name: "Old profile has TLS10 and old ciphers",
			tlsProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileOldType,
			},
			expectedMinTLSVer:    "TLSv1.0",
			expectedCiphers:      "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305:ECDHE-ECDSA-AES128-SHA256:ECDHE-RSA-AES128-SHA256:ECDHE-ECDSA-AES128-SHA:ECDHE-RSA-AES128-SHA:ECDHE-ECDSA-AES256-SHA:ECDHE-RSA-AES256-SHA:AES128-GCM-SHA256:AES256-GCM-SHA384:AES128-SHA256:AES128-SHA:AES256-SHA:DES-CBC3-SHA",
			expectCiphersPresent: true,
		},
		{
			name: "Custom profile uses custom TLS version and ciphers",
			tlsProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
						Ciphers: []string{
							"ECDHE-RSA-AES128-GCM-SHA256",
							"ECDHE-RSA-AES256-GCM-SHA384",
						},
					},
				},
			},
			expectedMinTLSVer:    "TLSv1.2",
			expectedCiphers:      "ECDHE-RSA-AES128-GCM-SHA256:ECDHE-RSA-AES256-GCM-SHA384",
			expectCiphersPresent: true,
		},
		{
			name: "Custom profile filters out TLS13 ciphers",
			tlsProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
						Ciphers: []string{
							"TLS_AES_128_GCM_SHA256",
							"ECDHE-RSA-AES128-GCM-SHA256",
							"TLS_CHACHA20_POLY1305_SHA256",
							"ECDHE-RSA-AES256-GCM-SHA384",
						},
					},
				},
			},
			expectedMinTLSVer:    "TLSv1.2",
			expectedCiphers:      "ECDHE-RSA-AES128-GCM-SHA256:ECDHE-RSA-AES256-GCM-SHA384",
			expectCiphersPresent: true,
		},
		{
			name: "Custom profile with all TLS13 ciphers results in no cipher config",
			tlsProfile: &configv1.TLSSecurityProfile{
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
			expectedMinTLSVer: "TLSv1.3",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{},
			}

			if tc.tlsProfile != nil {
				hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
					APIServer: &configv1.APIServerSpec{
						TLSSecurityProfile: tc.tlsProfile,
					},
				}
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "haproxy-config",
					Namespace: "test-namespace",
				},
			}

			err := adaptHAProxyConfig(cpContext, cm)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(cm.Data).ToNot(BeNil())
			g.Expect(cm.Data["haproxy.conf"]).ToNot(BeEmpty())

			config := cm.Data["haproxy.conf"]

			g.Expect(config).To(ContainSubstring("ssl-min-ver " + tc.expectedMinTLSVer))
			tlsVersionCount := strings.Count(config, "ssl-min-ver "+tc.expectedMinTLSVer)
			g.Expect(tlsVersionCount).To(Equal(2), "ssl-min-ver should appear twice (bind and server)")
			totalSSLMinVerCount := strings.Count(config, "ssl-min-ver ")
			g.Expect(totalSSLMinVerCount).To(Equal(2), "ssl-min-ver should only appear exactly twice total")

			if tc.expectCiphersPresent {
				g.Expect(config).To(ContainSubstring("ciphers " + tc.expectedCiphers))
				cipherCount := strings.Count(config, "ciphers "+tc.expectedCiphers)
				g.Expect(cipherCount).To(Equal(2), "ciphers should appear twice (bind and server)")
				totalCiphersCount := strings.Count(config, "ciphers ")
				g.Expect(totalCiphersCount).To(Equal(2), "ciphers should only appear exactly twice total")
			} else {
				g.Expect(config).ToNot(ContainSubstring("ciphers "))
			}
		})
	}
}

func TestAdaptHAProxyConfigErrors(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name              string
		tlsProfile        *configv1.TLSSecurityProfile
		expectedErrSubstr string
	}{
		{
			name: "Custom profile with nil Custom field returns error",
			tlsProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
			},
			expectedErrSubstr: "Custom but Custom field is nil",
		},
		{
			name: "Unknown profile type returns error",
			tlsProfile: &configv1.TLSSecurityProfile{
				Type: "UnknownProfileType",
			},
			expectedErrSubstr: "unknown TLS profile type",
		},
		{
			name: "Empty profile type returns error",
			tlsProfile: &configv1.TLSSecurityProfile{
				Type: "",
			},
			expectedErrSubstr: "unknown TLS profile type",
		},
		{
			name: "Unknown cipher name is rejected",
			tlsProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
						Ciphers: []string{
							"ECDHE-RSA-AES128-GCM-SHA256",
							"UNKNOWN-CIPHER-NAME",
						},
					},
				},
			},
			expectedErrSubstr: "unknown OpenSSL cipher name",
		},
		{
			name: "Cipher with injection attempt is rejected",
			tlsProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
						Ciphers: []string{
							"ECDHE-RSA-AES128-GCM-SHA256",
							"MALICIOUS CIPHER",
						},
					},
				},
			},
			expectedErrSubstr: "unknown OpenSSL cipher name",
		},
		{
			name: "Empty cipher name is rejected",
			tlsProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
						Ciphers: []string{
							"ECDHE-RSA-AES128-GCM-SHA256",
							"",
						},
					},
				},
			},
			expectedErrSubstr: "cipher name cannot be empty",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						APIServer: &configv1.APIServerSpec{
							TLSSecurityProfile: tc.tlsProfile,
						},
					},
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "haproxy-config",
					Namespace: "test-namespace",
				},
			}

			err := adaptHAProxyConfig(cpContext, cm)
			g.Expect(err).To(HaveOccurred())
			g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrSubstr))
		})
	}
}
