package topology

import (
	"context"
	"fmt"
	"os"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftv1beta1 "github.com/openshift/hypershift/client/clientset/clientset/typed/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/controller/controllercmd"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

type Detector struct{}

func (d Detector) DetectTopology(ctx context.Context, restClient *rest.Config) (configv1.TopologyMode, error) {
	var namespace, name string
	for env, target := range map[string]*string{
		"HOSTED_CONTROL_PLANE_NAMESPACE": &namespace,
		"HOSTED_CONTROL_PLANE_NAME":      &name,
	} {
		value := os.Getenv(env)
		if value == "" {
			return "", fmt.Errorf("$%s is required", env)
		}
		*target = value
	}

	client, err := hypershiftv1beta1.NewForConfig(restClient)
	if err != nil {
		return "", fmt.Errorf("failed to create client: %w", err)
	}

	hcp, err := client.HostedControlPlanes(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get hosted control plane %s/%s: %q", namespace, name, err)
	}

	if hcp == nil {
		return "", fmt.Errorf("got a nil hosted control plane for %s/%s", namespace, name)
	}

	switch hcp.Spec.ControllerAvailabilityPolicy {
	case v1beta1.HighlyAvailable:
		return configv1.HighlyAvailableTopologyMode, nil
	case v1beta1.SingleReplica:
		return configv1.SingleReplicaTopologyMode, nil
	default:
		return "", fmt.Errorf("hosted control plane %s/%s has unknown topology mode %s", namespace, name, hcp.Spec.InfrastructureAvailabilityPolicy)
	}
}

var _ controllercmd.TopologyDetector = (*Detector)(nil)
