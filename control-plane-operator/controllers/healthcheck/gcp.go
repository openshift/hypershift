package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// gcpRegionChecker is the function used to check GCP region access.
// It is a package-level variable to allow injection in tests.
var gcpRegionChecker = func(ctx context.Context, project, region string) error {
	computeService, err := initGCPComputeClient(ctx)
	if err != nil {
		wrapped := fmt.Errorf("%w: %w", errComputeClientUnavailable, err)
		ctrl.LoggerFrom(ctx).V(4).Info("GCP compute client not available, skipping credential check", "reason", err.Error())
		return wrapped
	}
	_, err = computeService.Regions.Get(project, region).Context(ctx).Do()
	return err
}

// errComputeClientUnavailable is returned by gcpRegionChecker when the GCP
// compute client cannot be initialized (e.g. missing credentials file).
var errComputeClientUnavailable = fmt.Errorf("GCP compute client unavailable")

func gcpHealthCheckIdentityProvider(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	kasAvailable := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.KubeAPIServerAvailable))
	if kasAvailable == nil || kasAvailable.Status != metav1.ConditionTrue {
		setGCPConditions(hcp, metav1.ConditionUnknown, hyperv1.StatusUnknownReason,
			"Cannot validate GCP credentials while KubeAPIServer is not available")
		return nil
	}

	if hcp.Spec.Platform.GCP == nil {
		// Use Unknown rather than False: a missing GCP spec is a configuration
		// defect, not a credential failure. Setting False would cause
		// GetCredentialStatus to return CredentialStatusInvalid and trigger
		// DeleteOrphanedMachines to strip GCPMachine finalizers, potentially
		// leaking cloud resources. The latch in ComputeGCPCredentialConditions
		// would then persist that False through teardown.
		setGCPConditions(hcp, metav1.ConditionUnknown, "MissingGCPConfiguration",
			"GCP platform configuration is missing from HostedControlPlane spec")
		return fmt.Errorf("GCP platform configuration is missing from HostedControlPlane spec")
	}

	project := hcp.Spec.Platform.GCP.Project
	region := hcp.Spec.Platform.GCP.Region
	if err := gcpRegionChecker(ctx, project, region); err != nil {
		if errors.Is(err, errComputeClientUnavailable) {
			setGCPConditions(hcp, metav1.ConditionUnknown, hyperv1.StatusUnknownReason,
				"GCP compute client is not available")
			return nil //nolint:nilerr // missing compute client is not a reconciler error; conditions are set to Unknown
		}
		if isWIFTokenError(err) {
			// WIF token exchange failed: the workload identity configuration is
			// invalid. Credentials cannot be validated because the token cannot
			// be obtained; mark ValidGCPCredentials as Unknown.
			setGCPCondition(hcp, hyperv1.ValidGCPWorkloadIdentity, metav1.ConditionFalse,
				hyperv1.InvalidIdentityProvider, "GCP Workload Identity Federation token exchange failed")
			setGCPCondition(hcp, hyperv1.ValidGCPCredentials, metav1.ConditionUnknown,
				hyperv1.StatusUnknownReason, "Cannot validate GCP credentials: WIF token exchange failed")
			return fmt.Errorf("error health checking GCP identity provider: %w", err)
		}
		if isComputeAuthError(err) {
			// The WIF token was obtained successfully (WorkloadIdentity is valid)
			// but the Compute API rejected the resulting credential with HTTP 401.
			// Only ValidGCPCredentials is False; ValidGCPWorkloadIdentity stays True
			// because the token exchange itself succeeded.
			setGCPCondition(hcp, hyperv1.ValidGCPWorkloadIdentity, metav1.ConditionTrue,
				hyperv1.AsExpectedReason, "GCP Workload Identity Federation token exchange succeeded")
			setGCPCondition(hcp, hyperv1.ValidGCPCredentials, metav1.ConditionFalse,
				hyperv1.InvalidIdentityProvider, "GCP credential validation failed: Compute API rejected the credential")
			return fmt.Errorf("error health checking GCP identity provider: %w", err)
		}

		setGCPConditions(hcp, metav1.ConditionUnknown, hyperv1.StatusUnknownReason,
			"GCP API error during credential validation")
		return fmt.Errorf("error health checking GCP identity provider: %w", err)
	}

	setGCPConditions(hcp, metav1.ConditionTrue, hyperv1.AsExpectedReason, hyperv1.AllIsWellMessage)
	return nil
}

func setGCPCondition(hcp *hyperv1.HostedControlPlane, condType hyperv1.ConditionType, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
		Type:               string(condType),
		ObservedGeneration: hcp.Generation,
		Status:             status,
		Reason:             reason,
		Message:            message,
	})
}

func setGCPConditions(hcp *hyperv1.HostedControlPlane, status metav1.ConditionStatus, reason, message string) {
	setGCPCondition(hcp, hyperv1.ValidGCPWorkloadIdentity, status, reason, message)
	setGCPCondition(hcp, hyperv1.ValidGCPCredentials, status, reason, message)
}

// isWIFTokenError returns true if the error indicates a non-transient WIF
// token-exchange failure (oauth2.RetrieveError with HTTP 400/401/403).
// These errors mean the workload identity pool/provider/SA is misconfigured;
// no Compute API call was made yet.
func isWIFTokenError(err error) bool {
	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) && retrieveErr.Response != nil {
		code := retrieveErr.Response.StatusCode
		return code == 400 || code == 401 || code == 403
	}
	return false
}

// isComputeAuthError returns true if the Compute API itself rejected the
// request with HTTP 401 (authentication rejected). This indicates the WIF
// token was obtained but not accepted by the Compute API.
//
// Compute API 403 is NOT treated as an auth error because it means
// authentication succeeded but authorization failed (missing IAM permission,
// quota, etc.).
func isComputeAuthError(err error) bool {
	var apiErr *googleapi.Error
	return errors.As(err, &apiErr) && apiErr.Code == 401
}

func initGCPComputeClient(ctx context.Context) (*compute.Service, error) {
	credentialsFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if credentialsFile == "" {
		return nil, fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS not set")
	}
	if _, err := os.Stat(credentialsFile); err != nil {
		return nil, fmt.Errorf("credentials file not accessible at %s: %w", credentialsFile, err)
	}
	httpClient, err := google.DefaultClient(ctx, compute.CloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("failed to create Google Cloud client: %w", err)
	}
	return compute.NewService(ctx, option.WithHTTPClient(httpClient))
}
