package ccm

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type CloudProviderParams struct {
	OwnerRef         config.OwnerRef `json:"ownerRef"`
	DeploymentConfig config.DeploymentConfig
}

func NewCloudProviderParams(hcp *hyperv1.HostedControlPlane) *CloudProviderParams {
	p := &CloudProviderParams{
		OwnerRef: config.OwnerRefFrom(hcp),
	}
	p.DeploymentConfig.Resources = config.ResourcesSpec{
		manifests.CCMContainer().Name: {
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("60Mi"),
				corev1.ResourceCPU:    resource.MustParse("75m"),
			},
		},
	}
	p.DeploymentConfig.LivenessProbes = config.LivenessProbes{
		manifests.CCMContainer().Name: {
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTP,
					Port:   intstr.FromInt(int(10258)),
					Path:   "healthz",
				},
			},
			InitialDelaySeconds: 120,
			TimeoutSeconds:      5,
			PeriodSeconds:       60,
		},
	}
	p.DeploymentConfig.AdditionalLabels = additionalLabels()
	p.DeploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	p.DeploymentConfig.SetControlPlaneIsolation(hcp)
	p.DeploymentConfig.SetColocationAnchor(hcp)

	p.DeploymentConfig.Replicas = 1

	return p
}

func ccmLabels() map[string]string {
	return map[string]string{
		"app": "cloud-controller-manager",
	}
}

func additionalLabels() map[string]string {
	return map[string]string{
		hyperv1.ControlPlaneComponent: "cloud-controller-manager",
	}
}
