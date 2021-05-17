package config

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	"github.com/openshift/hypershift/api"
)

func ControllerOwnerRef(obj client.Object) *metav1.OwnerReference {
	gvk, err := apiutil.GVKForObject(obj, api.Scheme)
	if err != nil {
		return nil
	}
	return metav1.NewControllerRef(obj, gvk)
}
