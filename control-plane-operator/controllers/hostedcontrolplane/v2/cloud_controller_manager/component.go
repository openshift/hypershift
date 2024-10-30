package cloudcontrollermanager

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/kubevirt"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/openstack"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/powervs"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
)

const (
	ComponentName = "cloud-controller-manager"
)

func NewComponent(platform hyperv1.PlatformType) component.ControlPlaneComponent {
	var builder *component.ControlPlaneWorkloadBuilder[*appsv1.Deployment]
	switch platform {
	case hyperv1.AWSPlatform:
		builder = aws.NewComponentBuilder(ComponentName)
	case hyperv1.AzurePlatform:
		builder = azure.NewComponentBuilder(ComponentName)
	case hyperv1.OpenStackPlatform:
		builder = openstack.NewComponentBuilder(ComponentName)
	case hyperv1.PowerVSPlatform:
		builder = powervs.NewComponentBuilder(ComponentName)
	case hyperv1.KubevirtPlatform:
		builder = kubevirt.NewComponentBuilder(ComponentName)
	default:
		panic(fmt.Sprintf("unrecognized platform %s", platform))
	}

	return builder.Build()
}
