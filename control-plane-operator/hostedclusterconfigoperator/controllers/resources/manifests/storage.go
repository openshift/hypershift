package manifests

import (
	operatorv1 "github.com/openshift/api/operator/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CSISnapshotController() *operatorv1.CSISnapshotController {
	return &operatorv1.CSISnapshotController{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func Storage() *operatorv1.Storage {
	return &operatorv1.Storage{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ClusterCSIDriver(name operatorv1.CSIDriverName) *operatorv1.ClusterCSIDriver {
	return &operatorv1.ClusterCSIDriver{
		ObjectMeta: metav1.ObjectMeta{
			Name: string(name),
		},
	}
}
