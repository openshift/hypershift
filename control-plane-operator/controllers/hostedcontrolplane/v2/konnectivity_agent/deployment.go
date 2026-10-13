package konnectivity

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	ips := []string{
		fmt.Sprintf("ipv4=%s", cpContext.InfraStatus.OpenShiftAPIHost),
		fmt.Sprintf("ipv4=%s", cpContext.InfraStatus.PackageServerAPIAddress),
	}
	if util.HCPOAuthEnabled(cpContext.HCP) {
		ips = append(ips, fmt.Sprintf("ipv4=%s", cpContext.InfraStatus.OauthAPIServerHost))
	}

	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		if image, ok := cpContext.HCP.Annotations[hyperv1.KonnectivityAgentImageAnnotation]; ok {
			c.Image = image
		}

		c.Args = append(c.Args, "--agent-identifiers", strings.Join(ips, "&"))
	})

	return nil
}
