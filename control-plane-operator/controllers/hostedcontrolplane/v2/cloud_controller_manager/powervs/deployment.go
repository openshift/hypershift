package powervs

import (
	"fmt"
	"strings"

	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	cloudCredsVolumeName = "cloud-creds"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP
	if hcp.Spec.Platform.PowerVS == nil {
		return fmt.Errorf(".spec.platform.powervs is not defined")
	}

	podspec.UpdateContainer("cloud-controller-manager", deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		// Add TLS configuration based on cluster TLS security profile
		if tlsMinVersion := config.MinTLSVersion(hcp.Spec.Configuration.GetTLSSecurityProfile()); tlsMinVersion != "" {
			c.Args = append(c.Args, fmt.Sprintf("--tls-min-version=%s", tlsMinVersion))
		}
		if cipherSuites := config.CipherSuites(hcp.Spec.Configuration.GetTLSSecurityProfile()); len(cipherSuites) != 0 {
			c.Args = append(c.Args, fmt.Sprintf("--tls-cipher-suites=%s", strings.Join(cipherSuites, ",")))
		}
	})

	podspec.UpdateVolume(cloudCredsVolumeName, deployment.Spec.Template.Spec.Volumes, func(v *corev1.Volume) {
		v.Secret.SecretName = hcp.Spec.Platform.PowerVS.KubeCloudControllerCreds.Name
	})
	return nil
}
