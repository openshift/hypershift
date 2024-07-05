package kas

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"
	k8sadmissionv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type AdmissionPolicy struct {
	Name             string
	MatchConstraints *k8sadmissionv1beta1.MatchResources
	Validations      []k8sadmissionv1beta1.Validation
}

const (
	AdmissionPolicyNameConfig = "config"
	AdmissionPolicyNameICSP   = "icsp"
)

var (
	defaultAdmissionPoliciesOperations = []k8sadmissionv1beta1.OperationType{"UPDATE", "DELETE"}
	defaultMatchResourcesScope         = k8sadmissionv1beta1.ScopeType("*")
	defaultMatchPolicyType             = k8sadmissionv1beta1.Equivalent
	HCCOUserValidation                 = k8sadmissionv1beta1.Validation{
		Expression: fmt.Sprintf("request.userInfo.username == 'system:%s' || object.spec == oldObject.spec", config.HCCOUser),
		Message:    "This resource cannot be created, updated, or deleted. Please ask your administrator to modify the resource in the HostedCluster object.",
		Reason:     ptr.To(metav1.StatusReasonInvalid),
	}
)

// ReconcileKASValidatingAdmissionPolicies will create ValidatingAdmissionPolicies which block certain resources
// from being updated/deleted from the DataPlane side.
func ReconcileKASValidatingAdmissionPolicies(ctx context.Context, hcp *hyperv1.HostedControlPlane, client client.Client, createOrUpdate upsert.CreateOrUpdateFN) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling validating admission policies")

	if err := reconcileConfigValidatingAdmissionPolicy(ctx, hcp, client, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile Config Validating Admission Policy: %v", err)
	}

	if err := reconcileICSPValidatingAdmissionPolicy(ctx, client, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile ICSP Validating Admission Policy: %v", err)
	}

	return nil
}

func reconcileICSPValidatingAdmissionPolicy(ctx context.Context, client client.Client, createOrUpdate upsert.CreateOrUpdateFN) error {
	icspAdmissionPolicy := AdmissionPolicy{Name: AdmissionPolicyNameICSP}
	icspAPIVersion := []string{operatorv1alpha1.GroupVersion.Version}
	icspAPIGroup := []string{operatorv1alpha1.GroupVersion.Group}
	icspResources := []string{"imagecontentsourcepolicies"}

	icspAdmissionPolicy.Validations = []k8sadmissionv1beta1.Validation{HCCOUserValidation}
	icspAdmissionPolicy.MatchConstraints = constructPolicyMatchConstraints(icspResources, icspAPIVersion, icspAPIGroup, defaultAdmissionPoliciesOperations)
	if err := icspAdmissionPolicy.reconcileAdmissionPolicy(ctx, client, createOrUpdate); err != nil {
		return fmt.Errorf("error reconciling ICSP Validating Admission Policy: %v", err)
	}

	return nil
}

func reconcileConfigValidatingAdmissionPolicy(ctx context.Context, hcp *hyperv1.HostedControlPlane, client client.Client, createOrUpdate upsert.CreateOrUpdateFN) error {
	// Config AdmissionPolicy
	configAdmissionPolicy := AdmissionPolicy{Name: AdmissionPolicyNameConfig}
	configAPIVersion := []string{configv1.GroupVersion.Version}
	configAPIGroup := []string{configv1.GroupVersion.Group}
	configResources := []string{
		"apiservers",
		"authentications",
		"featuregates",
		"images",
		"imagedigestmirrorsets",
		"imagetagmirrorsets",
		"imagecontentpolicies",
		"infrastructures",
		"ingresses",
		"proxies",
		"schedulers",
		"networks",
		"oauths",
	}

	if hcp.Spec.OLMCatalogPlacement == hyperv1.ManagementOLMCatalogPlacement {
		configResources = append(configResources, "operatorhubs")
	}
	configAdmissionPolicy.Validations = []k8sadmissionv1beta1.Validation{HCCOUserValidation}
	configAdmissionPolicy.MatchConstraints = constructPolicyMatchConstraints(configResources, configAPIVersion, configAPIGroup, defaultAdmissionPoliciesOperations)
	if err := configAdmissionPolicy.reconcileAdmissionPolicy(ctx, client, createOrUpdate); err != nil {
		return fmt.Errorf("error reconciling Config Validating Admission Policy: %v", err)
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
		policyBinding.Spec.ValidationActions = []k8sadmissionv1beta1.ValidationAction{k8sadmissionv1beta1.Deny}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create/update Validating Admission Policy Binding with name %s: %v", ap.Name, err)
	}

	return nil
}

func constructPolicyMatchConstraints(resources, apiVersion, apiGroup []string, operations []k8sadmissionv1beta1.OperationType) *k8sadmissionv1beta1.MatchResources {
	return &k8sadmissionv1beta1.MatchResources{
		ResourceRules: []k8sadmissionv1beta1.NamedRuleWithOperations{
			{
				RuleWithOperations: k8sadmissionv1beta1.RuleWithOperations{
					Operations: operations,
					Rule: k8sadmissionv1beta1.Rule{
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
