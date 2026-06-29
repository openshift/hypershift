package gcp

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	ComponentName = "gcp-cloud-controller-manager"
)

var _ component.ComponentOptions = &gcpOptions{}

type gcpOptions struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *gcpOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *gcpOptions) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *gcpOptions) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	// The credentials secret (cloud-controller-manager-creds) is pre-created by the
	// hypershift-operator in ReconcileCredentials, so we don't need to create it here.
	// The deployment.yaml mounts that pre-created secret.
	return component.NewDeploymentComponent(ComponentName, &gcpOptions{}).
		WithPredicate(predicate).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfig),
		).
		InjectTokenMinterContainer(component.TokenMinterContainerOptions{
			TokenType:               component.CloudToken,
			ServiceAccountNameSpace: "kube-system",
			ServiceAccountName:      "cloud-controller-manager",
		}).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	return cpContext.HCP.Spec.Platform.Type == hyperv1.GCPPlatform, nil
}

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	podspec.UpdateContainer("cloud-controller-manager", deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		// Add TLS configuration based on cluster TLS security profile
		if tlsMinVersion := config.MinTLSVersion(hcp.Spec.Configuration.GetTLSSecurityProfile()); tlsMinVersion != "" {
			c.Args = append(c.Args, fmt.Sprintf("--tls-min-version=%s", tlsMinVersion))
		}
		if cipherSuites := config.CipherSuites(hcp.Spec.Configuration.GetTLSSecurityProfile()); len(cipherSuites) != 0 {
			c.Args = append(c.Args, fmt.Sprintf("--tls-cipher-suites=%s", strings.Join(cipherSuites, ",")))
		}
	})

	return nil
}
