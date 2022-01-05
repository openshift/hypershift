package agent

import (
	"context"
	"fmt"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1alpha1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/upsert"
	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Agent struct{}

func (p Agent) ReconcileCAPIInfraCR(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string, apiEndpoint hyperv1.APIEndpoint) (client.Object, error) {

	hcp := controlplaneoperator.HostedControlPlane(controlPlaneNamespace, hcluster.Name)
	if err := c.Get(ctx, client.ObjectKeyFromObject(hcp), hcp); err != nil {
		return nil, fmt.Errorf("failed to get control plane ref: %w", err)
	}

	agentCluster := &agentv1.AgentCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      hcluster.Name,
		},
	}

	_, err := createOrUpdate(ctx, c, agentCluster, func() error {
		return reconcileAgentCluster(agentCluster, hcluster, hcp)
	})
	if err != nil {
		return nil, err
	}

	return agentCluster, nil
}

func (p Agent) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, tokenMinterImage string) (*appsv1.DeploymentSpec, error) {
	// It is expected that cluster-api-provider-agent and the Infrastructure Operator are installed as a prerequisite
	return nil, nil
}

func (p Agent) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	return nil
}

func (Agent) ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {
	return nil
}

func (Agent) CAPIProviderPolicyRules() []rbacv1.PolicyRule {
	return nil
}

func reconcileAgentCluster(agentCluster *agentv1.AgentCluster, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) error {
	agentCluster.Spec.ReleaseImage = hcp.Spec.ReleaseImage
	agentCluster.Spec.ClusterName = hcluster.Name
	agentCluster.Spec.BaseDomain = hcluster.Spec.DNS.BaseDomain
	agentCluster.Spec.PullSecretRef = &hcp.Spec.PullSecret

	caSecret := ignitionserver.IgnitionCACertSecret(hcp.Namespace)
	if hcluster.Status.IgnitionEndpoint != "" {
		agentCluster.Spec.IgnitionEndpoint = &agentv1.IgnitionEndpoint{
			Url:                    "https://" + hcluster.Status.IgnitionEndpoint + "/ignition",
			CaCertificateReference: &agentv1.CaCertificateReference{Name: caSecret.Name, Namespace: caSecret.Namespace}}
	}
	agentCluster.Spec.ControlPlaneEndpoint = capiv1.APIEndpoint{
		Host: hcp.Status.ControlPlaneEndpoint.Host,
		Port: hcp.Status.ControlPlaneEndpoint.Port,
	}

	return nil
}
