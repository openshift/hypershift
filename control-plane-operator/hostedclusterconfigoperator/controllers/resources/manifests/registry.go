package manifests

import (
	imageregistryv1 "github.com/openshift/api/imageregistry/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Registry() *imageregistryv1.Config {
	return &imageregistryv1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "cluster",
			Finalizers: []string{"imageregistry.operator.openshift.io/finalizer"},
		},
	}
}
