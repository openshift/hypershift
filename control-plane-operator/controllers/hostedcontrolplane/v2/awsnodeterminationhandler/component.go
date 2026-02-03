package awsnodeterminationhandler

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ComponentName = "aws-node-termination-handler"

	// DefaultAWSNodeTerminationHandlerImage is the default image for the AWS Node Termination Handler.
	// TODO(alberto): Replace this with mirror image or payload once available.
	DefaultAWSNodeTerminationHandlerImage = "public.ecr.aws/aws-ec2/aws-node-termination-handler:v1.25.3"
)

var _ component.ComponentOptions = &options{}

type options struct{}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (o *options) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (o *options) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (o *options) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &options{}).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter("credentials-secret.yaml",
			component.WithAdaptFunction(adaptCredentialsSecret),
		).
		WithPredicate(predicate).
		InjectTokenMinterContainer(component.TokenMinterContainerOptions{
			TokenType:               component.CloudToken,
			ServiceAccountName:      "capa-controller-manager",
			ServiceAccountNameSpace: "kube-system",
			KubeconfingVolumeName:   "svc-kubeconfig",
		}).
		Build()
}

// getTerminationHandlerQueueURL returns the SQS queue URL for the termination handler.
// Returns empty string if not configured or if hcp is nil.
func getTerminationHandlerQueueURL(hcp *hyperv1.HostedControlPlane) string {
	if hcp == nil {
		return ""
	}
	if hcp.Spec.Platform.AWS != nil && hcp.Spec.Platform.AWS.TerminationHandlerQueueURL != nil {
		return *hcp.Spec.Platform.AWS.TerminationHandlerQueueURL
	}
	return ""
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	hcp := cpContext.HCP

	// Only deploy for AWS platform
	if hcp.Spec.Platform.Type != hyperv1.AWSPlatform {
		return false, nil
	}

	// Require AWS platform spec with NodePoolManagementARN for credentials
	if hcp.Spec.Platform.AWS == nil || hcp.Spec.Platform.AWS.RolesRef.NodePoolManagementARN == "" {
		return false, nil
	}

	// Require SQS queue URL from API
	queueURL := getTerminationHandlerQueueURL(hcp)
	if queueURL == "" {
		return false, nil
	}

	// Check if we have the required kubeconfig secret
	if hcp.Status.KubeConfig == nil {
		return false, nil
	}

	// Verify service-network-admin-kubeconfig exists
	kubeconfigSecret := &corev1.Secret{}
	if err := cpContext.Client.Get(cpContext, client.ObjectKey{
		Namespace: hcp.Namespace,
		Name:      "service-network-admin-kubeconfig",
	}, kubeconfigSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get service-network-admin-kubeconfig secret: %w", err)
	}

	return true, nil
}
