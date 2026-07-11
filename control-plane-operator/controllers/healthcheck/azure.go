package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/netutil"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// azureHealthCheckCredentials validates that the Azure credentials are functional
// by making a benign Resource Group GET call. This covers both credential paths:
//   - Self-managed: workload identity (needs KAS for token minting)
//   - ARO HCP (managed): user-assigned managed identity via CSI mount (no KAS dependency)
//
// The result is recorded in the ValidAzureIdentityProvider condition on the HCP.
// The condition name uses "IdentityProvider" for consistency with the AWS pattern
// (ValidAWSIdentityProvider), even though Azure managed clusters use managed
// identity rather than an OIDC identity provider.
func azureHealthCheckCredentials(ctx context.Context, hcp *hyperv1.HostedControlPlane, azureCreds azcore.TokenCredential) error {
	if hcp.Spec.Platform.Azure == nil {
		setAzureCredentialCondition(hcp, metav1.ConditionUnknown,
			hyperv1.StatusUnknownReason,
			"Azure platform configuration is missing")
		return nil
	}

	// Self-managed workload identity needs KAS for token minting.
	// ARO HCP uses managed identity from CSI mount — no KAS dependency.
	if !netutil.IsAroHCPByHCP(hcp) {
		kasAvailable := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.KubeAPIServerAvailable))
		if kasAvailable == nil || kasAvailable.Status != metav1.ConditionTrue {
			setAzureCredentialCondition(hcp, metav1.ConditionUnknown,
				hyperv1.StatusUnknownReason,
				"Cannot validate Azure credentials while KubeAPIServer is not available")
			return nil
		}
	}

	if azureCreds == nil {
		setAzureCredentialCondition(hcp, metav1.ConditionUnknown,
			hyperv1.StatusUnknownReason,
			"Azure credentials are not available for validation")
		return nil
	}

	_, err := azureutil.GetResourceGroupInfo(ctx,
		hcp.Spec.Platform.Azure.ResourceGroupName,
		hcp.Spec.Platform.Azure.SubscriptionID,
		azureCreds,
		hcp.Spec.Platform.Azure.Cloud)
	if err != nil {
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) {
			switch respErr.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				setAzureCredentialCondition(hcp, metav1.ConditionFalse,
					hyperv1.InvalidIdentityProvider,
					respErr.ErrorCode)
				return fmt.Errorf("azure credential auth failure: %s", respErr.ErrorCode)
			case http.StatusNotFound:
				setAzureCredentialCondition(hcp, metav1.ConditionFalse,
					hyperv1.InvalidIdentityProvider,
					fmt.Sprintf("Resource group %q not found", hcp.Spec.Platform.Azure.ResourceGroupName))
				return fmt.Errorf("resource group %q not found", hcp.Spec.Platform.Azure.ResourceGroupName)
			default:
				setAzureCredentialCondition(hcp, metav1.ConditionUnknown,
					hyperv1.AzureErrorReason,
					respErr.ErrorCode)
				return fmt.Errorf("azure API error: %s", respErr.ErrorCode)
			}
		}

		var authErr *azidentity.AuthenticationFailedError
		if errors.As(err, &authErr) {
			setAzureCredentialCondition(hcp, metav1.ConditionFalse,
				hyperv1.InvalidIdentityProvider,
				"Azure credential authentication failed")
			return fmt.Errorf("azure credential authentication failed: %w", err)
		}

		setAzureCredentialCondition(hcp, metav1.ConditionUnknown,
			hyperv1.StatusUnknownReason,
			err.Error())
		return fmt.Errorf("error health checking Azure credentials: %w", err)
	}

	setAzureCredentialCondition(hcp, metav1.ConditionTrue,
		hyperv1.AsExpectedReason,
		hyperv1.AllIsWellMessage)
	return nil
}

func setAzureCredentialCondition(hcp *hyperv1.HostedControlPlane, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
		Type:               string(hyperv1.ValidAzureIdentityProvider),
		ObservedGeneration: hcp.Generation,
		Status:             status,
		Reason:             reason,
		Message:            message,
	})
}
