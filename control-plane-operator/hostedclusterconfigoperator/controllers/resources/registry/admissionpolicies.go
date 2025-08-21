package registry

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/upsert"

	imageregistryv1 "github.com/openshift/api/imageregistry/v1"

	k8sadmissionv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type AdmissionPolicy struct {
	Name             string
	MatchConstraints *k8sadmissionv1.MatchResources
	Validations      []k8sadmissionv1.Validation
}

const (
	AdmissionPolicyNameManagementState = "deny-removed-managementstate"
)

var (
	defaultMatchResourcesScope           = k8sadmissionv1.ScopeType("*")
	defaultMatchPolicyType               = k8sadmissionv1.Equivalent
	denyRemovedManagementStateValidation = k8sadmissionv1.Validation{
		Message: "Setting managementState to 'Removed' is not allowed in Image Registry config.",
		Reason:  ptr.To(metav1.StatusReasonInvalid),
	}
)

func ReconcileRegistryConfigValidatingAdmissionPolicies(ctx context.Context, hcp *hyperv1.HostedControlPlane, client client.Client, createOrUpdate upsert.CreateOrUpdateFN) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling image registry config validating admission policies")

	if err := reconcileRegistryConfigManagementStateValidatingAdmissionPolicy(ctx, hcp, client, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile ManagementState Validating Admission Policy: %v", err)
	}

	return nil
}

// reconcileRegistryConfigManagementStateValidatingAdmissionPolicy reconciles the Validating Admission Policy
// that controls access to the Image Registry config managementState field.
//
// During normal operation, it prevents users from setting managementState to 'Removed' except for the HCCO user.
func reconcileRegistryConfigManagementStateValidatingAdmissionPolicy(ctx context.Context, hcp *hyperv1.HostedControlPlane, client client.Client, createOrUpdate upsert.CreateOrUpdateFN) error {
	log := ctrl.LoggerFrom(ctx)
	registryConfigManagementStateAdmissionPolicy := AdmissionPolicy{Name: AdmissionPolicyNameManagementState}
	registryConfigManagementStateAPIVersion := []string{imageregistryv1.GroupVersion.Version}
	registryConfigManagementStateAPIGroup := []string{imageregistryv1.GroupVersion.Group}
	registryConfigManagementStateResources := []string{
		"configs",
	}

	// During normal operation, prevent users from setting managementState to Removed
	// but allow the HCCO user
	log.Info("Cluster is active, enforcing registry management state admission policy")
	denyRemovedManagementStateValidation.Expression = "object.spec.managementState != 'Removed' || request.userInfo.username == 'system:hosted-cluster-config'"

	registryConfigManagementStateAdmissionPolicy.Validations = []k8sadmissionv1.Validation{denyRemovedManagementStateValidation}
	registryConfigManagementStateAdmissionPolicy.MatchConstraints = constructPolicyMatchConstraints(registryConfigManagementStateResources, registryConfigManagementStateAPIVersion, registryConfigManagementStateAPIGroup, []k8sadmissionv1.OperationType{"CREATE", "UPDATE"})
	if err := registryConfigManagementStateAdmissionPolicy.reconcileAdmissionPolicy(ctx, client, createOrUpdate); err != nil {
		return fmt.Errorf("error reconciling management State Validating Admission Policy: %v", err)
	}

	return nil
}

func (ap *AdmissionPolicy) reconcileAdmissionPolicy(ctx context.Context, client client.Client, createOrUpdate upsert.CreateOrUpdateFN) error {
	vap := manifests.ValidatingAdmissionPolicy(ap.Name)
	if _, err := createOrUpdate(ctx, client, vap, func() error {
		if vap.Spec.MatchConstraints != nil {
			vap.Spec.MatchConstraints.ResourceRules = ap.MatchConstraints.ResourceRules
			vap.Spec.MatchConstraints.MatchPolicy = ap.MatchConstraints.MatchPolicy
		} else {
			vap.Spec.MatchConstraints = ap.MatchConstraints
		}
		vap.Spec.Validations = ap.Validations

		return nil
	}); err != nil {
		return fmt.Errorf("failed to create/update Validating Admission Policy with name %s: %v", ap.Name, err)
	}

	policyBinding := manifests.ValidatingAdmissionPolicyBinding(fmt.Sprintf("%s-binding", ap.Name))
	if _, err := createOrUpdate(ctx, client, policyBinding, func() error {
		policyBinding.Spec.PolicyName = ap.Name
		policyBinding.Spec.ValidationActions = []k8sadmissionv1.ValidationAction{k8sadmissionv1.Deny}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create/update Validating Admission Policy Binding with name %s: %v", ap.Name, err)
	}

	return nil
}

func constructPolicyMatchConstraints(resources, apiVersion, apiGroup []string, operations []k8sadmissionv1.OperationType) *k8sadmissionv1.MatchResources {
	return &k8sadmissionv1.MatchResources{
		ResourceRules: []k8sadmissionv1.NamedRuleWithOperations{
			{
				RuleWithOperations: k8sadmissionv1.RuleWithOperations{
					Operations: operations,
					Rule: k8sadmissionv1.Rule{
						APIGroups:   apiGroup,
						APIVersions: apiVersion,
						Resources:   resources,
						Scope:       &defaultMatchResourcesScope,
					},
				},
			},
		},
		MatchPolicy: &defaultMatchPolicyType,
	}
}
