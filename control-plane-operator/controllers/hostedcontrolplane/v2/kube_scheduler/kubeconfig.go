package scheduler

import (
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	kasv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
)

func adaptKubeconfig(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	svcURL := kasv2.InClusterKASURL(cpContext.HCP.Spec.Platform.Type)
	kubeconfig, err := kasv2.GenerateKubeConfig(cpContext, manifests.KubeSchedulerClientCertSecret(secret.Namespace), svcURL)
	if err != nil {
		return fmt.Errorf("failed to generate kubeconfig: %w", err)
	}

	secret.Data[util.KubeconfigKey] = kubeconfig
	return nil
}
