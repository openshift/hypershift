package storage

import (
	operatorv1 "github.com/openshift/api/operator/v1"

	"github.com/go-logr/logr"
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

// ReconcileAWSEBSCSIDriverKMSKey configures the KMS key ARN on the AWS EBS
// ClusterCSIDriver for volume encryption. This follows a write-once pattern using
// CreationTimestamp to distinguish create from update:
//
//   - Create path (CreationTimestamp is zero): the resource does not yet exist.
//     The HCCO writes DriverConfig.AWS.KMSKeyARN if configured. If initialKMSKeyARN is
//     empty, DriverConfig is left unset.
//   - Update path (CreationTimestamp is non-zero): the resource already exists.
//     The HCCO skips DriverConfig entirely, preserving any in-cluster modifications
//     made by the administrator.
func ReconcileAWSEBSCSIDriverKMSKey(log logr.Logger, driver *operatorv1.ClusterCSIDriver, kmsKeyARN string) {
	// Nothing to configure when no key is set.
	if kmsKeyARN == "" {
		return
	}

	// Write-once: if the resource already exists, skip DriverConfig.
	if !driver.CreationTimestamp.IsZero() {
		log.V(4).Info("ClusterCSIDriver already exists; skipping write-once initialKMSKeyARN. Day-2 key changes must be made directly on the ClusterCSIDriver in the hosted cluster.",
			"clusterCSIDriver", driver.Name)
		return
	}

	driver.Spec.DriverConfig = operatorv1.CSIDriverConfigSpec{
		DriverType: operatorv1.AWSDriverType,
		AWS: &operatorv1.AWSCSIDriverConfigSpec{
			KMSKeyARN: kmsKeyARN,
		},
	}
}
