package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

func gcpHealthCheckIdentityProvider(ctx context.Context, hcp *hyperv1.HostedControlPlane) error {
	kasAvailable := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.KubeAPIServerAvailable))
	if kasAvailable == nil || kasAvailable.Status != metav1.ConditionTrue {
		setGCPConditions(hcp, metav1.ConditionUnknown, hyperv1.StatusUnknownReason,
			"Cannot validate GCP credentials while KubeAPIServer is not available")
		return nil
	}

	if hcp.Spec.Platform.GCP == nil {
		setGCPConditions(hcp, metav1.ConditionFalse, "MissingGCPConfiguration",
			"GCP platform configuration is missing from HostedControlPlane spec")
		return fmt.Errorf("GCP platform configuration is missing from HostedControlPlane spec")
	}

	computeService, err := initGCPComputeClient(ctx)
	if err != nil {
		setGCPConditions(hcp, metav1.ConditionUnknown, hyperv1.StatusUnknownReason,
			fmt.Sprintf("GCP compute client is not available: %v", err))
		return nil
	}

	project := hcp.Spec.Platform.GCP.Project
	region := hcp.Spec.Platform.GCP.Region
	if _, err := computeService.Regions.Get(project, region).Context(ctx).Do(); err != nil {
		var apiErr *googleapi.Error
		if errors.As(err, &apiErr) && (apiErr.Code == 401 || apiErr.Code == 403) {
			setGCPConditions(hcp, metav1.ConditionFalse, hyperv1.InvalidIdentityProvider,
				fmt.Sprintf("GCP credential validation failed: %s", apiErr.Message))
			return fmt.Errorf("error health checking GCP identity provider: %w", err)
		}

		setGCPConditions(hcp, metav1.ConditionUnknown, hyperv1.StatusUnknownReason,
			fmt.Sprintf("GCP API error during credential validation: %v", err))
		return fmt.Errorf("error health checking GCP identity provider: %w", err)
	}

	setGCPConditions(hcp, metav1.ConditionTrue, hyperv1.AsExpectedReason, hyperv1.AllIsWellMessage)
	return nil
}

func setGCPConditions(hcp *hyperv1.HostedControlPlane, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
		Type:               string(hyperv1.ValidGCPWorkloadIdentity),
		ObservedGeneration: hcp.Generation,
		Status:             status,
		Reason:             reason,
		Message:            message,
	})
	meta.SetStatusCondition(&hcp.Status.Conditions, metav1.Condition{
		Type:               string(hyperv1.ValidGCPCredentials),
		ObservedGeneration: hcp.Generation,
		Status:             status,
		Reason:             reason,
		Message:            message,
	})
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
