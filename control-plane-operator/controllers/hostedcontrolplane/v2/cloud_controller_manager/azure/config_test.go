package azure

import (
	"strings"
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
	hcp := newTestHCP(map[string]string{
		hyperv1.SharedLoadBalancerHealthProbePathAnnotation: "/healthz",
		hyperv1.SharedLoadBalancerHealthProbePortAnnotation: "10256",
	})
	hcp.Namespace = "HCP_NAMESPACE"

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

// newTestHCP creates a HostedControlPlane with default Azure configuration for testing.
// Custom annotations can be provided to override defaults.
func newTestHCP(annotations map[string]string) *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "test-namespace",
			Annotations: annotations,
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
}

func TestConfigErrorStates(t *testing.T) {
	tests := []struct {
		name        string
		hcp         *hyperv1.HostedControlPlane
		expectedErr string
	}{
		{
			name: "invalid load balancer health probe mode",
			hcp: newTestHCP(map[string]string{
				hyperv1.AzureLoadBalancerHealthProbeModeAnnotation: "invalid-mode",
			}),
			expectedErr: "invalid value for annotation hypershift.openshift.io/azure-load-balancer-health-probe-mode: invalid-mode",
		},
		{
			name: "invalid health probe port - non-numeric",
			hcp: newTestHCP(map[string]string{
				// This annotation is common to AWS and Azure. Test it only here.
				hyperv1.SharedLoadBalancerHealthProbePortAnnotation: "not-a-number",
			}),
			expectedErr: "invalid value for annotation hypershift.openshift.io/shared-load-balancer-health-probe-port: not-a-number (must be a valid port number)",
		},
		{
			name: "invalid health probe port - out of range (too low)",
			hcp: newTestHCP(map[string]string{
				// This annotation is common to AWS and Azure. Test it only here.
				hyperv1.SharedLoadBalancerHealthProbePortAnnotation: "0",
			}),
			expectedErr: "invalid value for annotation hypershift.openshift.io/shared-load-balancer-health-probe-port: 0 (must be between 1 and 65535)",
		},
		{
			name: "invalid health probe port - out of range (too high)",
			hcp: newTestHCP(map[string]string{
				// This annotation is common to AWS and Azure. Test it only here.
				hyperv1.SharedLoadBalancerHealthProbePortAnnotation: "65536",
			}),
			expectedErr: "invalid value for annotation hypershift.openshift.io/shared-load-balancer-health-probe-port: 65536 (must be between 1 and 65535)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := &corev1.ConfigMap{
				Data: map[string]string{},
			}
			cpContext := component.WorkloadContext{
				HCP: tt.hcp,
			}
			err := adaptConfig(cpContext, cm)
			if err == nil {
				t.Fatalf("expected error but got none")
			}
			if tt.expectedErr != "" && !strings.Contains(err.Error(), tt.expectedErr) {
				t.Fatalf("expected error to contain %q, but got: %v", tt.expectedErr, err)
			}
		})
	}
}
