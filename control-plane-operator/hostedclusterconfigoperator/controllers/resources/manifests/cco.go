package manifests

import (
	operatorv1 "github.com/openshift/api/operator/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CloudCredential() *operatorv1.CloudCredential {
	return &operatorv1.CloudCredential{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}
