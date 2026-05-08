package catalogd

import (
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/olmv1/util"
	component "github.com/openshift/hypershift/support/controlplane-component"
	appsv1 "k8s.io/api/apps/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	// Inject kubeconfig for hosted cluster API access
	// This adds: volume mount and KUBECONFIG env var (no --kubeconfig flag needed)
	if err := util.InjectHostedClusterKubeconfig(cpContext, deployment); err != nil {
		return err
	}

	// Add catalogd-specific configuration
	// External address uses namespace-specific service name for Konnectivity access
	externalAddress := fmt.Sprintf("catalogd.%s.svc", cpContext.HCP.Namespace)

	// Find the catalogd container and add required args
	for i := range deployment.Spec.Template.Spec.Containers {
		container := &deployment.Spec.Template.Spec.Containers[i]
		if container.Name == ComponentName {
			// Override default external address with namespace-specific service
			container.Args = append(container.Args,
				fmt.Sprintf("--external-address=%s", externalAddress),
				fmt.Sprintf("--system-namespace=%s", cpContext.HCP.Namespace),
			)
		}
	}

	return nil
}
