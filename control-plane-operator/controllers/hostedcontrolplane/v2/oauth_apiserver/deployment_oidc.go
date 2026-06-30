package oapi

import (
	"fmt"
	"strings"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func adaptForExternalOIDC(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	configuration := cpContext.HCP.Spec.Configuration

	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append([]string{
			"external-oidc",
			"--config=/etc/kubernetes/config/auth-config/auth-config.json",
			"--secure-port=8443",
			"--tls-private-key-file=/etc/kubernetes/certs/serving/tls.key",
			"--tls-cert-file=/etc/kubernetes/certs/serving/tls.crt",
			fmt.Sprintf("--tls-min-version=%s", config.MinTLSVersion(configuration.GetTLSSecurityProfile())),
			"--v=2",
		}, c.Args...)

		if cipherSuites := config.CipherSuites(configuration.GetTLSSecurityProfile()); len(cipherSuites) != 0 {
			c.Args = append(c.Args, fmt.Sprintf("--tls-cipher-suites=%s", strings.Join(cipherSuites, ",")))
		}

		c.VolumeMounts = append(c.VolumeMounts,
			corev1.VolumeMount{Name: "auth-config", MountPath: "/etc/kubernetes/config/auth-config"},
		)
	})

	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
		corev1.Volume{
			Name: "auth-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "openshift-oauth-apiserver-auth-config"},
					DefaultMode:          ptr.To(int32(420)),
				},
			},
		},
	)

	// No trusted-ca-bundle volume needed: the OIDC issuer CA cert is embedded
	// inline in the auth-config JSON by the generator.

	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		podspec.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "NO_PROXY",
			Value: manifests.KubeAPIServerService("").Name,
		})
	})

	return nil
}
