package cco

import (
	"fmt"

	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/proxy"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	tlsArgs, err := config.TLSArgs(cpContext.HCP.Spec.Configuration.GetTLSSecurityProfile())
	if err != nil {
		return fmt.Errorf("failed to resolve Cloud Credential Operator TLS arguments: %w", err)
	}

	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "RELEASE_VERSION",
			Value: cpContext.ReleaseImageProvider.Version(),
		})
		proxy.SetEnvVars(&c.Env)

		if len(tlsArgs) > 0 {
			c.Args = append(c.Args, tlsArgs...)
		}
	})
	return nil
}
