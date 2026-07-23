package ignitionserverproxy

import (
	"fmt"
	"strings"

	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/proxy"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	podspec.UpdateContainer("haproxy", deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		proxy.SetEnvVars(&c.Env)
	})

	hcp := cpContext.HCP
	if hcp.Spec.AdditionalTrustBundle != nil {
		// Add trusted-ca mount with optional configmap
		podspec.DeploymentAddTrustBundleVolume(hcp.Spec.AdditionalTrustBundle, deployment)
	}

	return nil
}

func tlsVersionToHAProxy(version string) (string, error) {
	switch version {
	case "VersionTLS13":
		return "TLSv1.3", nil
	case "VersionTLS12":
		return "TLSv1.2", nil
	case "VersionTLS11":
		return "TLSv1.1", nil
	case "VersionTLS10":
		return "TLSv1.0", nil
	default:
		return "", fmt.Errorf("unknown TLS version %q", version)
	}
}

func adaptHAProxyConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	profile := cpContext.HCP.Spec.Configuration.GetTLSSecurityProfile()
	// TODO(ingvagabund): crypto.DefaultTLSProfileType available in 4.23+
	if profile == nil {
		profile = &configv1.TLSSecurityProfile{
			Type: configv1.TLSProfileIntermediateType,
		}
	}

	// Skip config.CipherSuites invocation to keep the ciphers in OpenSSL
	// format to avoid translating them to IANA and back. HAProxy accepts OpenSSL format.
	var ciphers []string
	var minVersionStr string
	if profile.Type == configv1.TLSProfileCustomType {
		ciphers = profile.Custom.Ciphers
		minVersionStr = string(profile.Custom.MinTLSVersion)
	} else {
		ciphers = configv1.TLSProfiles[profile.Type].Ciphers
		minVersionStr = string(configv1.TLSProfiles[profile.Type].MinTLSVersion)
	}

	var minTLSVersion string
	if minVersionStr != "" {
		var err error
		minTLSVersion, err = tlsVersionToHAProxy(minVersionStr)
		if err != nil {
			return fmt.Errorf("failed to convert TLS version: %w", err)
		}
	}

	// Filter out TLS 1.3 ciphers (they start with "TLS_") - TLS 1.3 ciphers are not configurable in HAProxy
	var cipherStr string
	tls12Ciphers := []string{}
	for _, cipher := range ciphers {
		if !strings.HasPrefix(cipher, "TLS_") {
			tls12Ciphers = append(tls12Ciphers, cipher)
		}
	}
	if len(tls12Ciphers) > 0 {
		cipherStr = strings.Join(tls12Ciphers, ":")
	}

	bindOptions := "bind :::8443 v4v6 ssl crt /tmp/tls.pem"
	serverOptions := "server ignition-server ignition-server:443 check ssl ca-file /etc/ssl/root-ca/ca.crt"

	if minTLSVersion != "" {
		bindOptions += fmt.Sprintf(" ssl-min-ver %s", minTLSVersion)
		serverOptions += fmt.Sprintf(" ssl-min-ver %s", minTLSVersion)
	}
	if cipherStr != "" {
		bindOptions += fmt.Sprintf(" ciphers %s", cipherStr)
		serverOptions += fmt.Sprintf(" ciphers %s", cipherStr)
	}

	bindOptions += " alpn http/1.1"
	serverOptions += " alpn http/1.1"

	haproxyConf := fmt.Sprintf(`defaults
  mode http
  timeout connect 5s
  timeout client 30s
  timeout server 30s

frontend ignition-server
  %s
  default_backend ignition_servers

backend ignition_servers
  %s
`, bindOptions, serverOptions)

	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data["haproxy.conf"] = haproxyConf
	return nil
}
