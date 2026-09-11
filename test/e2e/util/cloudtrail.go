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

// cloudTrailChecked tracks which HostedClusters have already been checked,
// keyed by namespace/name, to avoid redundant CloudTrail API calls.
var cloudTrailChecked sync.Map

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
// the role name and account from the userIdentity ARN
// (arn:aws:sts::ACCOUNT:assumed-role/ROLE/SESSION) to handle cases where the
// session name is a numeric ID or UUID. The account is verified to prevent
// false matches against same-named roles in different AWS accounts.
func matchesRole(payload cloudTrailEventPayload, roleARNSet map[string]bool, roleAccountNameSet map[string]bool) (string, bool) {
	issuerARN := payload.UserIdentity.SessionContext.SessionIssuer.ARN
	if roleARNSet[issuerARN] {
		return issuerARN, true
	}

	// Extract account and role name from arn:aws:sts::ACCOUNT:assumed-role/ROLE_NAME/SESSION
	identityARN := payload.UserIdentity.ARN
	if strings.Contains(identityARN, ":assumed-role/") {
		account := extractAccountFromARN(identityARN)
		parts := strings.SplitN(identityARN, ":assumed-role/", 2)
		if len(parts) == 2 && account != "" {
			if slashIdx := strings.Index(parts[1], "/"); slashIdx > 0 {
				roleName := parts[1][:slashIdx]
				key := account + "/" + roleName
				if roleAccountNameSet[key] {
					if issuerARN != "" {
						return issuerARN, true
					}
					return fmt.Sprintf("arn:aws:iam::%s:role/%s", account, roleName), true
				}
			}
		}
	}

	return "", false
}

// extractAccountFromARN extracts the AWS account ID from an ARN.
// arn:aws:sts::123456789012:assumed-role/... -> 123456789012
func extractAccountFromARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) >= 5 {
		return parts[4]
	}
	return ""
}

// buildRoleAccountNameSet extracts account/role-name pairs from IAM role ARNs.
// arn:aws:iam::123456789012:role/ROLE_NAME -> "123456789012/ROLE_NAME"
func buildRoleAccountNameSet(roleARNs []string) map[string]bool {
	names := make(map[string]bool, len(roleARNs))
	for _, arn := range roleARNs {
		account := extractAccountFromARN(arn)
		if idx := strings.LastIndex(arn, "/"); idx >= 0 && account != "" {
			names[account+"/"+arn[idx+1:]] = true
		}
	}
	return names
}

type cloudTrailLookupAPI interface {
	LookupEvents(ctx context.Context, input *cloudtrail.LookupEventsInput, optFns ...func(*cloudtrail.Options)) (*cloudtrail.LookupEventsOutput, error)
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

	return filterCloudTrailEvents(ctx, ctClient, startTime, endTime, roleARNs)
}

func filterCloudTrailEvents(ctx context.Context, ctClient cloudTrailLookupAPI, startTime, endTime time.Time, roleARNs []string) (*CloudTrailPermissionReport, error) {
	roleARNSet := make(map[string]bool, len(roleARNs))
	for _, arn := range roleARNs {
		roleARNSet[arn] = true
	}
	roleNameSet := buildRoleAccountNameSet(roleARNs)

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
// Returns discovered role ARNs and any warnings from failed list operations.
func discoverHCPRoleARNs(ctx context.Context, client crclient.Client, hcpNamespace string) ([]string, []string) {
	var roleARNs []string
	var warnings []string
	seen := make(map[string]bool)

	addRole := func(arn string) {
		if arn != "" && !seen[arn] {
			seen[arn] = true
			roleARNs = append(roleARNs, arn)
		}
	}

	saList := &corev1.ServiceAccountList{}
	if err := client.List(ctx, saList, crclient.InNamespace(hcpNamespace)); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to list ServiceAccounts in %s: %v", hcpNamespace, err))
	} else {
		for _, sa := range saList.Items {
			addRole(sa.Annotations["eks.amazonaws.com/role-arn"])
		}
	}

	podList := &corev1.PodList{}
	if err := client.List(ctx, podList, crclient.InNamespace(hcpNamespace)); err != nil {
		warnings = append(warnings, fmt.Sprintf("failed to list Pods in %s: %v", hcpNamespace, err))
	} else {
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

	return roleARNs, warnings
}

// RunCloudTrailPermissionCheck is a self-contained CloudTrail permission denied
// check that queries, logs results via logf, and writes a JSON artifact. It is
// safe to call from both v1 (t.Logf) and v2 (GinkgoWriter.Printf) frameworks.
func RunCloudTrailPermissionCheck(ctx context.Context, client crclient.Client, awsCreds, awsRegion string, startTime time.Time, hostedCluster *hyperv1.HostedCluster, artifactDir string, logf func(format string, args ...any)) {
	report, warnings, err := CheckCloudTrailPermissionDenied(ctx, client, awsCreds, awsRegion, startTime, hostedCluster)
	for _, w := range warnings {
		logf("warning: %s", w)
	}
	if err != nil {
		logf("warning: failed to query CloudTrail for permission denied events: %v", err)
		return
	}

	clusterKey := hostedCluster.Namespace + "/" + hostedCluster.Name
	logf("CloudTrail Permission Denied Report for HostedCluster %s", clusterKey)
	logf("  Time window: %s to %s", report.StartTime.Format(time.RFC3339), report.EndTime.Format(time.RFC3339))
	logf("  Roles checked: %s", strings.Join(report.RoleARNs, ", "))

	if len(report.Events) == 0 {
		logf("  No permission denied events found")
	} else {
		logf("error: non-fatal, found %d CloudTrail permission denied event(s) for HostedCluster %s",
			len(report.Events), clusterKey)
		for i, event := range report.Events {
			logf("error: non-fatal, [%d] %s on %s (%s) by role %s",
				i+1, event.ErrorCode, event.EventName, event.EventSource, event.RoleARN)
			if event.ErrorMessage != "" {
				logf("       Message: %s", event.ErrorMessage)
			}
		}
	}

	if artifactDir != "" {
		reportJSON, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			reportPath := filepath.Join(artifactDir, fmt.Sprintf("cloudtrail-permission-denied-%s.json", hostedCluster.Name))
			if err := os.WriteFile(reportPath, reportJSON, 0644); err != nil {
				logf("warning: failed to write CloudTrail report to %s: %v", reportPath, err)
			} else {
				logf("CloudTrail permission denied report written to %s", reportPath)
			}
		}
	}
}

// CheckCloudTrailPermissionDenied queries CloudTrail for permission denied events
// associated with the HostedCluster's IAM roles within the given time window.
// Returns the report, any discovery warnings, and any error. Callers handle
// logging and artifact writing.
func CheckCloudTrailPermissionDenied(ctx context.Context, client crclient.Client, awsCreds, awsRegion string, startTime time.Time, hostedCluster *hyperv1.HostedCluster) (*CloudTrailPermissionReport, []string, error) {
	roleARNs := extractRoleARNs(hostedCluster)

	hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	hcpRoleARNs, warnings := discoverHCPRoleARNs(ctx, client, hcpNamespace)
	seen := make(map[string]bool, len(roleARNs))
	for _, arn := range roleARNs {
		seen[arn] = true
	}
	for _, arn := range hcpRoleARNs {
		if !seen[arn] {
			roleARNs = append(roleARNs, arn)
		}
	}

	if len(roleARNs) == 0 {
		return &CloudTrailPermissionReport{StartTime: startTime, EndTime: time.Now()}, warnings, nil
	}

	endTime := time.Now()
	report, err := lookupCloudTrailPermissionDenied(ctx, awsCreds, awsRegion, startTime, endTime, roleARNs)
	return report, warnings, err
}

// NoticeCloudTrailPermissionDenied queries CloudTrail for permission denied events
// associated with the HostedCluster's IAM roles and logs any findings.
// This is a non-failing check (uses t.Logf) for informational purposes.
// Runs at most once per HostedCluster (by namespace/name) to avoid redundant
// CloudTrail API calls when multiple tests share the same cluster.
func NoticeCloudTrailPermissionDenied(t *testing.T, ctx context.Context, client crclient.Client, awsCreds, awsRegion string, startTime time.Time, hostedCluster *hyperv1.HostedCluster) {
	t.Run("NoticeCloudTrailPermissionDenied", func(t *testing.T) {
		clusterKey := hostedCluster.Namespace + "/" + hostedCluster.Name
		if _, alreadyRan := cloudTrailChecked.LoadOrStore(clusterKey, true); alreadyRan {
			t.Logf("CloudTrail check already ran for %s, skipping", clusterKey)
			return
		}

		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()

		RunCloudTrailPermissionCheck(checkCtx, client, awsCreds, awsRegion, startTime,
			hostedCluster, os.Getenv("ARTIFACT_DIR"), t.Logf)
	})
}
