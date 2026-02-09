package globalconfig

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileInfrastructure_AWSCloudConfig(t *testing.T) {
	tests := map[string]struct {
		hcp                     *hyperv1.HostedControlPlane
		expectedCloudConfigName string
		expectedCloudConfigKey  string
	}{
		"When reconciling AWS infrastructure it should set CloudConfig name and key": {
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster1",
					Namespace: "master-cluster1",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "us-east-1",
						},
					},
					InfraID: "test-infra-id",
				},
				Status: hyperv1.HostedControlPlaneStatus{
					ControlPlaneEndpoint: hyperv1.APIEndpoint{
						Host: "api.example.com",
						Port: 6443,
					},
				},
			},
			expectedCloudConfigName: "cloud-provider-config",
			expectedCloudConfigKey:  "config",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			infra := InfrastructureConfig()
			ReconcileInfrastructure(infra, test.hcp)

			if infra.Spec.CloudConfig.Name != test.expectedCloudConfigName {
				t.Errorf("expected CloudConfig.Name = %q, got %q", test.expectedCloudConfigName, infra.Spec.CloudConfig.Name)
			}
			if infra.Spec.CloudConfig.Key != test.expectedCloudConfigKey {
				t.Errorf("expected CloudConfig.Key = %q, got %q", test.expectedCloudConfigKey, infra.Spec.CloudConfig.Key)
			}

			if infra.Status.PlatformStatus == nil || infra.Status.PlatformStatus.AWS == nil {
				t.Fatal("expected AWS PlatformStatus to be set")
			}
			if infra.Status.PlatformStatus.AWS.Region != "us-east-1" {
				t.Errorf("expected AWS region = %q, got %q", "us-east-1", infra.Status.PlatformStatus.AWS.Region)
			}
			if infra.Spec.PlatformSpec.Type != configv1.AWSPlatformType {
				t.Errorf("expected PlatformSpec.Type = %q, got %q", configv1.AWSPlatformType, infra.Spec.PlatformSpec.Type)
			}
		})
	}
}
