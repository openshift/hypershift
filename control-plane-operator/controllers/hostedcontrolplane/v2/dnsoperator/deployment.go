package dnsoperator

import (
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
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

// TODO(alberto) refactor cco, csi, this... and let the service account kubeconfig injection be common.
func adaptKubeconfigSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	csrSigner := manifests.CSRSignerCASecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(csrSigner), csrSigner); err != nil {
		return fmt.Errorf("failed to get cluster-signer-ca secret: %v", err)
	}
	rootCA := manifests.RootCAConfigMap(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca cert configMap: %w", err)
	}

	return pki.ReconcileServiceAccountKubeconfig(secret, csrSigner, rootCA, cpContext.HCP, "openshift-dns-operator", "dns-operator")
}
