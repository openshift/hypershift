package oapi

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
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileOpenshiftAPIServerDeploymentTrustBundle(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "test",
		},
	}

	testCases := []struct {
		name                         string
		expectedVolume               *corev1.Volume
		additionalTrustBundle        *corev1.LocalObjectReference
		clusterConf                  *hyperv1.ClusterConfiguration
		imageRegistryAdditionalCAs   *corev1.ConfigMap
		expectProjectedVolumeMounted bool
	}{
		{
			name: "Trust bundle provided",
			additionalTrustBundle: &corev1.LocalObjectReference{
				Name: "user-ca-bundle",
			},
			expectedVolume: &corev1.Volume{
				Name: "additional-trust-bundle",
				VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{
						Sources:     []corev1.VolumeProjection{getFakeVolumeProjectionCABundle()},
						DefaultMode: ptr.To[int32](420),
					},
				},
			},
			expectProjectedVolumeMounted: true,
		},
		{
			name:                         "Trust bundle not provided",
			expectedVolume:               nil,
			additionalTrustBundle:        nil,
			expectProjectedVolumeMounted: false,
		},
		{
			name: "Trust bundle and image registry additional CAs provided",
			additionalTrustBundle: &corev1.LocalObjectReference{
				Name: "user-ca-bundle",
			},
			imageRegistryAdditionalCAs: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "image-registry-additional-ca",
					Namespace: hcp.Namespace,
				},
				Data: map[string]string{
					"registry1": "fake-bundle",
					"registry2": "fake-bundle-2",
				},
			},
			clusterConf: &hyperv1.ClusterConfiguration{
				Image: &configv1.ImageSpec{
					AdditionalTrustedCA: configv1.ConfigMapNameReference{
						Name: "image-registry-additional-ca",
					},
				},
			},
			expectedVolume: &corev1.Volume{
				Name: "additional-trust-bundle",
				VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{
						Sources:     []corev1.VolumeProjection{getFakeVolumeProjectionCABundle(), getFakeVolumeProjectionImageRegistryCAs()},
						DefaultMode: ptr.To[int32](420),
					},
				},
			},
			expectProjectedVolumeMounted: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			fakeClientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.imageRegistryAdditionalCAs != nil {
				fakeClientBuilder.WithObjects(tc.imageRegistryAdditionalCAs)
			}
			hcp.Spec.Configuration = tc.clusterConf
			hcp.Spec.AdditionalTrustBundle = tc.additionalTrustBundle
			cpContext := component.ControlPlaneContext{
				Client: fakeClientBuilder.Build(),
				HCP:    hcp,
			}

			oapiDeployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, oapiDeployment)
			g.Expect(err).ToNot(HaveOccurred())

			if tc.expectProjectedVolumeMounted {
				g.Expect(oapiDeployment.Spec.Template.Spec.Volumes).To(ContainElement(*tc.expectedVolume))
			} else {
				g.Expect(oapiDeployment.Spec.Template.Spec.Volumes).NotTo(ContainElement(&corev1.Volume{Name: "additional-trust-bundle"}))
			}
		})
	}
}

func getFakeVolumeProjectionCABundle() corev1.VolumeProjection {
	return corev1.VolumeProjection{
		ConfigMap: &corev1.ConfigMapProjection{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: "user-ca-bundle",
			},
			Items: []corev1.KeyToPath{
				{
					Key:  "ca-bundle.crt",
					Path: "additional-ca-bundle.pem",
				},
			},
		},
	}
}

func getFakeVolumeProjectionImageRegistryCAs() corev1.VolumeProjection {
	return corev1.VolumeProjection{
		ConfigMap: &corev1.ConfigMapProjection{
			LocalObjectReference: corev1.LocalObjectReference{
				Name: "image-registry-additional-ca",
			},
			Items: []corev1.KeyToPath{
				{
					Key:  "registry1",
					Path: "image-registry-1.pem",
				},
				{
					Key:  "registry2",
					Path: "image-registry-2.pem",
				},
			},
		},
	}
}
