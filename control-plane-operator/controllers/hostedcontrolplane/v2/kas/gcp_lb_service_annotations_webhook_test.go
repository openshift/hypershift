package kas

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func TestApplyGCPLBServiceAnnotationsWebhookContainer(t *testing.T) {
	g := NewWithT(t)
	podSpec := &corev1.PodSpec{}
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.GCPPlatform,
				GCP: &hyperv1.GCPPlatformSpec{ResourceLabels: []hyperv1.GCPResourceLabel{
					{Key: "goog-partner-solution", Value: ptr.To("openshift")},
					{Key: "env", Value: ptr.To("prod")},
				}},
			},
		},
	}

	applyGCPLBServiceAnnotationsWebhookContainer(podSpec, hcp)

	container := findContainerByNameInPod(podSpec, "gcp-lb-service-annotations-webhook")
	g.Expect(container).NotTo(BeNil())
	g.Expect(container.Image).To(Equal("controlplane-operator"))
	g.Expect(container.Command).To(Equal([]string{
		"/usr/bin/control-plane-operator",
		"gcp-lb-service-annotations-webhook",
		"--labels=env=prod,goog-partner-solution=openshift",
		"--port=8443",
		"--tls-cert=/var/run/app/certs/tls.crt",
		"--tls-key=/var/run/app/certs/tls.key",
	}))
	g.Expect(container.LivenessProbe).To(BeNil())
	g.Expect(container.ReadinessProbe).To(BeNil())
	g.Expect(container.VolumeMounts).To(ConsistOf(corev1.VolumeMount{
		Name:      gcpLBServiceAnnotationsWebhookServingCertVolumeName,
		MountPath: "/var/run/app/certs",
	}))
	g.Expect(podSpec.Volumes).To(ConsistOf(corev1.Volume{
		Name: gcpLBServiceAnnotationsWebhookServingCertVolumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: manifests.GCPLBServiceAnnotationsWebhookServingCert("").Name},
		},
	}))
}
