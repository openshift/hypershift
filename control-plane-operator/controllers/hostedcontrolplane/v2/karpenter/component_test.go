package karpenter

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPredicate(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "hcp-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
		},
	}

	testCases := []struct {
		name                 string
		capiKubeconfigSecret client.Object

		expected bool
	}{
		{
			name:                 "when CAPI kubeconfig secret exist predicate returns true",
			capiKubeconfigSecret: manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID),
			expected:             true,
		},
		{
			name:     "when CAPI kubeconfig secret doesn't exist, predicate return false",
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.capiKubeconfigSecret != nil {
				clientBuilder = clientBuilder.WithObjects(tc.capiKubeconfigSecret)
				hcp.Status.KubeConfig = &hyperv1.KubeconfigSecretRef{
					Name: tc.capiKubeconfigSecret.GetName(),
				}
			}
			client := clientBuilder.Build()

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				Client:  client,
				HCP:     hcp,
			}

			g := NewGomegaWithT(t)

			result, err := predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestAdaptDeployment(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "hcp-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
			Platform: hyperv1.PlatformSpec{
				AWS: &hyperv1.AWSPlatformSpec{
					Region: "test-region",
				},
			},
		},
	}

	testCases := []struct {
		name           string
		hcpAnnotations map[string]string

		expectedImage string
	}{
		{
			name: "when HCP has KarpenterProviderAWSImage annotation, image should be overridden",
			hcpAnnotations: map[string]string{
				hyperkarpenterv1.KarpenterProviderAWSImage: "some-override-karpenter-image",
			},
			expectedImage: "some-override-karpenter-image",
		},
		{
			name:          "expect default image",
			expectedImage: "aws-karpenter-provider-aws",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp.Annotations = tc.hcpAnnotations

			cpContext := controlplanecomponent.WorkloadContext{
				HCP: hcp,
			}

			g := NewGomegaWithT(t)

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// verify the adapted deployment has expected fields
			g.Expect(deployment.Spec.Template.Spec.Volumes).To(ContainElement(
				WithTransform(func(vol corev1.Volume) volumeKey {
					secretName := ""
					if vol.Secret != nil {
						secretName = vol.Secret.SecretName
					}
					return volumeKey{
						Name:       kubeconfigVolumeName,
						SecretName: secretName,
					}
				}, Equal(volumeKey{
					Name:       kubeconfigVolumeName,
					SecretName: manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID).Name,
				})),
			))
			g.Expect(deployment.Spec.Template.Spec.Containers[0].Env).To(ContainElements(
				corev1.EnvVar{
					Name:  "AWS_REGION",
					Value: hcp.Spec.Platform.AWS.Region,
				},
				corev1.EnvVar{
					Name:  "CLUSTER_NAME",
					Value: hcp.Spec.InfraID,
				},
			))

			// expect correct image
			g.Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal(tc.expectedImage))
		})
	}
}

type volumeKey struct {
	Name       string
	SecretName string
}
