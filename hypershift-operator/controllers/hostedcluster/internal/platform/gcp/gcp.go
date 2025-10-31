// Package gcp provides Google Cloud Platform support for HyperShift.
//
// This package implements the Platform interface to enable HyperShift
// to create and manage OpenShift control planes on Google Cloud Platform.
//
// The package currently provides enough functionality to allow HostedCluster
// resources with platform.type: GCP to be accepted and reconciled by the
// HyperShift operator without errors. All methods return nil or no-op values
// to indicate that no platform-specific operations should be performed at this time.
//
// For more information about HyperShift platform implementations, see:
// https://github.com/openshift/hypershift/blob/main/docs/content/how-to/platforms.md
package gcp

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GCP implements the Platform interface for Google Cloud Platform.
//
// This is a minimal implementation that enables HostedCluster reconciliation
// for GCP platform. The struct provides no-op implementations for all Platform
// interface methods, which allows HyperShift to accept and process HostedCluster
// resources with platform.type: GCP without attempting actual cloud resource
// management.
type GCP struct{}

// New creates a new GCP platform instance.
//
// This function returns a new instance of the GCP platform implementation
// that can be used with the HyperShift platform factory. The returned
// platform provides minimal no-op implementations for all required
// Platform interface methods.
//
// The platform instance is stateless and can be safely reused across
// multiple HostedCluster reconciliation operations.
//
// Returns:
//   - *GCP: A new GCP platform instance ready for use with HyperShift controllers
//
// Example usage:
//
//	platform := gcp.New()
//	// platform can now be used with HyperShift platform factory
func New() *GCP {
	return &GCP{}
}

// ReconcileCAPIInfraCR is a no-op
// TODO: Implement CAPI/CAPG integration for NodePool support.
func (p GCP) ReconcileCAPIInfraCR(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string, apiEndpoint hyperv1.APIEndpoint) (client.Object, error) {

	// TODO: Implement CAPI GCP infrastructure reconciliation in future phase
	// for NodePool support (CAPG integration)
	return nil, nil
}

// CAPIProviderDeploymentSpec is a no-op
// TODO: Implement CAPI/CAPG deployment for NodePool support.
func (p GCP) CAPIProviderDeploymentSpec(hcluster *hyperv1.HostedCluster, _ *hyperv1.HostedControlPlane) (*appsv1.DeploymentSpec, error) {
	// TODO: Implement CAPI GCP provider deployment
	// for NodePool support (CAPG integration)
	return nil, nil
}

// ReconcileCredentials is a no-op
// TODO: Implement GCP workload identity credential reconciliation.
func (p GCP) ReconcileCredentials(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {

	// TODO: Implement GCP workload identity credential reconciliation
	return nil
}

// ReconcileSecretEncryption is a no-op
// TODO: Implement GCP KMS secret encryption integration.
func (p GCP) ReconcileSecretEncryption(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN,
	hcluster *hyperv1.HostedCluster,
	controlPlaneNamespace string) error {

	// TODO: Implement GCP KMS secret encryption
	return nil
}

// CAPIProviderPolicyRules is a no-op
// TODO: Implement CAPI/CAPG RBAC policy rules for NodePool support.
func (p GCP) CAPIProviderPolicyRules() []rbacv1.PolicyRule {
	// TODO: Implement CAPI GCP provider policy rules
	// for NodePool support (CAPG integration)
	return nil
}

// DeleteCredentials is a no-op
// TODO: Implement GCP workload identity credential cleanup.
func (p GCP) DeleteCredentials(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string) error {
	// TODO: Implement GCP credential cleanup
	return nil
}
