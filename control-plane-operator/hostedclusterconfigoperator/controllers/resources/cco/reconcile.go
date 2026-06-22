package cco

import (
	operatorv1 "github.com/openshift/api/operator/v1"
)

func ReconcileCloudCredentialConfig(cfg *operatorv1.CloudCredential) {
	if cfg.Spec.ManagementState == "" {
		cfg.Spec.ManagementState = operatorv1.Managed
	}

	cfg.Spec.CredentialsMode = operatorv1.CloudCredentialsModeManual
}
