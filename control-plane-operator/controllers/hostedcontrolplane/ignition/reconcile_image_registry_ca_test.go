package ignition

import (
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/config"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8syaml "sigs.k8s.io/yaml"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
)

func TestReconcileImageRegistryCAIgnitionConfig(t *testing.T) {
	ownerRef := config.OwnerRef{
		Reference: &metav1.OwnerReference{
			APIVersion: "hypershift.openshift.io/v1beta1",
			Kind:       "HostedControlPlane",
			Name:       "test",
			UID:        "test-uid",
		},
	}

	testCases := []struct {
		name      string
		serviceCA string
	}{
		{
			name:      "When service CA is provided it should create a MachineConfig with the CA file",
			serviceCA: "-----BEGIN CERTIFICATE-----\nMIIDQTCCAimgAwIBAgI...\n-----END CERTIFICATE-----\n",
		},
		{
			name:      "When service CA is empty it should create a no-op MachineConfig",
			serviceCA: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ignition-config-image-registry-ca",
					Namespace: "test-namespace",
				},
			}

			err := ReconcileImageRegistryCAIgnitionConfig(cm, ownerRef, tc.serviceCA)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify the ConfigMap has the expected labels
			g.Expect(cm.Labels).To(HaveKeyWithValue("hypershift.openshift.io/core-ignition-config", "true"))

			// Verify the ConfigMap has config data
			configData, ok := cm.Data[ignitionConfigKey]
			g.Expect(ok).To(BeTrue())
			g.Expect(configData).ToNot(BeEmpty())

			// Parse the MachineConfig from the ConfigMap
			var mc mcfgv1.MachineConfig
			err = k8syaml.Unmarshal([]byte(configData), &mc)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify MachineConfig metadata
			g.Expect(mc.Name).To(Equal("99-worker-image-registry-ca"))
			g.Expect(mc.Labels).To(HaveKeyWithValue("machineconfiguration.openshift.io/role", "worker"))
			g.Expect(mc.APIVersion).To(Equal("machineconfiguration.openshift.io/v1"))
			g.Expect(mc.Kind).To(Equal("MachineConfig"))

			// Parse the ignition config from the MachineConfig
			var ignConfig igntypes.Config
			err = json.Unmarshal(mc.Spec.Config.Raw, &ignConfig)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(ignConfig.Ignition.Version).To(Equal("3.2.0"))

			if tc.serviceCA != "" {
				// When CA is provided, verify the file is present
				g.Expect(ignConfig.Storage.Files).To(HaveLen(1))
				g.Expect(ignConfig.Storage.Files[0].Path).To(Equal(imageRegistryCertPath))
				g.Expect(ignConfig.Storage.Files[0].Overwrite).ToNot(BeNil())
				g.Expect(*ignConfig.Storage.Files[0].Overwrite).To(BeTrue())
				g.Expect(ignConfig.Storage.Files[0].Mode).ToNot(BeNil())
				g.Expect(*ignConfig.Storage.Files[0].Mode).To(Equal(0644))
				// Verify the content source is set (data URL encoded)
				g.Expect(ignConfig.Storage.Files[0].Contents.Source).ToNot(BeNil())
				g.Expect(*ignConfig.Storage.Files[0].Contents.Source).To(ContainSubstring("data:"))
			} else {
				// When CA is empty, verify no files are present
				g.Expect(ignConfig.Storage.Files).To(BeEmpty())
			}
		})
	}
}
