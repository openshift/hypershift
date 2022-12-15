package config

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	"github.com/blang/semver"
	hyperv1alpha1 "github.com/openshift/hypershift/api/v1alpha1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/util"
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

// MutatingOwnerRefFromHCP returns ownerRef with altered API version based on OCP release version
func MutatingOwnerRefFromHCP(hcp *hyperv1.HostedControlPlane, version semver.Version) OwnerRef {
	ownerRef := OwnerRefFrom(hcp)
	if version.Major == 4 && version.Minor < 12 {
		ownerRef.Reference.APIVersion = hyperv1alpha1.GroupVersion.String()
	}
	return ownerRef
}
