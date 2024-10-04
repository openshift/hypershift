package globalconfig

import (
	"fmt"
	"testing"

	"github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func TestApplyNamedCertificateMounts(t *testing.T) {
	inputMountPrefix := "/etc/certs/named"
	inputContainerName := "container-1"
	testsCases := []struct {
		name                 string
		inputServingCerts    []configv1.APIServerNamedServingCert
		inputPodSpec         *corev1.PodSpec
		expectedVolumeMounts []corev1.VolumeMount
		expectedVolumes      []corev1.Volume
	}{
		{
			name: "APIServerNamedServingCerts volume mounts and volumes are added appropriately",
			inputServingCerts: []configv1.APIServerNamedServingCert{
				{
					Names: []string{"*.example.com"},
					ServingCertificate: configv1.SecretNameReference{
						Name: "example-cert",
					},
				},
			},
			inputPodSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: inputContainerName,
					},
				},
			},
			expectedVolumeMounts: []corev1.VolumeMount{
				{
					Name:      "named-cert-1",
					MountPath: fmt.Sprintf("%s-%d", inputMountPrefix, 1),
				},
			},
			expectedVolumes: []corev1.Volume{
				{
					Name: "named-cert-1",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName:  "example-cert",
							DefaultMode: ptr.To[int32](0640),
						},
					},
				},
			},
		},
		{
			name:              "Empty APIServerNamedServingCerts do not add additional volumes",
			inputServingCerts: []configv1.APIServerNamedServingCert{},
			inputPodSpec: &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name: inputContainerName,
					},
				},
			},
			expectedVolumeMounts: nil,
			expectedVolumes:      nil,
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := gomega.NewGomegaWithT(t)
			ApplyNamedCertificateMounts(inputContainerName, inputMountPrefix, tc.inputServingCerts, tc.inputPodSpec)
			g.Expect(tc.inputPodSpec.Volumes).To(gomega.BeEquivalentTo(tc.expectedVolumes))
			g.Expect(tc.inputPodSpec.Containers[0].VolumeMounts).To(gomega.BeEquivalentTo(tc.expectedVolumeMounts))
		})
	}
}

func TestGetConfigNamedCertificates(t *testing.T) {
	inputMountPrefix := "/etc/certs/named"
	testsCases := []struct {
		name              string
		inputServingCerts []configv1.APIServerNamedServingCert
		expectedOutput    []configv1.NamedCertificate
	}{
		{
			name:              "Empty array returned when no named certificates specified",
			inputServingCerts: []configv1.APIServerNamedServingCert{},
			expectedOutput:    []configv1.NamedCertificate{},
		},
		{
			name: "APIServerNamedServingCerts are serialized appropriately",
			inputServingCerts: []configv1.APIServerNamedServingCert{
				{
					Names: []string{"*.example.com"},
					ServingCertificate: configv1.SecretNameReference{
						Name: "example-cert",
					},
				},
			},
			expectedOutput: []configv1.NamedCertificate{
				{
					Names: []string{"*.example.com"},
					CertInfo: configv1.CertInfo{
						CertFile: fmt.Sprintf("%s-%d/%s", inputMountPrefix, 1, corev1.TLSCertKey),
						KeyFile:  fmt.Sprintf("%s-%d/%s", inputMountPrefix, 1, corev1.TLSPrivateKeyKey),
					},
				},
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := gomega.NewGomegaWithT(t)
			outputNamedCerts := GetConfigNamedCertificates(tc.inputServingCerts, inputMountPrefix)
			g.Expect(outputNamedCerts).To(gomega.BeEquivalentTo(tc.expectedOutput))
		})
	}
}
