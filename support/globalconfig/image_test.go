package globalconfig

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
)

func TestReconcileImageConfig(t *testing.T) {
	t.Parallel()

	configured := configv1.ImageSpec{
		AllowedRegistriesForImport: []configv1.RegistryLocation{{DomainName: "quay.io"}},
		ExternalRegistryHostnames:  []string{"registry.apps.example.com"},
		AdditionalTrustedCA:        configv1.ConfigMapNameReference{Name: "mirror-registry-ca"},
		RegistrySources: configv1.RegistrySources{
			InsecureRegistries:               []string{"mirror.internal.example.com:5000"},
			AllowedRegistries:                []string{"quay.io", "mirror.internal.example.com:5000"},
			ContainerRuntimeSearchRegistries: []string{"quay.io"},
		},
		ImageStreamImportMode: configv1.ImportModeLegacy,
	}
	partial := configv1.ImageSpec{
		RegistrySources: configv1.RegistrySources{
			BlockedRegistries: []string{"blocked.example.com"},
		},
	}

	testCases := []struct {
		name          string
		existingSpec  configv1.ImageSpec
		configuration *hyperv1.ClusterConfiguration
		expectedSpec  configv1.ImageSpec
	}{
		{
			name:         "When cluster configuration is removed, it should clear the existing image spec",
			existingSpec: configured,
		},
		{
			name:          "When image configuration is removed, it should clear the existing image spec",
			existingSpec:  configured,
			configuration: &hyperv1.ClusterConfiguration{},
		},
		{
			name:         "When image configuration is explicitly empty, it should clear the existing image spec",
			existingSpec: configured,
			configuration: &hyperv1.ClusterConfiguration{
				Image: &configv1.ImageSpec{},
			},
		},
		{
			name: "When image configuration is populated, it should replace the existing image spec",
			configuration: &hyperv1.ClusterConfiguration{
				Image: &configured,
			},
			expectedSpec: configured,
		},
		{
			name:         "When image configuration drops fields, it should remove the omitted fields",
			existingSpec: configured,
			configuration: &hyperv1.ClusterConfiguration{
				Image: &partial,
			},
			expectedSpec: partial,
		},
		{
			name: "When image configuration and the existing spec are empty, it should leave the spec empty",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			image := ImageConfig()
			image.Spec = tc.existingSpec
			image.Labels = map[string]string{"app": "image-config"}
			image.Status.InternalRegistryHostname = "image-registry.openshift-image-registry.svc:5000"
			hcp := &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{Configuration: tc.configuration},
			}

			ReconcileImageConfig(image, hcp)

			g.Expect(image.Spec).To(Equal(tc.expectedSpec), "guest Image spec should match the reconciled HCP configuration")
			g.Expect(image.Labels).To(Equal(map[string]string{"app": "image-config"}), "reconciling Image spec should preserve labels")
			g.Expect(image.Status.InternalRegistryHostname).To(Equal("image-registry.openshift-image-registry.svc:5000"), "reconciling Image spec should preserve status")
		})
	}
}
