package kas

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type KubeAPIServerServiceParams struct {
	AllowedCIDRBlocks []string
	OwnerReference    *metav1.OwnerReference
}

const (
	KonnectivityHealthPort      = 2041
	KonnectivityServerLocalPort = 8090
	KonnectivityServerPort      = 8091
)

func NewKubeAPIServerServiceParams(hcp *hyperv1.HostedControlPlane) *KubeAPIServerServiceParams {
	var allowedCIDRBlocks []string
	for _, block := range util.AllowedCIDRBlocks(hcp) {
		allowedCIDRBlocks = append(allowedCIDRBlocks, string(block))
	}
	return &KubeAPIServerServiceParams{
		AllowedCIDRBlocks: allowedCIDRBlocks,
		OwnerReference:    config.ControllerOwnerRef(hcp),
	}
}
