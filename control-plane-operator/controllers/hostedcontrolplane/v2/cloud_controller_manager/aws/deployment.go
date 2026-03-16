package aws

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	if cpContext.HCP.Spec.AdditionalTrustBundle != nil {
		util.DeploymentAddAWSCABundleVolume(cpContext.HCP.Spec.AdditionalTrustBundle, deployment, cpContext.ReleaseImageProvider.GetImage(util.CPOImageName))
	}
	return nil
}
