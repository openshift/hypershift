package oauth

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestAdaptDeploymentWithHTPasswdIDP verifies that HTPasswd IDP volumes are properly
// added to the oauth-openshift deployment. This is critical for OADP restore scenarios.
func TestAdaptDeploymentWithHTPasswdIDP(t *testing.T) {
	testCases := []struct {
		name                   string
		identityProviders      []configv1.IdentityProvider
		idpSecrets             []*corev1.Secret
		expectedVolumesCount   int
		expectedVolumePrefixes []string
		expectError            bool
		errorContains          string
	}{
		{
			name: "When HTPasswd IDP is configured, it should add IDP volumes to deployment",
			identityProviders: []configv1.IdentityProvider{
				{
					Name: "htpasswd-idp",
					IdentityProviderConfig: configv1.IdentityProviderConfig{
						Type: configv1.IdentityProviderTypeHTPasswd,
						HTPasswd: &configv1.HTPasswdIdentityProvider{
							FileData: configv1.SecretNameReference{
								Name: "htpasswd-secret",
							},
						},
					},
				},
			},
			idpSecrets: []*corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "htpasswd-secret",
						Namespace: "test-namespace",
					},
					Data: map[string][]byte{
						"htpasswd": []byte("testuser:$apr1$xyz"),
					},
				},
			},
			expectedVolumesCount:   1, // 1 for htpasswd secret
			expectedVolumePrefixes: []string{"idp-secret-"},
			expectError:            false,
		},
		{
			name: "When HTPasswd IDP secret is missing, it should still add volume reference (pod will wait for secret)",
			identityProviders: []configv1.IdentityProvider{
				{
					Name: "htpasswd-idp",
					IdentityProviderConfig: configv1.IdentityProviderConfig{
						Type: configv1.IdentityProviderTypeHTPasswd,
						HTPasswd: &configv1.HTPasswdIdentityProvider{
							FileData: configv1.SecretNameReference{
								Name: "missing-secret",
							},
						},
					},
				},
			},
			idpSecrets: []*corev1.Secret{},
			// Volume reference is still added even if secret doesn't exist yet
			// This is important for OADP restore where secrets may be restored after HCP
			expectedVolumesCount:   1,
			expectedVolumePrefixes: []string{"idp-secret-"},
			expectError:            false,
		},
		{
			name: "When multiple IDPs are configured, it should add all IDP volumes",
			identityProviders: []configv1.IdentityProvider{
				{
					Name: "htpasswd-idp",
					IdentityProviderConfig: configv1.IdentityProviderConfig{
						Type: configv1.IdentityProviderTypeHTPasswd,
						HTPasswd: &configv1.HTPasswdIdentityProvider{
							FileData: configv1.SecretNameReference{
								Name: "htpasswd-secret",
							},
						},
					},
				},
				{
					Name: "github-idp",
					IdentityProviderConfig: configv1.IdentityProviderConfig{
						Type: configv1.IdentityProviderTypeGitHub,
						GitHub: &configv1.GitHubIdentityProvider{
							ClientID: "test-client-id",
							ClientSecret: configv1.SecretNameReference{
								Name: "github-secret",
							},
						},
					},
				},
			},
			idpSecrets: []*corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "htpasswd-secret",
						Namespace: "test-namespace",
					},
					Data: map[string][]byte{
						"htpasswd": []byte("testuser:$apr1$xyz"),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "github-secret",
						Namespace: "test-namespace",
					},
					Data: map[string][]byte{
						"clientSecret": []byte("github-client-secret"),
					},
				},
			},
			expectedVolumesCount:   2, // 1 for htpasswd, 1 for github client secret
			expectedVolumePrefixes: []string{"idp-secret-"},
			expectError:            false,
		},
		{
			name:                   "When no IDPs are configured, it should not add IDP volumes",
			identityProviders:      []configv1.IdentityProvider{},
			idpSecrets:             []*corev1.Secret{},
			expectedVolumesCount:   0,
			expectedVolumePrefixes: []string{},
			expectError:            false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			// Create fake client with IDP secrets
			fakeClientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			for _, secret := range tc.idpSecrets {
				fakeClientBuilder.WithObjects(secret)
			}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						OAuth: &configv1.OAuthSpec{
							IdentityProviders: tc.identityProviders,
						},
					},
				},
			}

			cpContext := component.WorkloadContext{
				Client: fakeClientBuilder.Build(),
				HCP:    hcp,
			}

			// Load the deployment manifest
			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			// Count initial volumes (from the static manifest)
			initialVolumeCount := len(deployment.Spec.Template.Spec.Volumes)

			// Adapt the deployment
			err = adaptDeployment(cpContext, deployment)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				if tc.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.errorContains))
				}
				return
			}

			g.Expect(err).ToNot(HaveOccurred())

			// Count IDP volumes added
			idpVolumeCount := 0
			for _, vol := range deployment.Spec.Template.Spec.Volumes {
				for _, prefix := range tc.expectedVolumePrefixes {
					if len(vol.Name) > len(prefix) && vol.Name[:len(prefix)] == prefix {
						idpVolumeCount++
						break
					}
				}
			}

			// Verify the correct number of IDP volumes were added
			g.Expect(idpVolumeCount).To(Equal(tc.expectedVolumesCount), "Expected %d IDP volumes but found %d", tc.expectedVolumesCount, idpVolumeCount)

			// Verify total volume count
			expectedTotalVolumes := initialVolumeCount + tc.expectedVolumesCount
			g.Expect(len(deployment.Spec.Template.Spec.Volumes)).To(Equal(expectedTotalVolumes), "Expected total %d volumes but found %d", expectedTotalVolumes, len(deployment.Spec.Template.Spec.Volumes))

			// If IDP volumes are expected, verify they are properly configured
			if tc.expectedVolumesCount > 0 {
				// Find the oauth-openshift container
				var oauthContainer *corev1.Container
				for i := range deployment.Spec.Template.Spec.Containers {
					if deployment.Spec.Template.Spec.Containers[i].Name == ComponentName {
						oauthContainer = &deployment.Spec.Template.Spec.Containers[i]
						break
					}
				}
				g.Expect(oauthContainer).ToNot(BeNil(), "oauth-openshift container should exist")

				// Verify IDP volume mounts are added to the container
				idpVolumeMountCount := 0
				for _, vm := range oauthContainer.VolumeMounts {
					for _, prefix := range tc.expectedVolumePrefixes {
						if len(vm.Name) > len(prefix) && vm.Name[:len(prefix)] == prefix {
							idpVolumeMountCount++
							break
						}
					}
				}
				g.Expect(idpVolumeMountCount).To(Equal(tc.expectedVolumesCount), "Expected %d IDP volume mounts but found %d", tc.expectedVolumesCount, idpVolumeMountCount)
			}
		})
	}
}

// TestAdaptDeploymentOADPRestoreScenario specifically tests the OADP restore scenario
// where IDP conversion might initially fail but should retry and eventually succeed.
func TestAdaptDeploymentOADPRestoreScenario(t *testing.T) {
	g := NewGomegaWithT(t)

	// Scenario: After OADP restore, the secret exists
	htpasswdSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "htpasswd-secret",
			Namespace: "test-namespace",
		},
		Data: map[string][]byte{
			"htpasswd": []byte("admin:$apr1$xyz123"),
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(htpasswdSecret).
		Build()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "restored-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Configuration: &hyperv1.ClusterConfiguration{
				OAuth: &configv1.OAuthSpec{
					IdentityProviders: []configv1.IdentityProvider{
						{
							Name: "htpasswd",
							IdentityProviderConfig: configv1.IdentityProviderConfig{
								Type: configv1.IdentityProviderTypeHTPasswd,
								HTPasswd: &configv1.HTPasswdIdentityProvider{
									FileData: configv1.SecretNameReference{
										Name: "htpasswd-secret",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	cpContext := component.WorkloadContext{
		Client: fakeClient,
		HCP:    hcp,
	}

	// Load and adapt the deployment
	deployment, err := assets.LoadDeploymentManifest(ComponentName)
	g.Expect(err).ToNot(HaveOccurred())

	err = adaptDeployment(cpContext, deployment)
	g.Expect(err).ToNot(HaveOccurred(), "Deployment adaptation should succeed when IDP secret exists")

	// Verify the htpasswd volume was added
	hasHTPasswdVolume := false
	for _, vol := range deployment.Spec.Template.Spec.Volumes {
		if len(vol.Name) >= len("idp-secret-") && vol.Name[:len("idp-secret-")] == "idp-secret-" {
			hasHTPasswdVolume = true
			g.Expect(vol.Secret).ToNot(BeNil(), "IDP volume should be a secret volume")
			g.Expect(vol.Secret.SecretName).To(Equal("htpasswd-secret"), "IDP volume should reference the htpasswd secret")
			break
		}
	}
	g.Expect(hasHTPasswdVolume).To(BeTrue(), "HTPasswd IDP volume should be added to deployment")

	// Verify the volume mount was added to the oauth-openshift container
	var oauthContainer *corev1.Container
	for i := range deployment.Spec.Template.Spec.Containers {
		if deployment.Spec.Template.Spec.Containers[i].Name == ComponentName {
			oauthContainer = &deployment.Spec.Template.Spec.Containers[i]
			break
		}
	}
	g.Expect(oauthContainer).ToNot(BeNil(), "oauth-openshift container should exist")

	hasHTPasswdVolumeMount := false
	for _, vm := range oauthContainer.VolumeMounts {
		if len(vm.Name) >= len("idp-secret-") && vm.Name[:len("idp-secret-")] == "idp-secret-" {
			hasHTPasswdVolumeMount = true
			g.Expect(vm.MountPath).To(ContainSubstring("/etc/oauth/idp"), "IDP volume mount should be under /etc/oauth/idp")
			break
		}
	}
	g.Expect(hasHTPasswdVolumeMount).To(BeTrue(), "HTPasswd IDP volume mount should be added to oauth-openshift container")
}
