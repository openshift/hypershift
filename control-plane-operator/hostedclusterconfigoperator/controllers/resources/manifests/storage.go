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
