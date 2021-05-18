package oapi

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

type OpenShiftAPIServerServiceParams struct {
	OwnerReference *metav1.OwnerReference `json:"ownerReference"`
}

func NewOpenShiftAPIServerServiceParams(hcp *hyperv1.HostedControlPlane) *OpenShiftAPIServerServiceParams {
	return &OpenShiftAPIServerServiceParams{
		OwnerReference: config.ControllerOwnerRef(hcp),
	}
}
