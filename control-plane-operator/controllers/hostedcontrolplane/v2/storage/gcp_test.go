package storage

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptGCPPDCSIConfig(t *testing.T) {
	t.Parallel()

	t.Run("When GCP platform is configured it should populate the project ID", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		cm := &corev1.ConfigMap{}
		_, _, err := assets.LoadManifestInto(ComponentName, "gcp-pd-csi-config.yaml", cm)
		g.Expect(err).ToNot(HaveOccurred())

		hcp := &hyperv1.HostedControlPlane{
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.GCPPlatform,
					GCP: &hyperv1.GCPPlatformSpec{
						Project: "my-tenant-project",
					},
				},
			},
		}

		err = adaptGCPPDCSIConfig(component.WorkloadContext{HCP: hcp}, cm)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cm.Data[gcpPDCloudConfigKey]).To(Equal("[Global]\nproject-id = my-tenant-project\n"))
	})

	t.Run("When GCP platform configuration is nil it should error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "gcp-pd-cloud-config"},
			Data:       map[string]string{gcpPDCloudConfigKey: ""},
		}

		hcp := &hyperv1.HostedControlPlane{
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.GCPPlatform,
					GCP:  nil,
				},
			},
		}

		err := adaptGCPPDCSIConfig(component.WorkloadContext{HCP: hcp}, cm)
		g.Expect(err).To(HaveOccurred())
	})
}
