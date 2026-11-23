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

		hcp := cpContext.HCP
		var additionalNoProxyCIDRS []string
		additionalNoProxyCIDRS = append(additionalNoProxyCIDRS, netutil.ClusterCIDRs(hcp.Spec.Networking.ClusterNetwork)...)
		additionalNoProxyCIDRS = append(additionalNoProxyCIDRS, netutil.ServiceCIDRs(hcp.Spec.Networking.ServiceNetwork)...)

		// The auth-scoped component proxy takes precedence over the management
		// cluster proxy and is applied in full: nothing is inherited from the
		// management cluster's proxy environment. The bypass list keeps the
		// same internal baseline as the fallback leg: the hosted CIDRs above,
		// the hostname defaults, and the kube-apiserver Service.
		if authProxy := proxy.AuthProxyOverride(hcp); authProxy != nil {
			proxy.SetEnvVarsTo(&c.Env,
				authProxy.HTTPProxy,
				authProxy.HTTPSProxy,
				proxy.AuthProxyNoProxy(authProxy.NoProxy, additionalNoProxyCIDRS...),
			)
		} else {
			// Match legacy KAS proxy settings, including hosted pod and service CIDR bypasses.
			proxy.SetEnvVars(&c.Env, additionalNoProxyCIDRS...)
		}
	})

	return nil
}
