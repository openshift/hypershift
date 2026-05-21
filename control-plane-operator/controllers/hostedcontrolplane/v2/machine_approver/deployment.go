package machineapprover

import (
	"fmt"
	"strings"

	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP
	configuration := hcp.Spec.Configuration
	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args, fmt.Sprintf("--machine-namespace=%s", hcp.Namespace))

		if tlsMinVersion := config.MinTLSVersion(configuration.GetTLSSecurityProfile()); tlsMinVersion != "" {
			c.Args = append(c.Args, fmt.Sprintf("--tls-min-version=%s", tlsMinVersion))
		}
		if cipherSuites := config.CipherSuites(configuration.GetTLSSecurityProfile()); len(cipherSuites) != 0 {
			c.Args = append(c.Args, fmt.Sprintf("--tls-cipher-suites=%s", strings.Join(cipherSuites, ",")))
		}
	})

	return nil
}
