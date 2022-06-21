package platform

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/ccm/platform/kubevirt"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
)

type Platform interface {
	AddPlatfomVolumes(deployment *appsv1.Deployment)
	GetContainerImage() string
	GetContainerCommand() []string
	GetContainerArgs() []string
	GetPodVolumeMounts() util.PodVolumeMounts
}

func GetPlatform(hcp *hyperv1.HostedControlPlane) Platform {
	var platform Platform
	switch hcp.Spec.Platform.Type {
	case hyperv1.KubevirtPlatform:
		platform = kubevirt.NewKubevirtPlatform(hcp)
	}
	return platform
}
