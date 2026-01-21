package gcp

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
	hcp := newTestHCP()
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

func TestConfigErrorStates(t *testing.T) {
	tests := []struct {
		name        string
		hcp         *hyperv1.HostedControlPlane
		expectedErr string
	}{
		{
			name: "nil GCP platform configuration",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						GCP: nil,
					},
				},
			},
			expectedErr: "GCP platform configuration is nil",
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
			if tt.expectedErr != "" && err.Error() != tt.expectedErr {
				t.Fatalf("expected error '%s', but got: %v", tt.expectedErr, err)
			}
		})
	}
}

func TestPredicate(t *testing.T) {
	tests := []struct {
		name           string
		platformType   hyperv1.PlatformType
		expectedResult bool
	}{
		{
			name:           "GCP platform returns true",
			platformType:   hyperv1.GCPPlatform,
			expectedResult: true,
		},
		{
			name:           "AWS platform returns false",
			platformType:   hyperv1.AWSPlatform,
			expectedResult: false,
		},
		{
			name:           "Azure platform returns false",
			platformType:   hyperv1.AzurePlatform,
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tt.platformType,
					},
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			result, err := predicate(cpContext)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result != tt.expectedResult {
				t.Errorf("expected predicate to return %v for platform %s, got %v",
					tt.expectedResult, tt.platformType, result)
			}
		})
	}
}

// newTestHCP creates a HostedControlPlane with default GCP configuration for testing.
func newTestHCP() *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "test-namespace",
			Annotations: map[string]string{},
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.GCPPlatform,
				GCP: &hyperv1.GCPPlatformSpec{
					Project: "my-project",
					Region:  "us-central1",
					NetworkConfig: hyperv1.GCPNetworkConfig{
						Network: hyperv1.GCPResourceReference{
							Name: "my-network",
						},
					},
					WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
						ProjectNumber: "123456789012",
						PoolID:        "my-pool",
						ProviderID:    "my-provider",
						ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
							NodePool:        "nodepool@my-project.iam.gserviceaccount.com",
							ControlPlane:    "controlplane@my-project.iam.gserviceaccount.com",
							CloudController: "cloud-controller@my-project.iam.gserviceaccount.com",
						},
					},
				},
			},
			InfraID: "my-infra-ID",
		},
	}
}
