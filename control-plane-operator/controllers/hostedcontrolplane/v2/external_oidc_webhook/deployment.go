package extoidc

import (
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/netutil"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/proxy"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	profile := cpContext.HCP.Spec.Configuration.GetTLSSecurityProfile()
	tlsMinVersion, err := config.MinTLSVersion(profile)
	if err != nil {
		return err
	}
	cipherSuites, err := config.CipherSuites(profile)
	if err != nil {
		return err
	}

	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		if tlsMinVersion != "" {
			c.Args = append(c.Args, "--tls-min-version="+tlsMinVersion)
		}
		for _, cipherSuite := range cipherSuites {
			c.Args = append(c.Args, "--tls-cipher-suites="+cipherSuite)
		}

		// Match legacy KAS proxy settings, including hosted pod and service CIDR bypasses.
		hcp := cpContext.HCP
		var additionalNoProxyCIDRS []string
		additionalNoProxyCIDRS = append(additionalNoProxyCIDRS, netutil.ClusterCIDRs(hcp.Spec.Networking.ClusterNetwork)...)
		additionalNoProxyCIDRS = append(additionalNoProxyCIDRS, netutil.ServiceCIDRs(hcp.Spec.Networking.ServiceNetwork)...)
		proxy.SetEnvVars(&c.Env, additionalNoProxyCIDRS...)
	})

	return nil
}
