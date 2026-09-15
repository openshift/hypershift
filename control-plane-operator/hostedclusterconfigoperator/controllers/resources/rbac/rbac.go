package rbac

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/upsert"

	rbacv1 "k8s.io/api/rbac/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PolicyFacts carries the narrow set of policy inputs the RBAC domain needs.
type PolicyFacts struct {
	IsAroHCP     bool
	Capabilities *hyperv1.Capabilities
}

type manifestReconciler interface {
	upsert(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN) error
	getKey() string
}

type manifestAndReconcile[o client.Object] struct {
	manifest  func() o
	reconcile func(o) error
}

func (m manifestAndReconcile[o]) upsert(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN) error {
	obj := m.manifest()
	if _, err := createOrUpdate(ctx, c, obj, func() error {
		return m.reconcile(obj)
	}); err != nil {
		return fmt.Errorf("failed to reconcile %T %s: %w", obj, obj.GetName(), err)
	}
	return nil
}

func (m manifestAndReconcile[o]) getKey() string {
	obj := m.manifest()
	gvk := obj.GetObjectKind().GroupVersionKind()
	ns := obj.GetNamespace()
	name := obj.GetName()
	if ns != "" {
		return fmt.Sprintf("%s/%s/%s", ns, name, gvk.Kind)
	}
	return fmt.Sprintf("%s/%s", name, gvk.Kind)
}

func catalog(facts PolicyFacts) []manifestReconciler {
	reconcilers := []manifestReconciler{
		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.CSRApproverClusterRole, reconcile: ReconcileCSRApproverClusterRole},
		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.IngressToRouteControllerClusterRole, reconcile: ReconcileIngressToRouteControllerClusterRole},
		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.NamespaceSecurityAllocationControllerClusterRole, reconcile: ReconcileNamespaceSecurityAllocationControllerClusterRole},

		manifestAndReconcile[*rbacv1.Role]{manifest: manifests.IngressToRouteControllerRole, reconcile: ReconcileReconcileIngressToRouteControllerRole},

		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.CSRApproverClusterRoleBinding, reconcile: ReconcileCSRApproverClusterRoleBinding},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.IngressToRouteControllerClusterRoleBinding, reconcile: ReconcileIngressToRouteControllerClusterRoleBinding},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.NamespaceSecurityAllocationControllerClusterRoleBinding, reconcile: ReconcileNamespaceSecurityAllocationControllerClusterRoleBinding},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.NodeBootstrapperClusterRoleBinding, reconcile: ReconcileNodeBootstrapperClusterRoleBinding},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.CSRRenewalClusterRoleBinding, reconcile: ReconcileCSRRenewalClusterRoleBinding},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.MetricsClientClusterRoleBinding, reconcile: ReconcileGenericMetricsClusterRoleBinding("system:serviceaccount:hypershift:prometheus")},
		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.MetricsResourcesClusterRole, reconcile: ReconcileMetricsResourcesClusterRole},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.MetricsResourcesClusterRoleBinding, reconcile: ReconcileMetricsResourcesClusterRoleBinding},

		manifestAndReconcile[*rbacv1.RoleBinding]{manifest: manifests.IngressToRouteControllerRoleBinding, reconcile: ReconcileIngressToRouteControllerRoleBinding},

		manifestAndReconcile[*rbacv1.RoleBinding]{manifest: manifests.AuthenticatedReaderForAuthenticatedUserRolebinding, reconcile: ReconcileAuthenticatedReaderForAuthenticatedUserRolebinding},

		manifestAndReconcile[*rbacv1.Role]{manifest: manifests.KCMLeaderElectionRole, reconcile: ReconcileKCMLeaderElectionRole},
		manifestAndReconcile[*rbacv1.RoleBinding]{manifest: manifests.KCMLeaderElectionRoleBinding, reconcile: ReconcileKCMLeaderElectionRoleBinding},

		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.ImageTriggerControllerClusterRole, reconcile: ReconcileImageTriggerControllerClusterRole},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.ImageTriggerControllerClusterRoleBinding, reconcile: ReconcileImageTriggerControllerClusterRoleBinding},

		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.PodSecurityAdmissionLabelSyncerControllerClusterRole, reconcile: ReconcilePodSecurityAdmissionLabelSyncerControllerClusterRole},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.PodSecurityAdmissionLabelSyncerControllerRoleBinding, reconcile: ReconcilePodSecurityAdmissionLabelSyncerControllerRoleBinding},

		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.PriviligedNamespacesPSALabelSyncerClusterRole, reconcile: ReconcilePriviligedNamespacesPSALabelSyncerClusterRole},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.PriviligedNamespacesPSALabelSyncerClusterRoleBinding, reconcile: ReconcilePriviligedNamespacesPSALabelSyncerClusterRoleBinding},

		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.DeployerClusterRole, reconcile: ReconcileDeployerClusterRole},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.DeployerClusterRoleBinding, reconcile: ReconcileDeployerClusterRoleBinding},

		manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.UserOAuthClusterRole, reconcile: ReconcileUserOAuthClusterRole},
		manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.UserOAuthClusterRoleBinding, reconcile: ReconcileUserOAuthClusterRoleBinding},

		manifestAndReconcile[*rbacv1.Role]{manifest: manifests.KASConnectionCheckerRole, reconcile: ReconcileKASConnectionCheckerRole},
		manifestAndReconcile[*rbacv1.RoleBinding]{manifest: manifests.KASConnectionCheckerRoleBinding, reconcile: ReconcileKASConnectionCheckerRoleBinding},
	}

	if facts.IsAroHCP {
		reconcilers = append(reconcilers,
			manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.AzureDiskCSIDriverNodeServiceAccountRole, reconcile: ReconcileAzureDiskCSIDriverNodeServiceAccountClusterRole},
			manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.AzureDiskCSIDriverNodeServiceAccountRoleBinding, reconcile: ReconcileAzureDiskCSIDriverNodeServiceAccountClusterRoleBinding},

			manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.AzureFileCSIDriverNodeServiceAccountRole, reconcile: ReconcileAzureFileCSIDriverNodeServiceAccountClusterRole},
			manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.AzureFileCSIDriverNodeServiceAccountRoleBinding, reconcile: ReconcileAzureFileCSIDriverNodeServiceAccountClusterRoleBinding},

			manifestAndReconcile[*rbacv1.ClusterRole]{manifest: manifests.CloudNetworkConfigControllerServiceAccountRole, reconcile: ReconcileCloudNetworkConfigControllerServiceAccountClusterRole},
			manifestAndReconcile[*rbacv1.ClusterRoleBinding]{manifest: manifests.CloudNetworkConfigControllerServiceAccountRoleBinding, reconcile: ReconcileCloudNetworkConfigControllerServiceAccountClusterRoleBinding},
		)
	}

	return reconcilers
}

func isSkipped(key string, facts PolicyFacts) bool {
	capability, found := manifests.RbacCapabilityMap[key]
	return found && capability == hyperv1.IngressCapability && !capabilities.IsIngressCapabilityEnabled(facts.Capabilities)
}

// Reconcile applies all applicable RBAC resources in catalog order, aggregating errors.
func Reconcile(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN, facts PolicyFacts) error {
	var errs []error
	for _, m := range catalog(facts) {
		if isSkipped(m.getKey(), facts) {
			continue
		}
		if err := m.upsert(ctx, c, createOrUpdate); err != nil {
			errs = append(errs, err)
		}
	}
	return utilerrors.NewAggregate(errs)
}
