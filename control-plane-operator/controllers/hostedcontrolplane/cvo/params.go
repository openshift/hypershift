package cvo

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	k8sutilspointer "k8s.io/utils/pointer"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"

	"github.com/openshift/hypershift/support/config"
)

type CVOParams struct {
	Image            string
	CLIImage         string
	OwnerRef         config.OwnerRef
	DeploymentConfig config.DeploymentConfig
}

func NewCVOParams(hcp *hyperv1.HostedControlPlane, images map[string]string, explicitNonRootSecurityContext bool) *CVOParams {
	p := &CVOParams{
		CLIImage: images["cli"],
		Image:    hcp.Spec.ReleaseImage,
		OwnerRef: config.OwnerRefFrom(hcp),
	}
	p.DeploymentConfig.Resources = config.ResourcesSpec{
		cvoContainerPrepPayload().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("20Mi"),
				corev1.ResourceCPU:    resource.MustParse("10m"),
			},
		},
		cvoContainerMain().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("50Mi"),
				corev1.ResourceCPU:    resource.MustParse("20m"),
			},
		},
	}
	p.DeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	p.DeploymentConfig.SetColocation(hcp)
	p.DeploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	p.DeploymentConfig.SetControlPlaneIsolation(hcp)
	p.DeploymentConfig.Replicas = 1

	if explicitNonRootSecurityContext {
		// iterate over resources and set security context to all the containers
		securityContextsObj := make(config.SecurityContextSpec)
		for containerName := range p.DeploymentConfig.Resources {
			securityContextsObj[containerName] = corev1.SecurityContext{RunAsUser: k8sutilspointer.Int64Ptr(1001)}
		}
		p.DeploymentConfig.SecurityContexts = securityContextsObj
	}

	return p
}
