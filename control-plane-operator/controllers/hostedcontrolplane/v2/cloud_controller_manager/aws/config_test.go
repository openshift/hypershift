package aws

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
	"k8s.io/utils/ptr"
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

// newTestHCP creates a HostedControlPlane with default AWS configuration for testing.
// Custom annotations can be provided to override defaults.
func newTestHCP(annotations map[string]string) *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "test-namespace",
			Annotations: annotations,
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				AWS: &hyperv1.AWSPlatformSpec{
					CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
						VPC:    "my-vpc",
						Subnet: &hyperv1.AWSResourceReference{ID: ptr.To("my-subnet-ID")},
						Zone:   "my-zone",
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
				hyperv1.AWSLoadBalancerHealthProbeModeAnnotation: "invalid-mode",
			}),
			expectedErr: "invalid value for annotation hypershift.openshift.io/aws-load-balancer-health-probe-mode: invalid-mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := &corev1.ConfigMap{}
			_, _, err := assets.LoadManifestInto(ComponentName, "config.yaml", cm)
			if err != nil {
				t.Fatalf("failed to load manifest: %v", err)
			}
			cpContext := component.WorkloadContext{
				HCP: tt.hcp,
			}
			err = adaptConfig(cpContext, cm)
			if err == nil {
				t.Fatalf("expected error but got none")
			}
			if tt.expectedErr != "" && !strings.Contains(err.Error(), tt.expectedErr) {
				t.Fatalf("expected error to contain %q, but got: %v", tt.expectedErr, err)
			}
		})
	}
}
