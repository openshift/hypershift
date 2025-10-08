package azure

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestConfig(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "HCP_NAMESPACE",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AzurePlatform,
				Azure: &hyperv1.AzurePlatformSpec{
					Cloud:             "AzurePublicCloud",
					TenantID:          "my-tenant-id",
					SubscriptionID:    "my-subscription-id",
					Location:          "eastus",
					ResourceGroupName: "my-resource-group",
					VnetID:            "/subscriptions/my-subscription-id/resourceGroups/my-vnet-rg/providers/Microsoft.Network/virtualNetworks/my-vnet",
					SubnetID:          "/subscriptions/my-subscription-id/resourceGroups/my-subnet-rg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/my-subnet",
					SecurityGroupID:   "/subscriptions/my-subscription-id/resourceGroups/my-sg-rg/providers/Microsoft.Network/networkSecurityGroups/my-security-group",
					AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
						AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeWorkloadIdentities,
						WorkloadIdentities: &hyperv1.AzureWorkloadIdentities{
							CloudProvider: hyperv1.WorkloadIdentity{
								ClientID: "my-client-id",
							},
						},
					},
				},
			},
			InfraID: "my-infra-ID",
		},
	}

	cm := &corev1.ConfigMap{}
	_, _, err := assets.LoadManifestInto(ComponentName, "config.yaml", cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cpContext := component.WorkloadContext{
		HCP: hcp,
	}
	err = adaptConfig(cpContext, cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	yaml, err := util.SerializeResource(cm, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, yaml)
}
