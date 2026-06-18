package aws

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP
	if hcp.Spec.AdditionalTrustBundle != nil {
		podspec.DeploymentAddAWSCABundleVolume(hcp.Spec.AdditionalTrustBundle, deployment, cpContext.ReleaseImageProvider.GetImage(podspec.CPOImageName))
	}
	return nil
}
