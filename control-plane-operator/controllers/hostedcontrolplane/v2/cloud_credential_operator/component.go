package cco

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	ComponentName = "cloud-credential-operator"
)

var _ component.ComponentOptions = &cloudCredentialOperator{}

type cloudCredentialOperator struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *cloudCredentialOperator) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *cloudCredentialOperator) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *cloudCredentialOperator) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &cloudCredentialOperator{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(isAWSPlatform).
		WithDependencies(oapiv2.ComponentName).
		InjectServiceAccountKubeConfig(component.ServiceAccountKubeConfigOpts{
			Name:      "cloud-credential-operator",
			Namespace: "openshift-cloud-credential-operator",
			MountPath: "/etc/kubernetes",
		}).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{
			KubeconfigVolumeName:          component.ServiceAccountKubeconfigVolumeName,
			WaitForInfrastructureResource: true,
			RequiredAPIs: []schema.GroupVersionKind{
				{Group: "operator.openshift.io", Version: "v1", Kind: "CloudCredential"},
			},
		}).
		Build()
}

func isAWSPlatform(cpContext component.WorkloadContext) (bool, error) {
	return cpContext.HCP.Spec.Platform.Type == hyperv1.AWSPlatform, nil
}
