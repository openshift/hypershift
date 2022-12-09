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
