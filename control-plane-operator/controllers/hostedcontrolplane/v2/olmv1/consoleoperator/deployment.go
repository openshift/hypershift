package consoleoperator

import (
	"strings"

	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	// Configure proxy environment variables for catalogd access through Konnectivity tunnel
	// The Konnectivity sidecar provides a SOCKS5 proxy on 127.0.0.1:8090
	socks5ProxyURL := "socks5://127.0.0.1:8090"

	// NO_PROXY excludes services that should use direct access (not through tunnel)
	// - kube-apiserver: console-operator uses in-cluster config for hosted cluster API
	// - Other OpenShift services in the same cluster
	noProxy := []string{
		"kube-apiserver",
		".svc",
		".svc.cluster.local",
		"localhost",
		"127.0.0.1",
	}

	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		podspec.UpsertEnvVars(c, []corev1.EnvVar{
			{Name: "HTTP_PROXY", Value: socks5ProxyURL},
			{Name: "HTTPS_PROXY", Value: socks5ProxyURL},
			{Name: "NO_PROXY", Value: strings.Join(noProxy, ",")},
			{Name: "RELEASE_VERSION", Value: cpContext.UserReleaseImageProvider.Version()},
		})
	})

	return nil
}
