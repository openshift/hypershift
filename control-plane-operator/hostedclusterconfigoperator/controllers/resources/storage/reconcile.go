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
// for AWS EBS encryption. This follows a write-once pattern using
// CreationTimestamp to distinguish create from update:
//
//   - Create path (CreationTimestamp is zero): the resource does not yet exist.
//     The HCCO writes DriverConfig.AWS.KMSKeyARN if configured. If initialKMSKeyARN is
//     empty, DriverConfig is left unset.
//   - Update path (CreationTimestamp is non-zero): the resource already exists.
//     The HCCO skips DriverConfig entirely, preserving any in-cluster modifications
//     made by the administrator.
//
// CreationTimestamp is a server-set field that cannot be modified or deleted by users,
// making this guard tamper-proof unlike an annotation-based approach.
func ReconcileClusterCSIDriverKMSKey(driver *operatorv1.ClusterCSIDriver, kmsKeyARN string) {
	// Write-once: if the resource already exists, skip DriverConfig.
	if !driver.CreationTimestamp.IsZero() {
		return
	}

	// Create path: write the KMS key only if configured.
	if kmsKeyARN == "" {
		return
	}
	driver.Spec.DriverConfig = operatorv1.CSIDriverConfigSpec{
		DriverType: operatorv1.AWSDriverType,
		AWS: &operatorv1.AWSCSIDriverConfigSpec{
			KMSKeyARN: kmsKeyARN,
		},
	}
}
