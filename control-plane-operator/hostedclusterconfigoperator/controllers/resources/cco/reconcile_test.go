package cco

import (
	"testing"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"

	operatorv1 "github.com/openshift/api/operator/v1"

	"github.com/google/go-cmp/cmp"
)

func TestReconcileCloudCredentialConfig(t *testing.T) {
	testsCases := []struct {
		name           string
		inputConfig    *operatorv1.CloudCredential
		expectedConfig *operatorv1.CloudCredential
	}{
		{
			name:        "create",
			inputConfig: manifests.CloudCredential(),
			expectedConfig: &operatorv1.CloudCredential{
				ObjectMeta: manifests.CloudCredential().ObjectMeta,
				Spec: operatorv1.CloudCredentialSpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: operatorv1.Managed,
					},
					CredentialsMode: operatorv1.CloudCredentialsModeManual,
				},
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			config := tc.inputConfig
			ReconcileCloudCredentialConfig(config)
			if diff := cmp.Diff(config, tc.expectedConfig); diff != "" {
				t.Errorf("invalid reconciled config: %v", diff)
			}
		})
	}
}
