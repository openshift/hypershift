package dnsoperator

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

func adaptDeployment(cpContext component.WorkloadContext, obj *appsv1.Deployment) error {
	util.UpdateContainer("dns-operator", obj.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		// TODO (alberto): enforce ImagePullPolicy in component defaults.
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = []string{"dns-operator"}
		c.Env = []corev1.EnvVar{
			{
				Name:  "RELEASE_VERSION",
				Value: cpContext.UserReleaseImageProvider.Version(),
			}, {
				Name:  "IMAGE",
				Value: cpContext.UserReleaseImageProvider.GetImage("coredns"),
			}, {
				Name:  "OPENSHIFT_CLI_IMAGE",
				Value: cpContext.UserReleaseImageProvider.GetImage("cli"),
			}, {
				Name:  "KUBE_RBAC_PROXY_IMAGE",
				Value: cpContext.UserReleaseImageProvider.GetImage("kube-rbac-proxy"),
			}, {
				Name:  "KUBECONFIG",
				Value: "/etc/kubernetes/kubeconfig",
			},
		}
		c.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("29Mi"),
			},
		}
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
	})
	obj.Spec.Template.Spec.TerminationGracePeriodSeconds = ptr.To[int64](2)
	return nil
}
