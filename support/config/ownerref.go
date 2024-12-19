package config

import (
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

type OwnerRef struct {
	Reference *metav1.OwnerReference
}

func (c OwnerRef) ApplyTo(obj client.Object) {
	util.EnsureOwnerRef(obj, c.Reference)
}

func OwnerRefFrom(obj client.Object) OwnerRef {
	return OwnerRef{
		Reference: ControllerOwnerRef(obj),
	}
}

func ControllerOwnerRef(obj client.Object) *metav1.OwnerReference {
	gvk, err := apiutil.GVKForObject(obj, api.Scheme)
	if err != nil {
		return nil
	}
	return metav1.NewControllerRef(obj, gvk)
}
