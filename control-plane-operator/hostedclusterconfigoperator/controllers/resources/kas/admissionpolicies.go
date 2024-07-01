package kas

import (
	"context"
	"fmt"

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

type AdmissionPolicyBlacklist struct {
	AdmissionPolicies map[string]AdmissionPolicy
}

type AdmissionPolicy struct {
	Name        string
	APIGroup    []string
	ApiVersion  []string
	Resources   []string
	Validations []k8sadmissionv1beta1.Validation
}

const (
	AdmissionPolicyNameConfig = "config"
	AdmissionPolicyNameICSP   = "icsp"
)

var (
	defaultAdmissionPoliciesOperations = []k8sadmissionv1beta1.OperationType{"CREATE", "UPDATE", "DELETE"}
	HCCOUserValidation                 = k8sadmissionv1beta1.Validation{
		Expression: fmt.Sprintf("request.userInfo.username.contains('system:%s')", config.HCCOUser),
		Message:    "This resource cannot be created, updated, or deleted. Please ask your administrator to modify the resource in the HostedCluster object.",
		Reason:     ptr.To(metav1.StatusReasonInvalid),
	}
)

// in order to block certain resources from being created/updated/deleted from the DataPlane side.
// The force parameter is used to force the update of the existing Admission Policies in the KAS. This could
// be articulated via annotations or labels.
func ReconcileKASValidatingAdmissionPolicies(ctx context.Context, hcp *hyperv1.HostedControlPlane, client client.Client, createOrUpdate upsert.CreateOrUpdateFN, force bool) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling validating admission policies")
	apb := NewAdmissionPolicyBlacklist()
	apb.PopulateAdmissionPolicyBlacklist(hcp, force)
	if apb.AdmissionPolicies == nil {
		// The blacklist is nil, no need to create/update ValidationAdmissionPolicies
		return nil
	}

	for _, ap := range apb.AdmissionPolicies {
		// Create or Update the Validating Admission Policies
		policy := manifests.WithValidations(ap.Validations, manifests.WithPolicyMatch(ap.Resources, ap.ApiVersion, ap.APIGroup, defaultAdmissionPoliciesOperations, manifests.ValidatingAdmissionPolicy(ap.Name)))
		if _, err := createOrUpdate(ctx, client, policy, func() error { return nil }); err != nil {
			return fmt.Errorf("failed to create/update Validating Admission Policy with name %s: %v", ap.Name, err)
		}
		policyBinding := manifests.ValidatingAdmissionPolicyBinding(fmt.Sprintf("%s-binding", ap.Name), ap.Name, "")
		if _, err := createOrUpdate(ctx, client, policyBinding, func() error { return nil }); err != nil {
			return fmt.Errorf("failed to create/update Validating Admission Policy Binding with name %s: %v", ap.Name, err)
		}
	}

	return nil
}

func NewAdmissionPolicyBlacklist() AdmissionPolicyBlacklist {
	return AdmissionPolicyBlacklist{
		AdmissionPolicies: make(map[string]AdmissionPolicy, 0),
	}
}

func (apb *AdmissionPolicyBlacklist) PopulateAdmissionPolicyBlacklist(hcp *hyperv1.HostedControlPlane, force bool) {
	apb.AddAdmissionPolicy(AdmissionPolicy{
		Name:       AdmissionPolicyNameConfig,
		ApiVersion: []string{"v1"},
		APIGroup:   []string{"config.openshift.io"},
		Resources: []string{
			"apiservers",
			"authentication",
			"featuregates",
			"image",
			"imagedigestmirrorsets",
			"imagecontentpolicies",
			"infrastructures",
			"ingress",
			"networks",
			"oauths",
			"proxies",
			"scheduler",
		},
		Validations: []k8sadmissionv1beta1.Validation{HCCOUserValidation},
	}, force)

	if hcp.Spec.OLMCatalogPlacement == hyperv1.ManagementOLMCatalogPlacement {
		updatedPolicy := apb.AdmissionPolicies[AdmissionPolicyNameConfig]
		updatedPolicy.Resources = append(updatedPolicy.Resources, "operatorhubs")
		apb.UpdateAdmissionPolicy(apb.AdmissionPolicies[AdmissionPolicyNameConfig], &updatedPolicy)
	}

	apb.AddAdmissionPolicy(AdmissionPolicy{
		Name:        AdmissionPolicyNameICSP,
		ApiVersion:  []string{"v1alpha1"},
		APIGroup:    []string{"operator.openshift.io"},
		Resources:   []string{"imagecontentsourcepolicies"},
		Validations: []k8sadmissionv1beta1.Validation{HCCOUserValidation},
	}, force)

}

func (apb *AdmissionPolicyBlacklist) AddAdmissionPolicy(admissionPolicy AdmissionPolicy, force bool) {
	if _, ok := apb.AdmissionPolicies[admissionPolicy.Name]; ok && !force {
		return
	}
	apb.AdmissionPolicies[admissionPolicy.Name] = admissionPolicy
}

func (apb *AdmissionPolicyBlacklist) UpdateAdmissionPolicy(existingPolicy AdmissionPolicy, updatedPolicy *AdmissionPolicy) {
	if _, ok := apb.AdmissionPolicies[existingPolicy.Name]; ok {
		delete(apb.AdmissionPolicies, existingPolicy.Name)
		apb.AdmissionPolicies[existingPolicy.Name] = *updatedPolicy
	}
}
