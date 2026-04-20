package aws

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestAdaptDeployment(t *testing.T) {
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
			g := NewGomegaWithT(t)

			deployment := &appsv1.Deployment{}
			_, _, err := assets.LoadManifestInto(ComponentName, "deployment.yaml", deployment)
			g.Expect(err).ToNot(HaveOccurred())

			cpContext := component.WorkloadContext{
				HCP: tt.hcp,
			}
			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// Check for trusted-ca volume
			hasVolume := false
			for _, v := range deployment.Spec.Template.Spec.Volumes {
				if v.Name == trustedCAVolumeName {
					hasVolume = true
					g.Expect(v.ConfigMap).ToNot(BeNil())
					g.Expect(v.ConfigMap.Name).To(Equal("user-ca-bundle"))
					g.Expect(v.ConfigMap.Items).To(HaveLen(1))
					g.Expect(v.ConfigMap.Items[0].Key).To(Equal(caBundleKey))
					g.Expect(v.ConfigMap.Items[0].Path).To(Equal(caBundlePath))
				}
			}
			g.Expect(hasVolume).To(Equal(tt.expectTrustBundleMount))

			// Check for volume mount and env var on the CCM container
			for _, c := range deployment.Spec.Template.Spec.Containers {
				if c.Name == containerName {
					hasMount := false
					for _, vm := range c.VolumeMounts {
						if vm.Name == trustedCAVolumeName {
							hasMount = true
							g.Expect(vm.MountPath).To(Equal(caDir))
							g.Expect(vm.ReadOnly).To(BeTrue())
						}
					}
					g.Expect(hasMount).To(Equal(tt.expectTrustBundleMount))

					hasEnv := false
					for _, env := range c.Env {
						if env.Name == "AWS_CA_BUNDLE" {
							hasEnv = true
							g.Expect(env.Value).To(Equal(caDir + "/" + caBundlePath))
						}
					}
					g.Expect(hasEnv).To(Equal(tt.expectAWSCABundleEnv))
				}
			}
		})
	}
}
