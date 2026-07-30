package storage

import (
	operatorv1 "github.com/openshift/api/operator/v1"
)

func ReconcileOperatorSpec(spec *operatorv1.OperatorSpec) {
	spec.LogLevel = operatorv1.Normal
	spec.OperatorLogLevel = operatorv1.Normal
	spec.ManagementState = operatorv1.Managed
}

func ReconcileCSISnapshotController(csi *operatorv1.CSISnapshotController) {
	ReconcileOperatorSpec(&csi.Spec.OperatorSpec)
}

func ReconcileStorage(storage *operatorv1.Storage) {
	ReconcileOperatorSpec(&storage.Spec.OperatorSpec)
}

func ReconcileClusterCSIDriver(driver *operatorv1.ClusterCSIDriver) {
	ReconcileOperatorSpec(&driver.Spec.OperatorSpec)
}

// ReconcileClusterCSIDriverKMSKey configures the KMS key ARN on the ClusterCSIDriver
// for AWS EBS encryption. This follows a write-once pattern: if the DriverConfig
// already has a KMS key set (either by a previous HCCO reconciliation or by a
// cluster administrator's day-2 change), it is not overwritten. This preserves
// in-cluster modifications, following the same principle as
// ReconcileDefaultIngressController.
func ReconcileClusterCSIDriverKMSKey(driver *operatorv1.ClusterCSIDriver, kmsKeyARN string) {
	if kmsKeyARN == "" {
		return
	}
	// Write-once: if DriverConfig already has a KMS key, don't overwrite.
	// This preserves day-2 admin changes to the key.
	if driver.Spec.DriverConfig.AWS != nil && driver.Spec.DriverConfig.AWS.KMSKeyARN != "" {
		return
	}
	driver.Spec.DriverConfig = operatorv1.CSIDriverConfigSpec{
		DriverType: operatorv1.AWSDriverType,
		AWS: &operatorv1.AWSCSIDriverConfigSpec{
			KMSKeyARN: kmsKeyARN,
		},
	}
}
