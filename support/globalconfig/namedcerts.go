package globalconfig

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func ApplyNamedCertificateMounts(containerName string, mountPrefix string, certs []configv1.APIServerNamedServingCert, spec *corev1.PodSpec) {
	var container *corev1.Container
	for i := range spec.Containers {
		if spec.Containers[i].Name == containerName {
			container = &spec.Containers[i]
			break
		}
	}
	if container == nil {
		panic("oauth container not found")
	}
	for i, namedCert := range certs {
		volumeName := fmt.Sprintf("named-cert-%d", i+1)
		spec.Volumes = append(spec.Volumes, corev1.Volume{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  namedCert.ServingCertificate.Name,
					DefaultMode: ptr.To[int32](0640),
				},
			},
		})
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      volumeName,
			MountPath: fmt.Sprintf("%s-%d", mountPrefix, i+1),
		})
	}
}

func GetConfigNamedCertificates(servingCerts []configv1.APIServerNamedServingCert, mountPathPrefix string) []configv1.NamedCertificate {
	result := []configv1.NamedCertificate{}
	for i, cert := range servingCerts {
		result = append(result, configv1.NamedCertificate{
			Names: cert.Names,
			CertInfo: configv1.CertInfo{
				CertFile: fmt.Sprintf("%s-%d/%s", mountPathPrefix, i+1, corev1.TLSCertKey),
				KeyFile:  fmt.Sprintf("%s-%d/%s", mountPathPrefix, i+1, corev1.TLSPrivateKeyKey),
			},
		})
	}
	return result
}
