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
