package openstack

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func buildHostedControlPlane(tlsProfile *configv1.TLSSecurityProfile) *hyperv1.HostedControlPlane {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.OpenStackPlatform,
				OpenStack: &hyperv1.OpenStackPlatformSpec{
					IdentityRef: hyperv1.OpenStackIdentityReference{
						Name: "test-cloud-credentials",
					},
				},
			},
		},
	}

	if tlsProfile != nil {
		hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
			APIServer: &configv1.APIServerSpec{
				TLSSecurityProfile: tlsProfile,
			},
		}
	}

	return hcp
}

func buildDeployment(args []string) *appsv1.Deployment {
	return &appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "cloud-controller-manager",
							Args: append([]string{}, args...),
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: secretOCCMVolumeName,
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "old-secret",
								},
							},
						},
					},
				},
			},
		},
	}
}

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	baseArgs := []string{
		"--cloud-provider=openstack",
		"--use-service-account-credentials=true",
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
		name         string
		tlsProfile   *configv1.TLSSecurityProfile
		expectedArgs []string
	}{
		{
			name:       "When TLS profile is nil it should append intermediate defaults",
			tlsProfile: nil,
			expectedArgs: append(baseArgs,
				"--tls-min-version=VersionTLS12",
				"--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256",
			),
		},
		{
			name: "When using Modern TLS profile it should append only min-version",
			tlsProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileModernType,
			},
			expectedArgs: append(baseArgs,
				"--tls-min-version=VersionTLS13",
			),
		},
		{
			name:       "When using Custom TLS profile it should append custom TLS args",
			tlsProfile: customTLSProfile,
			expectedArgs: append(baseArgs,
				"--tls-min-version=VersionTLS12",
				"--tls-cipher-suites=TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
			),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := buildHostedControlPlane(tc.tlsProfile)
			deployment := buildDeployment(baseArgs)

			// Create credentials secret
			credentialsSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cloud-credentials",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{
					"clouds.yaml": []byte("test-data"),
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithObjects(credentialsSecret).
				Build()

			cpContext := component.WorkloadContext{
				HCP:    hcp,
				Client: fakeClient,
			}
			err := adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			container := podspec.FindContainer("cloud-controller-manager", deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil(), "cloud-controller-manager container should exist")
			g.Expect(container.Args).To(Equal(tc.expectedArgs))
		})
	}
}
