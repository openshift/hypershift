package aws

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                   string
		hcp                    *hyperv1.HostedControlPlane
		expectTrustBundleMount bool
		expectAWSCABundleEnv   bool
	}{
		{
			name: "When AdditionalTrustBundle is set, it should add trusted CA volume and AWS_CA_BUNDLE env var",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "us-east-1",
							CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
								VPC:    "my-vpc",
								Subnet: &hyperv1.AWSResourceReference{ID: ptr.To("my-subnet-ID")},
								Zone:   "my-zone",
							},
						},
					},
					InfraID: "test-infra",
					AdditionalTrustBundle: &corev1.LocalObjectReference{
						Name: "user-ca-bundle",
					},
				},
			},
			expectTrustBundleMount: true,
			expectAWSCABundleEnv:   true,
		},
		{
			name: "When AdditionalTrustBundle is not set, it should not modify the deployment",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "us-east-1",
							CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
								VPC:    "my-vpc",
								Subnet: &hyperv1.AWSResourceReference{ID: ptr.To("my-subnet-ID")},
								Zone:   "my-zone",
							},
						},
					},
					InfraID: "test-infra",
				},
			},
			expectTrustBundleMount: false,
			expectAWSCABundleEnv:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)

			deployment := &appsv1.Deployment{}
			_, _, err := assets.LoadManifestInto(ComponentName, "deployment.yaml", deployment)
			g.Expect(err).ToNot(HaveOccurred())

			cpContext := component.WorkloadContext{
				HCP: tt.hcp,
			}
			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify trusted-ca volume
			if tt.expectTrustBundleMount {
				g.Expect(deployment.Spec.Template.Spec.Volumes).To(ContainElement(SatisfyAll(
					HaveField("Name", trustedCAVolumeName),
					HaveField("VolumeSource.ConfigMap.Name", cpomanifests.UserCAConfigMap("test-namespace").Name),
					HaveField("VolumeSource.ConfigMap.Items", ConsistOf(corev1.KeyToPath{Key: caBundleKey, Path: caBundlePath})),
					HaveField("VolumeSource.ConfigMap.Optional", ptr.To(true)),
				)))
			} else {
				g.Expect(deployment.Spec.Template.Spec.Volumes).ToNot(
					ContainElement(HaveField("Name", trustedCAVolumeName)),
				)
			}

			// Find the CCM container
			var ccmContainer *corev1.Container
			for i := range deployment.Spec.Template.Spec.Containers {
				if deployment.Spec.Template.Spec.Containers[i].Name == containerName {
					ccmContainer = &deployment.Spec.Template.Spec.Containers[i]
					break
				}
			}
			g.Expect(ccmContainer).ToNot(BeNil(), "expected CCM container to exist")

			// Verify volume mount on the CCM container
			if tt.expectTrustBundleMount {
				g.Expect(ccmContainer.VolumeMounts).To(ContainElement(SatisfyAll(
					HaveField("Name", trustedCAVolumeName),
					HaveField("MountPath", caDir),
					HaveField("ReadOnly", true),
				)))
			} else {
				g.Expect(ccmContainer.VolumeMounts).ToNot(
					ContainElement(HaveField("Name", trustedCAVolumeName)),
				)
			}

			// Verify AWS_CA_BUNDLE env var on the CCM container
			if tt.expectAWSCABundleEnv {
				g.Expect(ccmContainer.Env).To(ContainElement(SatisfyAll(
					HaveField("Name", "AWS_CA_BUNDLE"),
					HaveField("Value", caDir+"/"+caBundlePath),
				)))
			} else {
				g.Expect(ccmContainer.Env).ToNot(
					ContainElement(HaveField("Name", "AWS_CA_BUNDLE")),
				)
			}
		})
	}
}
