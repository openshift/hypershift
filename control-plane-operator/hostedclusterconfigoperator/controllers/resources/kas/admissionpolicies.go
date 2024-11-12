package kas

import (
	"context"
	"fmt"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"
	k8sadmissionv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	corev1 "k8s.io/api/core/v1"
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
	AdmissionPolicyNameConfig             = "config"
	AdmissionPolicyNameMirror             = "mirror"
	AdmissionPolicyNameICSP               = "icsp"
	AdmissionPolicyNameInfra              = "infra"
	AdmissionPolicyNameNTOMirroredConfigs = "ntomirroredconfigmaps"
	cnoSAUser                             = "system:serviceaccount:openshift-network-operator:cluster-network-operator"

	BaseCelExpression = "has(object.spec) && has(oldObject.spec) && object.spec == oldObject.spec"
)

var (
	// This is a list of users that are allowed to create, update, or delete resources
	// without limitation in the HostedCluster side. It is usually used for the clusterOperators
	// that needs to modify the Spec after/during the HostedCluster is created.
	userWhiteList = []string{
		fmt.Sprintf("system:%s", config.HCCOUser),
	}
	allAdmissionPoliciesOperations = []k8sadmissionv1beta1.OperationType{"*"}
	defaultMatchResourcesScope     = k8sadmissionv1beta1.ScopeType("*")
	defaultMatchPolicyType         = k8sadmissionv1beta1.Equivalent
	HCCOUserValidation             = k8sadmissionv1beta1.Validation{
		Message: "This resource cannot be created, updated, or deleted. Please ask your administrator to modify the resource in the HostedCluster object.",
		Reason:  ptr.To(metav1.StatusReasonInvalid),
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

	if err := reconcileMirrorValidatingAdmissionPolicy(ctx, hcp, client, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile Mirror Validating Admission Policies: %v", err)
	}

	if err := reconcileInfraValidatingAdmissionPolicy(ctx, hcp, client, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile Infrastructure Validating Admission Policy: %v", err)
	}

	if err := reconcileConfigMapsValidatingAdmissionPolicy(ctx, client, createOrUpdate); err != nil {
		return fmt.Errorf("failed to reconcile Mirrored Configs Validating Admission Policy: %w", err)
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
		"imagecontentpolicies",
		"ingresses",
		"proxies",
		"schedulers",
		"networks",
		"oauths",
	}

	if hcp.Spec.OLMCatalogPlacement == hyperv1.ManagementOLMCatalogPlacement {
		configResources = append(configResources, "operatorhubs")
	}

	HCCOUserValidation.Expression = generateCelExpression(userWhiteList)
	configAdmissionPolicy.Validations = []k8sadmissionv1beta1.Validation{HCCOUserValidation}
	configAdmissionPolicy.MatchConstraints = constructPolicyMatchConstraints(configResources, configAPIVersion, configAPIGroup, []k8sadmissionv1beta1.OperationType{"UPDATE", "DELETE"})
	if err := configAdmissionPolicy.reconcileAdmissionPolicy(ctx, client, createOrUpdate); err != nil {
		return fmt.Errorf("error reconciling Config Validating Admission Policy: %v", err)
	}

	return nil
}

func reconcileInfraValidatingAdmissionPolicy(ctx context.Context, hcp *hyperv1.HostedControlPlane, client client.Client, createOrUpdate upsert.CreateOrUpdateFN) error {
	// Infra AdmissionPolicy
	// This VAP only reconciles the ValidationAdmissionPolicy for the Infrastructure resource
	// in order to allow certain SAs to update the spec field of the resource.
	infraAdmissionPolicy := AdmissionPolicy{Name: AdmissionPolicyNameInfra}
	infraAPIVersion := []string{configv1.GroupVersion.Version}
	infraAPIGroup := []string{configv1.GroupVersion.Group}
	infraResources := []string{"infrastructures"}

	HCCOUserValidation.Expression = generateCelExpression(append(userWhiteList, cnoSAUser))
	infraAdmissionPolicy.Validations = []k8sadmissionv1beta1.Validation{HCCOUserValidation}
	infraAdmissionPolicy.MatchConstraints = constructPolicyMatchConstraints(infraResources, infraAPIVersion, infraAPIGroup, []k8sadmissionv1beta1.OperationType{"UPDATE", "DELETE"})
	if err := infraAdmissionPolicy.reconcileAdmissionPolicy(ctx, client, createOrUpdate); err != nil {
		return fmt.Errorf("error reconciling Infrastructure Validating Admission Policy: %v", err)
	}

	return nil
}

func reconcileMirrorValidatingAdmissionPolicy(ctx context.Context, hcp *hyperv1.HostedControlPlane, client client.Client, createOrUpdate upsert.CreateOrUpdateFN) error {
	// Mirroring AdmissionPolicies
	mirrorAdmissionPolicy := AdmissionPolicy{Name: AdmissionPolicyNameMirror}
	mirrorAPIVersion := []string{configv1.GroupVersion.Version}
	mirrorAPIGroup := []string{configv1.GroupVersion.Group}
	mirrorResources := []string{
		"imagedigestmirrorsets",
		"imagetagmirrorsets",
	}

	if hcp.Spec.OLMCatalogPlacement == hyperv1.ManagementOLMCatalogPlacement {
		mirrorResources = append(mirrorResources, "operatorhubs")
	}

	HCCOUserValidation.Expression = generateCelExpression(userWhiteList)
	mirrorAdmissionPolicy.Validations = []k8sadmissionv1beta1.Validation{HCCOUserValidation}
	mirrorAdmissionPolicy.MatchConstraints = constructPolicyMatchConstraints(mirrorResources, mirrorAPIVersion, mirrorAPIGroup, allAdmissionPoliciesOperations)
	if err := mirrorAdmissionPolicy.reconcileAdmissionPolicy(ctx, client, createOrUpdate); err != nil {
		return fmt.Errorf("error reconciling Mirror Validating Admission Policy: %v", err)
	}

	// ICSP lives in other API, this is why we need to create another vap and vap-binding
	icspAdmissionPolicy := AdmissionPolicy{Name: AdmissionPolicyNameICSP}
	icspAPIVersion := []string{operatorv1alpha1.GroupVersion.Version}
	icspAPIGroup := []string{operatorv1alpha1.GroupVersion.Group}
	icspResources := []string{"imagecontentsourcepolicies"}

	icspAdmissionPolicy.Validations = []k8sadmissionv1beta1.Validation{HCCOUserValidation}
	icspAdmissionPolicy.MatchConstraints = constructPolicyMatchConstraints(icspResources, icspAPIVersion, icspAPIGroup, allAdmissionPoliciesOperations)
	if err := icspAdmissionPolicy.reconcileAdmissionPolicy(ctx, client, createOrUpdate); err != nil {
		return fmt.Errorf("error reconciling ICSP Validating Admission Policy: %v", err)
	}

	return nil
}

func reconcileConfigMapsValidatingAdmissionPolicy(ctx context.Context, client client.Client, createOrUpdate upsert.CreateOrUpdateFN) error {
	mirroredConfigsAdmissionPolicy := AdmissionPolicy{Name: AdmissionPolicyNameNTOMirroredConfigs}
	mirroredConfigsAPIVersion := []string{corev1.SchemeGroupVersion.Version}
	mirroredConfigsAPIGroup := []string{configv1.SchemeGroupVersion.Group}
	mirroredConfigsResources := []string{"configmaps"}

	HCCOUserValidation.Expression = generateCelExpression(userWhiteList)
	mirroredConfigsAdmissionPolicy.Validations = []k8sadmissionv1beta1.Validation{HCCOUserValidation}
	mirroredConfigsAdmissionPolicy.MatchConstraints = constructPolicyMatchConstraints(mirroredConfigsResources, mirroredConfigsAPIVersion, mirroredConfigsAPIGroup, []k8sadmissionv1beta1.OperationType{"UPDATE", "DELETE"})
	// we want to block changes only for configmaps with "hypershift.openshift.io/mirrored-config" label
	mirroredConfigsAdmissionPolicy.MatchConstraints.ObjectSelector = &metav1.LabelSelector{MatchLabels: map[string]string{nodepool.NTOMirroredConfigLabel: "true"}}
	if err := mirroredConfigsAdmissionPolicy.reconcileAdmissionPolicy(ctx, client, createOrUpdate); err != nil {
		return fmt.Errorf("error reconciling mirrored ConfigMaps Validating Admission Policy: %v", err)
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

func generateCelExpression(usernames []string) string {
	var userWhiteListExpression string

	if len(usernames) != 0 {
		quotedUsernames := make([]string, len(usernames))
		for i, username := range usernames {
			quotedUsernames[i] = fmt.Sprintf("'%s'", username)
		}

		userWhiteListExpression = fmt.Sprintf("request.userInfo.username in [%s]", strings.Join(quotedUsernames, ", "))
	}

	if len(userWhiteListExpression) == 0 {
		return BaseCelExpression
	}

	finalExpression := fmt.Sprintf("%s || (%s)", userWhiteListExpression, BaseCelExpression)

	return finalExpression
}
