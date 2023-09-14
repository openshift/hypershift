package globalconfig

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CloudCredentialsConfiguration() *operatorv1.CloudCredential {
	return &operatorv1.CloudCredential{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ReconcileCloudCredentialsConfiguration(cfg *operatorv1.CloudCredential) error {
	cfg.Spec.CredentialsMode = operatorv1.CloudCredentialsModeManual

	// Because we don't run the CCO, setting the management state to unmanaged.
	// This should change if/when we run the CCO on the control plane side.
	cfg.Spec.ManagementState = operatorv1.Unmanaged
	return nil
}
