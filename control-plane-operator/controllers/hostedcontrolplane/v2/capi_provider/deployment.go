package capiprovider

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/proxy"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TODO: create a separate component for each platform?
func (capi *CAPIProviderOptions) adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	deployment.Spec = *capi.deploymentSpec
	deployment.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: labels(),
	}
	deployment.Spec.Template.Labels = labels()
	deployment.Spec.Template.Spec.ServiceAccountName = "capi-provider"

	proxy.SetEnvVars(&deployment.Spec.Template.Spec.Containers[0].Env)

	if hcp.Spec.Platform.Type == hyperv1.AWSPlatform && hcp.Spec.AdditionalTrustBundle != nil {
		podspec.DeploymentAddAWSCABundleVolume(hcp.Spec.AdditionalTrustBundle, deployment, cpContext.ReleaseImageProvider.GetImage(podspec.CPOImageName))
	}

	if deployment.Annotations == nil {
		deployment.Annotations = make(map[string]string)
	}
	deployment.Annotations[k8sutil.HostedClusterAnnotation] = hcp.Annotations[k8sutil.HostedClusterAnnotation]

	return nil
}

func labels() map[string]string {
	return map[string]string{
		"control-plane": "capi-provider-controller-manager",
		"app":           "capi-provider-controller-manager",
	}
}
