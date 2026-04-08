package util

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/smithy-go"

	corev1 "k8s.io/api/core/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// cloudTrailOnce ensures the CloudTrail permission denied check runs only once per process.
var cloudTrailOnce sync.Once

// PermissionDeniedEvent represents a single permission denied event from CloudTrail.
type PermissionDeniedEvent struct {
	EventTime    time.Time `json:"eventTime"`
	EventName    string    `json:"eventName"`
	EventSource  string    `json:"eventSource"`
	ErrorCode    string    `json:"errorCode"`
	ErrorMessage string    `json:"errorMessage"`
	RoleARN      string    `json:"roleARN"`
}

// CloudTrailPermissionReport holds the results of a CloudTrail scan.
type CloudTrailPermissionReport struct {
	StartTime time.Time               `json:"startTime"`
	EndTime   time.Time               `json:"endTime"`
	RoleARNs  []string                `json:"roleARNs"`
	Events    []PermissionDeniedEvent `json:"events"`
}

// cloudTrailEventPayload is a minimal struct for parsing CloudTrail event JSON.
type cloudTrailEventPayload struct {
	EventName    string `json:"eventName"`
	EventSource  string `json:"eventSource"`
	ErrorCode    string `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
	UserIdentity struct {
		ARN            string `json:"arn"`
		SessionContext struct {
			SessionIssuer struct {
				ARN string `json:"arn"`
			} `json:"sessionIssuer"`
		} `json:"sessionContext"`
	} `json:"userIdentity"`
}

var permissionDeniedErrorCodes = map[string]bool{
	"AccessDenied":                 true,
	"Client.UnauthorizedAccess":    true,
	"Client.UnauthorizedOperation": true,
	"UnauthorizedOperation":        true,
}

// matchesRole checks if a CloudTrail event was made by one of the target roles.
// It checks the sessionIssuer ARN (IAM role ARN) directly, and also extracts
// the role name from the userIdentity ARN (arn:aws:sts::ACCOUNT:assumed-role/ROLE/SESSION)
// to handle cases where the session name is a numeric ID or UUID.
func matchesRole(payload cloudTrailEventPayload, roleARNSet map[string]bool, roleNameSet map[string]bool) (string, bool) {
	issuerARN := payload.UserIdentity.SessionContext.SessionIssuer.ARN
	if roleARNSet[issuerARN] {
		return issuerARN, true
	}

	// Extract role name from arn:aws:sts::ACCOUNT:assumed-role/ROLE_NAME/SESSION
	identityARN := payload.UserIdentity.ARN
	if strings.Contains(identityARN, ":assumed-role/") {
		parts := strings.SplitN(identityARN, ":assumed-role/", 2)
		if len(parts) == 2 {
			if slashIdx := strings.Index(parts[1], "/"); slashIdx > 0 {
				roleName := parts[1][:slashIdx]
				if roleNameSet[roleName] {
					if issuerARN != "" {
						return issuerARN, true
					}
					return identityARN, true
				}
			}
		}
	}

	return "", false
}

// buildRoleNameSet extracts role names from IAM role ARNs.
// arn:aws:iam::ACCOUNT:role/ROLE_NAME -> ROLE_NAME
func buildRoleNameSet(roleARNs []string) map[string]bool {
	names := make(map[string]bool, len(roleARNs))
	for _, arn := range roleARNs {
		if idx := strings.LastIndex(arn, "/"); idx >= 0 {
			names[arn[idx+1:]] = true
		}
	}
	return names
}

// lookupCloudTrailPermissionDenied queries CloudTrail for permission denied events
// associated with the given role ARNs within the specified time window.
// CloudTrail events can take up to 15 minutes to appear after the API call occurs.
func lookupCloudTrailPermissionDenied(ctx context.Context, awsCreds, awsRegion string, startTime, endTime time.Time, roleARNs []string) (*CloudTrailPermissionReport, error) {
	awsSession := awsutil.NewSession(ctx, "e2e-cloudtrail", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfig()
	ctClient := cloudtrail.NewFromConfig(*awsSession, func(o *cloudtrail.Options) {
		o.Retryer = awsConfig()
	})

	roleARNSet := make(map[string]bool, len(roleARNs))
	for _, arn := range roleARNs {
		roleARNSet[arn] = true
	}
	roleNameSet := buildRoleNameSet(roleARNs)

	report := &CloudTrailPermissionReport{
		StartTime: startTime,
		EndTime:   endTime,
		RoleARNs:  roleARNs,
	}

	input := &cloudtrail.LookupEventsInput{
		StartTime:  aws.Time(startTime),
		EndTime:    aws.Time(endTime),
		MaxResults: aws.Int32(50),
	}

	for {
		output, err := ctClient.LookupEvents(ctx, input)
		if err != nil {
			// On throttling, return what we have so far rather than failing
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "ThrottlingException" {
				return report, nil
			}
			return nil, fmt.Errorf("failed to lookup CloudTrail events: %w", err)
		}

		for _, event := range output.Events {
			if event.CloudTrailEvent == nil {
				continue
			}

			var payload cloudTrailEventPayload
			if err := json.Unmarshal([]byte(*event.CloudTrailEvent), &payload); err != nil {
				continue
			}

			if !permissionDeniedErrorCodes[payload.ErrorCode] {
				continue
			}

			matchedARN, ok := matchesRole(payload, roleARNSet, roleNameSet)
			if !ok {
				continue
			}

			eventTime := time.Time{}
			if event.EventTime != nil {
				eventTime = *event.EventTime
			}

			report.Events = append(report.Events, PermissionDeniedEvent{
				EventTime:    eventTime,
				EventName:    payload.EventName,
				EventSource:  payload.EventSource,
				ErrorCode:    payload.ErrorCode,
				ErrorMessage: payload.ErrorMessage,
				RoleARN:      matchedARN,
			})
		}

		if output.NextToken == nil || *output.NextToken == "" {
			break
		}
		input.NextToken = output.NextToken
	}

	return report, nil
}

// extractRoleARNs collects unique IAM role ARNs from a HostedCluster's AWS platform spec.
func extractRoleARNs(hostedCluster *hyperv1.HostedCluster) []string {
	if hostedCluster.Spec.Platform.AWS == nil {
		return nil
	}

	ref := hostedCluster.Spec.Platform.AWS.RolesRef
	arns := []string{
		ref.IngressARN,
		ref.ImageRegistryARN,
		ref.StorageARN,
		ref.NetworkARN,
		ref.KubeCloudControllerARN,
		ref.NodePoolManagementARN,
		ref.ControlPlaneOperatorARN,
	}

	seen := make(map[string]bool)
	var unique []string
	for _, arn := range arns {
		if arn != "" && !seen[arn] {
			seen[arn] = true
			unique = append(unique, arn)
		}
	}
	return unique
}

// discoverHCPRoleARNs scans all Pods and ServiceAccounts in the HCP namespace to find
// management cluster role ARNs (injected by EKS Pod Identity or IRSA) that wouldn't
// appear in the HostedCluster's RolesRef. This catches roles for CPO, cloud-controller-manager,
// ingress operator, CSI drivers, and other control plane components.
func discoverHCPRoleARNs(ctx context.Context, client crclient.Client, hcpNamespace string) []string {
	var roleARNs []string
	seen := make(map[string]bool)

	addRole := func(arn string) {
		if arn != "" && !seen[arn] {
			seen[arn] = true
			roleARNs = append(roleARNs, arn)
		}
	}

	// Check all ServiceAccounts in the HCP namespace for IRSA annotations
	saList := &corev1.ServiceAccountList{}
	if err := client.List(ctx, saList, crclient.InNamespace(hcpNamespace)); err == nil {
		for _, sa := range saList.Items {
			addRole(sa.Annotations["eks.amazonaws.com/role-arn"])
		}
	}

	// Check all running Pods for AWS_ROLE_ARN env var (injected by EKS Pod Identity webhook at admission)
	podList := &corev1.PodList{}
	if err := client.List(ctx, podList, crclient.InNamespace(hcpNamespace)); err == nil {
		for _, pod := range podList.Items {
			for _, c := range pod.Spec.Containers {
				for _, env := range c.Env {
					if env.Name == "AWS_ROLE_ARN" {
						addRole(env.Value)
					}
				}
			}
			for _, c := range pod.Spec.InitContainers {
				for _, env := range c.Env {
					if env.Name == "AWS_ROLE_ARN" {
						addRole(env.Value)
					}
				}
			}
		}
	}

	return roleARNs
}

// NoticeCloudTrailPermissionDenied queries CloudTrail for permission denied events
// associated with the HostedCluster's IAM roles and logs any findings.
// This is a non-failing check (uses t.Logf) for informational purposes.
// Only runs once per process (via sync.Once) to avoid CloudTrail API rate limits
// when multiple tests share the same roles in a CI job.
func NoticeCloudTrailPermissionDenied(t *testing.T, ctx context.Context, client crclient.Client, awsCreds, awsRegion string, startTime time.Time, hostedCluster *hyperv1.HostedCluster) {
	t.Run("NoticeCloudTrailPermissionDenied", func(t *testing.T) {
		cloudTrailOnce.Do(func() {
			roleARNs := extractRoleARNs(hostedCluster)

			// Discover management cluster role ARNs from HCP namespace pods/SAs (Pod Identity, IRSA)
			hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
			hcpRoleARNs := discoverHCPRoleARNs(ctx, client, hcpNamespace)
			if len(hcpRoleARNs) > 0 {
				t.Logf("Discovered HCP pod role ARNs: %s", strings.Join(hcpRoleARNs, ", "))
				seen := make(map[string]bool, len(roleARNs))
				for _, arn := range roleARNs {
					seen[arn] = true
				}
				for _, arn := range hcpRoleARNs {
					if !seen[arn] {
						roleARNs = append(roleARNs, arn)
					}
				}
			}

			if len(roleARNs) == 0 {
				t.Logf("No AWS role ARNs found on HostedCluster, skipping CloudTrail check")
				return
			}

			endTime := time.Now()
			report, err := lookupCloudTrailPermissionDenied(ctx, awsCreds, awsRegion, startTime, endTime, roleARNs)
			if err != nil {
				t.Logf("warning: failed to query CloudTrail for permission denied events: %v", err)
				return
			}

			t.Logf("CloudTrail Permission Denied Report for HostedCluster %s/%s", hostedCluster.Namespace, hostedCluster.Name)
			t.Logf("  Time window: %s to %s", startTime.Format(time.RFC3339), endTime.Format(time.RFC3339))
			t.Logf("  Roles checked: %s", strings.Join(roleARNs, ", "))

			if len(report.Events) == 0 {
				t.Logf("  No permission denied events found")
			} else {
				// "error: " prefix triggers Prow syntax highlighting
				t.Logf("error: non-fatal, found %d CloudTrail permission denied event(s) for HostedCluster %s/%s",
					len(report.Events), hostedCluster.Namespace, hostedCluster.Name)
				for i, event := range report.Events {
					t.Logf("error: non-fatal, [%d] %s on %s (%s) by role %s",
						i+1, event.ErrorCode, event.EventName, event.EventSource, event.RoleARN)
					if event.ErrorMessage != "" {
						t.Logf("       Message: %s", event.ErrorMessage)
					}
				}
			}

			// Always write JSON report to artifact dir so the check is visible in CI
			artifactDir := os.Getenv("ARTIFACT_DIR")
			if artifactDir != "" {
				reportJSON, err := json.MarshalIndent(report, "", "  ")
				if err == nil {
					reportPath := filepath.Join(artifactDir, fmt.Sprintf("cloudtrail-permission-denied-%s.json", hostedCluster.Name))
					if err := os.WriteFile(reportPath, reportJSON, 0644); err != nil {
						t.Logf("warning: failed to write CloudTrail report to %s: %v", reportPath, err)
					} else {
						t.Logf("CloudTrail permission denied report written to %s", reportPath)
					}
				}
			}
		})
	})
}
