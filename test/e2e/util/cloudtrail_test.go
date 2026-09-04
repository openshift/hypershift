package util

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cttypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/smithy-go"
)

func TestMatchesRole(t *testing.T) {
	roleARNSet := map[string]bool{
		"arn:aws:iam::123456789012:role/my-ingress-role": true,
		"arn:aws:iam::123456789012:role/my-storage-role": true,
	}
	roleAccountNameSet := map[string]bool{
		"123456789012/my-ingress-role": true,
		"123456789012/my-storage-role": true,
	}

	tests := []struct {
		name        string
		payload     cloudTrailEventPayload
		wantARN     string
		wantMatched bool
	}{
		{
			name: "When sessionIssuer ARN matches a role ARN, it should return the issuer ARN",
			payload: func() cloudTrailEventPayload {
				p := cloudTrailEventPayload{}
				p.UserIdentity.SessionContext.SessionIssuer.ARN = "arn:aws:iam::123456789012:role/my-ingress-role"
				return p
			}(),
			wantARN:     "arn:aws:iam::123456789012:role/my-ingress-role",
			wantMatched: true,
		},
		{
			name: "When identity ARN contains assumed-role with matching role name and account, it should return the issuer ARN",
			payload: func() cloudTrailEventPayload {
				p := cloudTrailEventPayload{}
				p.UserIdentity.ARN = "arn:aws:sts::123456789012:assumed-role/my-storage-role/i-0abc123"
				p.UserIdentity.SessionContext.SessionIssuer.ARN = "arn:aws:iam::123456789012:role/my-storage-role"
				return p
			}(),
			wantARN:     "arn:aws:iam::123456789012:role/my-storage-role",
			wantMatched: true,
		},
		{
			name: "When identity ARN contains assumed-role with matching name but no issuer ARN, it should return the reconstructed role ARN",
			payload: func() cloudTrailEventPayload {
				p := cloudTrailEventPayload{}
				p.UserIdentity.ARN = "arn:aws:sts::123456789012:assumed-role/my-ingress-role/session-uuid"
				return p
			}(),
			wantARN:     "arn:aws:iam::123456789012:role/my-ingress-role",
			wantMatched: true,
		},
		{
			name: "When role name matches but account differs, it should not match",
			payload: func() cloudTrailEventPayload {
				p := cloudTrailEventPayload{}
				p.UserIdentity.ARN = "arn:aws:sts::999999999999:assumed-role/my-ingress-role/session"
				return p
			}(),
			wantARN:     "",
			wantMatched: false,
		},
		{
			name: "When neither issuer nor identity ARN match any role, it should return empty and false",
			payload: func() cloudTrailEventPayload {
				p := cloudTrailEventPayload{}
				p.UserIdentity.ARN = "arn:aws:sts::123456789012:assumed-role/unrelated-role/session"
				p.UserIdentity.SessionContext.SessionIssuer.ARN = "arn:aws:iam::123456789012:role/unrelated-role"
				return p
			}(),
			wantARN:     "",
			wantMatched: false,
		},
		{
			name: "When identity ARN has no assumed-role segment, it should not match",
			payload: func() cloudTrailEventPayload {
				p := cloudTrailEventPayload{}
				p.UserIdentity.ARN = "arn:aws:iam::123456789012:user/some-user"
				return p
			}(),
			wantARN:     "",
			wantMatched: false,
		},
		{
			name:        "When all ARN fields are empty, it should not match",
			payload:     cloudTrailEventPayload{},
			wantARN:     "",
			wantMatched: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotARN, gotMatched := matchesRole(tt.payload, roleARNSet, roleAccountNameSet)
			if gotARN != tt.wantARN {
				t.Errorf("matchesRole() ARN = %q, want %q", gotARN, tt.wantARN)
			}
			if gotMatched != tt.wantMatched {
				t.Errorf("matchesRole() matched = %v, want %v", gotMatched, tt.wantMatched)
			}
		})
	}
}

func TestBuildRoleAccountNameSet(t *testing.T) {
	tests := []struct {
		name     string
		roleARNs []string
		want     map[string]bool
	}{
		{
			name:     "When given standard IAM role ARNs, it should extract account/role-name pairs",
			roleARNs: []string{"arn:aws:iam::123456789012:role/my-ingress-role", "arn:aws:iam::123456789012:role/my-storage-role"},
			want:     map[string]bool{"123456789012/my-ingress-role": true, "123456789012/my-storage-role": true},
		},
		{
			name:     "When given ARNs with path prefixes, it should extract the final name segment with account",
			roleARNs: []string{"arn:aws:iam::123456789012:role/service-role/my-custom-role"},
			want:     map[string]bool{"123456789012/my-custom-role": true},
		},
		{
			name:     "When given roles from different accounts, it should keep them separate",
			roleARNs: []string{"arn:aws:iam::111111111111:role/shared-name", "arn:aws:iam::222222222222:role/shared-name"},
			want:     map[string]bool{"111111111111/shared-name": true, "222222222222/shared-name": true},
		},
		{
			name:     "When given an empty slice, it should return an empty set",
			roleARNs: []string{},
			want:     map[string]bool{},
		},
		{
			name:     "When given an ARN with no slash, it should not add any entry",
			roleARNs: []string{"no-slash-arn"},
			want:     map[string]bool{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRoleAccountNameSet(tt.roleARNs)
			if len(got) != len(tt.want) {
				t.Errorf("buildRoleAccountNameSet() returned %d entries, want %d; got %v", len(got), len(tt.want), got)
			}
			for k := range tt.want {
				if _, ok := got[k]; !ok {
					t.Errorf("buildRoleAccountNameSet() missing expected key %q", k)
				}
			}
		})
	}
}

func TestExtractAccountFromARN(t *testing.T) {
	tests := []struct {
		name string
		arn  string
		want string
	}{
		{
			name: "When given a standard IAM role ARN, it should extract the account ID",
			arn:  "arn:aws:iam::123456789012:role/my-role",
			want: "123456789012",
		},
		{
			name: "When given an STS assumed-role ARN, it should extract the account ID",
			arn:  "arn:aws:sts::987654321098:assumed-role/my-role/session",
			want: "987654321098",
		},
		{
			name: "When given a malformed ARN with too few segments, it should return empty",
			arn:  "arn:aws:iam",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractAccountFromARN(tt.arn)
			if got != tt.want {
				t.Errorf("extractAccountFromARN() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractRoleARNs(t *testing.T) {
	tests := []struct {
		name          string
		hostedCluster *hyperv1.HostedCluster
		wantLen       int
		wantContains  []string
	}{
		{
			name: "When AWS platform is nil, it should return nil",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{},
				},
			},
			wantLen: 0,
		},
		{
			name: "When all role ARNs are populated, it should return all unique ARNs",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							RolesRef: hyperv1.AWSRolesRef{
								IngressARN:              "arn:aws:iam::123456789012:role/ingress",
								ImageRegistryARN:        "arn:aws:iam::123456789012:role/registry",
								StorageARN:              "arn:aws:iam::123456789012:role/storage",
								NetworkARN:              "arn:aws:iam::123456789012:role/network",
								KubeCloudControllerARN:  "arn:aws:iam::123456789012:role/kube-cloud",
								NodePoolManagementARN:   "arn:aws:iam::123456789012:role/nodepool",
								ControlPlaneOperatorARN: "arn:aws:iam::123456789012:role/cpo",
							},
						},
					},
				},
			},
			wantLen: 7,
			wantContains: []string{
				"arn:aws:iam::123456789012:role/ingress",
				"arn:aws:iam::123456789012:role/cpo",
			},
		},
		{
			name: "When some role ARNs are duplicated, it should deduplicate them",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							RolesRef: hyperv1.AWSRolesRef{
								IngressARN:              "arn:aws:iam::123456789012:role/shared",
								ImageRegistryARN:        "arn:aws:iam::123456789012:role/shared",
								StorageARN:              "arn:aws:iam::123456789012:role/storage",
								NetworkARN:              "arn:aws:iam::123456789012:role/shared",
								KubeCloudControllerARN:  "arn:aws:iam::123456789012:role/storage",
								NodePoolManagementARN:   "",
								ControlPlaneOperatorARN: "",
							},
						},
					},
				},
			},
			wantLen:      2,
			wantContains: []string{"arn:aws:iam::123456789012:role/shared", "arn:aws:iam::123456789012:role/storage"},
		},
		{
			name: "When all role ARNs are empty, it should return an empty slice",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							RolesRef: hyperv1.AWSRolesRef{},
						},
					},
				},
			},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractRoleARNs(tt.hostedCluster)
			if len(got) != tt.wantLen {
				t.Errorf("extractRoleARNs() returned %d ARNs, want %d; got: %v", len(got), tt.wantLen, got)
			}
			for _, want := range tt.wantContains {
				found := false
				for _, arn := range got {
					if arn == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("extractRoleARNs() result missing expected ARN %q; got: %v", want, got)
				}
			}
		})
	}
}

type mockCloudTrailClient struct {
	pages    []cloudtrail.LookupEventsOutput
	err      error
	errAfter int // return err after this many successful pages (0 = always error)
	calls    int
}

func (m *mockCloudTrailClient) LookupEvents(_ context.Context, _ *cloudtrail.LookupEventsInput, _ ...func(*cloudtrail.Options)) (*cloudtrail.LookupEventsOutput, error) {
	if m.err != nil && m.calls >= m.errAfter {
		return nil, m.err
	}
	if m.calls >= len(m.pages) {
		return &cloudtrail.LookupEventsOutput{}, nil
	}
	page := m.pages[m.calls]
	m.calls++
	return &page, nil
}

type throttlingError struct{}

func (e *throttlingError) Error() string                 { return "throttled" }
func (e *throttlingError) ErrorCode() string             { return "ThrottlingException" }
func (e *throttlingError) ErrorMessage() string          { return "Rate exceeded" }
func (e *throttlingError) ErrorFault() smithy.ErrorFault { return smithy.FaultServer }

func makeEvent(eventName, errorCode, roleARN string) cttypes.Event {
	eventTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	payload := cloudTrailEventPayload{
		EventName:   eventName,
		EventSource: "ec2.amazonaws.com",
		ErrorCode:   errorCode,
	}
	payload.UserIdentity.SessionContext.SessionIssuer.ARN = roleARN
	data, _ := json.Marshal(payload)
	s := string(data)
	return cttypes.Event{
		CloudTrailEvent: &s,
		EventTime:       &eventTime,
	}
}

func TestFilterCloudTrailEvents(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 1, 23, 59, 59, 0, time.UTC)
	roleARNs := []string{"arn:aws:iam::123456789012:role/my-role"}

	tests := []struct {
		name       string
		client     *mockCloudTrailClient
		wantEvents int
		wantErr    bool
	}{
		{
			name: "When CloudTrail returns matching permission denied events, it should include them in the report",
			client: &mockCloudTrailClient{
				pages: []cloudtrail.LookupEventsOutput{
					{Events: []cttypes.Event{
						makeEvent("RunInstances", "AccessDenied", "arn:aws:iam::123456789012:role/my-role"),
						makeEvent("CreateVolume", "UnauthorizedOperation", "arn:aws:iam::123456789012:role/my-role"),
					}},
				},
			},
			wantEvents: 2,
		},
		{
			name: "When events do not match the target role, it should exclude them",
			client: &mockCloudTrailClient{
				pages: []cloudtrail.LookupEventsOutput{
					{Events: []cttypes.Event{
						makeEvent("RunInstances", "AccessDenied", "arn:aws:iam::999999999999:role/other-role"),
					}},
				},
			},
			wantEvents: 0,
		},
		{
			name: "When events have non-denied error codes, it should exclude them",
			client: &mockCloudTrailClient{
				pages: []cloudtrail.LookupEventsOutput{
					{Events: []cttypes.Event{
						makeEvent("RunInstances", "InvalidParameterValue", "arn:aws:iam::123456789012:role/my-role"),
					}},
				},
			},
			wantEvents: 0,
		},
		{
			name: "When CloudTrail returns multiple pages, it should paginate through all",
			client: &mockCloudTrailClient{
				pages: []cloudtrail.LookupEventsOutput{
					{
						Events:    []cttypes.Event{makeEvent("RunInstances", "AccessDenied", "arn:aws:iam::123456789012:role/my-role")},
						NextToken: aws.String("page2"),
					},
					{
						Events: []cttypes.Event{makeEvent("CreateVolume", "AccessDenied", "arn:aws:iam::123456789012:role/my-role")},
					},
				},
			},
			wantEvents: 2,
		},
		{
			name: "When CloudTrail returns a throttling error on the first call, it should return empty results without error",
			client: &mockCloudTrailClient{
				err: &throttlingError{},
			},
			wantEvents: 0,
			wantErr:    false,
		},
		{
			name: "When CloudTrail throttles after a successful page, it should retain events from the first page",
			client: &mockCloudTrailClient{
				pages: []cloudtrail.LookupEventsOutput{
					{
						Events:    []cttypes.Event{makeEvent("RunInstances", "AccessDenied", "arn:aws:iam::123456789012:role/my-role")},
						NextToken: aws.String("page2"),
					},
				},
				err:      &throttlingError{},
				errAfter: 1,
			},
			wantEvents: 1,
			wantErr:    false,
		},
		{
			name: "When CloudTrail returns a non-throttling error, it should return an error",
			client: &mockCloudTrailClient{
				err: fmt.Errorf("connection refused"),
			},
			wantErr: true,
		},
		{
			name: "When an event has malformed JSON, it should skip it and continue",
			client: &mockCloudTrailClient{
				pages: []cloudtrail.LookupEventsOutput{
					{Events: []cttypes.Event{
						{CloudTrailEvent: aws.String("{invalid json")},
						makeEvent("RunInstances", "AccessDenied", "arn:aws:iam::123456789012:role/my-role"),
					}},
				},
			},
			wantEvents: 1,
		},
		{
			name: "When an event has nil CloudTrailEvent, it should skip it",
			client: &mockCloudTrailClient{
				pages: []cloudtrail.LookupEventsOutput{
					{Events: []cttypes.Event{
						{CloudTrailEvent: nil},
						makeEvent("RunInstances", "AccessDenied", "arn:aws:iam::123456789012:role/my-role"),
					}},
				},
			},
			wantEvents: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report, err := filterCloudTrailEvents(context.Background(), tt.client, start, end, roleARNs)
			if tt.wantErr {
				if err == nil {
					t.Error("filterCloudTrailEvents() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("filterCloudTrailEvents() unexpected error: %v", err)
				return
			}
			if len(report.Events) != tt.wantEvents {
				t.Errorf("filterCloudTrailEvents() returned %d events, want %d", len(report.Events), tt.wantEvents)
			}
		})
	}
}
