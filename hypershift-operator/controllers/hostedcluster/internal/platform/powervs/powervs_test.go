package powervs

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	"github.com/blang/semver"
	"github.com/google/go-cmp/cmp"
)

func buildPowerVSHostedControlPlane(tlsProfile *configv1.TLSSecurityProfile) *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Configuration: &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					TLSSecurityProfile: tlsProfile,
				},
			},
		},
	}
}

func TestCAPIProviderDeploymentSpec(t *testing.T) {
	defaultArgs := []string{
		"--namespace", "$(MY_NAMESPACE)",
		"--v=4",
		"--leader-elect=true",
		"--provider-id-fmt=v2",
	}

	customTLSProfile := &configv1.TLSSecurityProfile{
		Type: configv1.TLSProfileCustomType,
		Custom: &configv1.CustomTLSProfile{
			TLSProfileSpec: configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers: []string{
					"ECDHE-ECDSA-AES128-GCM-SHA256",
					"ECDHE-RSA-AES128-GCM-SHA256",
				},
			},
		},
	}

	testCases := []struct {
		name           string
		hcp            *hyperv1.HostedControlPlane
		payloadVersion *semver.Version
		expectedArgs   []string
	}{
		{
			name:           "When HostedControlPlane is nil it should not append TLS args",
			payloadVersion: ptr.To(semver.MustParse("4.23.0")),
			expectedArgs:   defaultArgs,
		},
		{
			name: "When version is 4.22 and HCP has TLS profile it should not append TLS args",
			hcp: buildPowerVSHostedControlPlane(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			}),
			payloadVersion: ptr.To(semver.MustParse("4.22.0")),
			expectedArgs:   defaultArgs,
		},
		{
			name: "When version is 4.23 and HCP has Modern TLS profile it should append min-version only",
			hcp: buildPowerVSHostedControlPlane(&configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			}),
			payloadVersion: ptr.To(semver.MustParse("4.23.0")),
			expectedArgs: append(defaultArgs,
				"--tls-min-version=VersionTLS13",
			),
		},
		{
			name:           "When version is 5.0 and HCP has custom TLS profile it should append custom TLS args",
			hcp:            buildPowerVSHostedControlPlane(customTLSProfile),
			payloadVersion: ptr.To(semver.MustParse("5.0.0")),
			expectedArgs: append(defaultArgs,
				"--tls-min-version=VersionTLS12",
				"--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
			),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			platform := New("", tc.payloadVersion)
			hcluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type:    hyperv1.PowerVSPlatform,
						PowerVS: &hyperv1.PowerVSPlatformSpec{},
					},
				},
			}
			spec, err := platform.CAPIProviderDeploymentSpec(hcluster, tc.hcp)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if spec == nil {
				t.Fatal("expected deployment spec, got nil")
			}
			if len(spec.Template.Spec.Containers) == 0 {
				t.Fatal("expected at least 1 container, got 0")
			}

			var managerContainer *corev1.Container
			for i := range spec.Template.Spec.Containers {
				if spec.Template.Spec.Containers[i].Name == "manager" {
					managerContainer = &spec.Template.Spec.Containers[i]
					break
				}
			}
			if managerContainer == nil {
				t.Fatal("manager container not found")
			}

			if diff := cmp.Diff(managerContainer.Args, tc.expectedArgs); diff != "" {
				t.Errorf("args differ (-got +want):\n%s", diff)
			}
		})
	}
}
