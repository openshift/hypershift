package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"

	"k8s.io/apimachinery/pkg/util/sets"
)

type lookupPage struct {
	output *cloudtrail.LookupEventsOutput
	err    error
}

type fakeCloudTrail struct {
	pages   []lookupPage
	inputs  []*cloudtrail.LookupEventsInput
	callNum int
}

func (f *fakeCloudTrail) LookupEvents(_ context.Context, input *cloudtrail.LookupEventsInput, _ ...func(*cloudtrail.Options)) (*cloudtrail.LookupEventsOutput, error) {
	request := *input
	f.inputs = append(f.inputs, &request)
	if f.callNum >= len(f.pages) {
		return nil, errors.New("unexpected extra LookupEvents call")
	}
	page := f.pages[f.callNum]
	f.callNum++
	return page.output, page.err
}

func TestPrepareRegions(t *testing.T) {
	commercialRole := "arn:aws:iam::123456789012:role/ControlPlane"
	govRole := "arn:aws-us-gov:iam::123456789012:role/ControlPlane"
	chinaRole := "arn:aws-cn:iam::123456789012:role/ControlPlane"
	for _, tc := range []struct {
		name    string
		regions []string
		roles   []string
		want    []string
		wantErr string
	}{
		{
			name:    "When commercial regions repeat, it should deduplicate them and include the global region",
			regions: []string{"us-west-2", "us-west-2", "us-east-1"},
			roles:   []string{commercialRole},
			want:    []string{"us-east-1", "us-west-2"},
		},
		{
			name:    "When roles and regions are in GovCloud, it should include only GovCloud global events",
			regions: []string{"us-gov-east-1"},
			roles:   []string{govRole},
			want:    []string{"us-gov-east-1", "us-gov-west-1"},
		},
		{
			name:    "When roles and regions are in China, it should include both global-event regions",
			regions: []string{"cn-northwest-1"},
			roles:   []string{chinaRole},
			want:    []string{"cn-north-1", "cn-northwest-1"},
		},
		{
			name:  "When only commercial roles are inventoried, it should derive the commercial global region",
			roles: []string{commercialRole},
			want:  []string{"us-east-1"},
		},
		{
			name:  "When only GovCloud roles are inventoried, it should derive the GovCloud global region",
			roles: []string{govRole},
			want:  []string{"us-gov-west-1"},
		},
		{
			name:  "When only China roles are inventoried, it should derive both China global regions",
			roles: []string{chinaRole},
			want:  []string{"cn-north-1", "cn-northwest-1"},
		},
		{
			name:    "When only regions are supplied, it should derive their partition",
			regions: []string{"eu-central-2"},
			want:    []string{"eu-central-2", "us-east-1"},
		},
		{
			name:    "When a region contains a hostname injection, it should reject it",
			regions: []string{"us-west-2@attacker.example/"},
			roles:   []string{commercialRole},
			wantErr: "unsupported CloudTrail region",
		},
		{
			name:    "When a region looks valid but is unknown, it should reject it",
			regions: []string{"us-west-999"},
			wantErr: "unsupported CloudTrail region",
		},
		{
			name:    "When a pseudo-region is supplied, it should reject it",
			regions: []string{"aws-global"},
			wantErr: "unsupported CloudTrail region",
		},
		{
			name:    "When a deprecated FIPS region alias is supplied, it should reject it",
			regions: []string{"fips-us-east-1"},
			wantErr: "unsupported CloudTrail region",
		},
		{
			name:    "When a region has surrounding whitespace, it should reject it without normalizing",
			regions: []string{" us-west-2 "},
			wantErr: "unsupported CloudTrail region",
		},
		{
			name:    "When an availability zone is supplied as a region, it should reject it",
			regions: []string{"us-west-2a"},
			wantErr: "unsupported CloudTrail region",
		},
		{
			name:    "When a region is empty, it should reject it",
			regions: []string{""},
			wantErr: "unsupported CloudTrail region",
		},
		{
			name:    "When regions span partitions, it should reject them",
			regions: []string{"us-east-1", "cn-north-1"},
			wantErr: "partition",
		},
		{
			name:    "When roles span partitions, it should reject them",
			roles:   []string{commercialRole, govRole},
			wantErr: "partitions",
		},
		{
			name:    "When a region and role partition differ, it should reject them",
			regions: []string{"us-east-1"},
			roles:   []string{govRole},
			wantErr: "partition",
		},
		{
			name:    "When a role partition is unsupported, it should reject it",
			roles:   []string{"arn:aws-iso:iam::123456789012:role/ControlPlane"},
			wantErr: "unsupported AWS partition",
		},
		{
			name:    "When a role ARN cannot be parsed, it should reject it",
			roles:   []string{"not-an-arn"},
			wantErr: "invalid IAM role ARN",
		},
		{
			name:    "When an ARN is not an IAM role, it should reject it",
			roles:   []string{"arn:aws:s3::123456789012:role/ControlPlane"},
			wantErr: "invalid IAM role ARN",
		},
		{
			name:    "When an IAM role ARN omits its account, it should reject it",
			roles:   []string{"arn:aws:iam:::role/ControlPlane"},
			wantErr: "invalid IAM role ARN",
		},
		{
			name:    "When an IAM role ARN has no role name, it should reject it",
			roles:   []string{"arn:aws:iam::123456789012:role/"},
			wantErr: "invalid IAM role ARN",
		},
		{
			name:    "When neither regions nor roles are supplied, it should reject the missing partition",
			wantErr: "no AWS partition",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			regions := append([]string(nil), tc.regions...)
			got, err := prepareRegions(regions, tc.roles)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("regions = %v, want %v", got, tc.want)
			}
			if !reflect.DeepEqual(regions, tc.regions) {
				t.Errorf("input regions changed: got %v, want %v", regions, tc.regions)
			}
		})
	}
}

func TestQueryCloudTrail(t *testing.T) {
	roleARN := "arn:aws:iam::123456789012:role/ControlPlane"
	for _, tc := range []struct {
		name       string
		regions    []string
		roles      []string
		failure    string
		wantCalls  int32
		wantEvents int
		wantErrors []string
	}{
		{
			name:       "When all pages succeed, it should collect events across pages and regions",
			regions:    []string{"us-west-2"},
			wantCalls:  3,
			wantEvents: 3,
		},
		{
			name:       "When a later page fails, it should preserve earlier denials and query later regions",
			regions:    []string{"us-west-2"},
			failure:    "later-page",
			wantCalls:  3,
			wantEvents: 2,
			wantErrors: []string{"ThrottlingException", "us-east-1"},
		},
		{
			name:       "When the first region fails, it should still collect later-region events",
			regions:    []string{"us-west-2"},
			failure:    "first-region",
			wantCalls:  2,
			wantEvents: 1,
			wantErrors: []string{"ThrottlingException", "us-east-1"},
		},
		{
			name:       "When every region fails, it should return joined regional errors",
			regions:    []string{"us-west-2"},
			failure:    "all-regions",
			wantCalls:  2,
			wantErrors: []string{"us-east-1", "us-west-2"},
		},
		{
			name:       "When a malicious region follows a valid region, it should reject all requests before AWS setup",
			regions:    []string{"us-east-1", "us-west-2@attacker.example/"},
			wantErrors: []string{"unsupported CloudTrail region", "us-west-2@attacker.example/"},
		},
		{
			name:       "When a malicious region precedes a valid region, it should reject all requests before AWS setup",
			regions:    []string{"us-west-2@attacker.example/", "us-east-1"},
			wantErrors: []string{"unsupported CloudTrail region", "us-west-2@attacker.example/"},
		},
		{
			name:       "When roles span partitions, it should reject all requests before AWS setup",
			regions:    []string{"us-east-1"},
			roles:      []string{roleARN, "arn:aws-cn:iam::123456789012:role/ControlPlane"},
			wantErrors: []string{"partitions"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			setupCloudTrailTest(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				credential := strings.Split(r.Header.Get("Authorization"), "/")
				if len(credential) < 5 || !strings.Contains(credential[0], "Credential=AKIDEXAMPLE") {
					t.Errorf("unexpected signing credentials: %q", r.Header.Get("Authorization"))
					http.Error(w, "invalid credentials", http.StatusBadRequest)
					return
				}
				region := credential[2]
				var input struct{ NextToken string }
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Errorf("decode request: %v", err)
					http.Error(w, "invalid request", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/x-amz-json-1.1")
				if tc.failure == "all-regions" || region == "us-east-1" && (tc.failure == "first-region" || tc.failure == "later-page" && input.NextToken != "") {
					w.WriteHeader(http.StatusBadRequest)
					if _, err := w.Write([]byte(`{"__type":"ThrottlingException","message":"retry later"}`)); err != nil {
						t.Errorf("write error response: %v", err)
					}
					return
				}
				response := map[string]any{
					"Events": []map[string]any{{"CloudTrailEvent": eventJSON(`"sessionContext":{"sessionIssuer":{"arn":"` + roleARN + `"}}`)}},
				}
				if region == "us-east-1" && input.NextToken == "" {
					response["NextToken"] = "next-page"
				}
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Errorf("write response: %v", err)
				}
			})
			roles := tc.roles
			if roles == nil {
				roles = []string{roleARN}
			}
			report, err := queryCloudTrail(t.Context(), time.Unix(0, 0), time.Unix(10, 0), tc.regions, roles)
			if (err != nil) != (len(tc.wantErrors) > 0) {
				t.Fatalf("error = %v, want errors %v", err, tc.wantErrors)
			}
			for _, message := range tc.wantErrors {
				if !strings.Contains(err.Error(), message) {
					t.Errorf("error = %v, want %q", err, message)
				}
			}
			if calls.Load() != tc.wantCalls || len(report.Events) != tc.wantEvents {
				t.Fatalf("got %d calls and %d events, want %d calls and %d events", calls.Load(), len(report.Events), tc.wantCalls, tc.wantEvents)
			}
			if tc.wantEvents > 0 && report.Events[len(report.Events)-1].Region != "us-west-2" {
				t.Errorf("later-region event missing: %#v", report.Events)
			}
		})
	}
}

func TestMatchEventRole(t *testing.T) {
	for _, tc := range []struct {
		name      string
		partition string
		issuer    string
		identity  string
		wantMatch bool
	}{
		{
			name:      "When the issuer matches exactly, it should use the inventoried role",
			partition: "aws",
			issuer:    "arn:aws:iam::123456789012:role/path/ControlPlane",
			wantMatch: true,
		},
		{
			name:      "When a GovCloud session has no issuer, it should preserve the role partition and path",
			partition: "aws-us-gov",
			identity:  "arn:aws-us-gov:sts::123456789012:assumed-role/ControlPlane/session",
			wantMatch: true,
		},
		{
			name:      "When a China session has no issuer, it should preserve the role partition and path",
			partition: "aws-cn",
			identity:  "arn:aws-cn:sts::123456789012:assumed-role/ControlPlane/session",
			wantMatch: true,
		},
		{
			name:      "When a same-name session belongs to another partition, it should not match",
			partition: "aws-us-gov",
			identity:  "arn:aws:sts::123456789012:assumed-role/ControlPlane/session",
		},
		{
			name:      "When a same-name session belongs to another account, it should not match",
			partition: "aws",
			identity:  "arn:aws:sts::999999999999:assumed-role/ControlPlane/session",
		},
		{
			name:      "When a session uses another role name, it should not match",
			partition: "aws",
			identity:  "arn:aws:sts::123456789012:assumed-role/OtherRole/session",
		},
		{
			name:      "When the caller is not an STS session, it should not match by role name",
			partition: "aws",
			identity:  "arn:aws:iam::123456789012:assumed-role/ControlPlane/session",
		},
		{
			name:      "When the caller ARN is malformed, it should not match",
			partition: "aws",
			identity:  "not-an-arn",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roleARN := fmt.Sprintf("arn:%s:iam::123456789012:role/path/ControlPlane", tc.partition)
			var payload cloudTrailEventPayload
			payload.UserIdentity.ARN = tc.identity
			payload.UserIdentity.SessionContext.SessionIssuer.ARN = tc.issuer
			got, ok := matchEventRole(payload, sets.New(roleARN), map[string]string{tc.partition + "/123456789012/ControlPlane": roleARN})
			if ok != tc.wantMatch || tc.wantMatch && got != roleARN || !tc.wantMatch && got != "" {
				t.Errorf("matched role = %q/%t, want %q/%t", got, ok, roleARN, tc.wantMatch)
			}
		})
	}
}

func TestLookupPermissionDeniedFiltersByCodeAndRole(t *testing.T) {
	roleARN := "arn:aws:iam::123456789012:role/ControlPlane"
	issuedByRole := eventJSON(`"sessionContext":{"sessionIssuer":{"arn":"` + roleARN + `"}}`)
	assumedRole := eventJSON(`"arn":"arn:aws:sts::123456789012:assumed-role/ControlPlane/session-1"`)
	otherRole := eventJSON(`"sessionContext":{"sessionIssuer":{"arn":"arn:aws:iam::999999999999:role/ControlPlane"}}`)
	allowedEvent := eventJSONWithCode("Throttling", `"sessionContext":{"sessionIssuer":{"arn":"`+roleARN+`"}}`)

	client := &fakeCloudTrail{pages: []lookupPage{{output: &cloudtrail.LookupEventsOutput{Events: []types.Event{
		{CloudTrailEvent: aws.String(issuedByRole), EventTime: aws.Time(time.Unix(1, 0))},
		{CloudTrailEvent: aws.String(assumedRole)},
		{CloudTrailEvent: aws.String(otherRole)},
		{CloudTrailEvent: aws.String(allowedEvent)},
	}}}}}
	start, end := time.Unix(0, 0), time.Unix(10, 0)
	events, err := lookupPermissionDenied(context.Background(), client, "us-west-2", start, end, []string{roleARN})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected two matching denied events, got %d", len(events))
	}
	if events[0].RoleARN != roleARN || events[0].Region != "us-west-2" {
		t.Fatalf("unexpected event: %#v", events[0])
	}
	if client.callNum != 1 {
		t.Fatalf("expected one LookupEvents call, got %d", client.callNum)
	}
}

func TestLookupPermissionDeniedPaginates(t *testing.T) {
	roleARN := "arn:aws:iam::123456789012:role/ControlPlane"
	firstToken := "next-page"
	client := &fakeCloudTrail{pages: []lookupPage{
		{output: &cloudtrail.LookupEventsOutput{NextToken: aws.String(firstToken)}},
		{output: &cloudtrail.LookupEventsOutput{Events: []types.Event{{CloudTrailEvent: aws.String(eventJSON(`"sessionContext":{"sessionIssuer":{"arn":"` + roleARN + `"}}`))}}}},
	}}
	events, err := lookupPermissionDenied(context.Background(), client, "us-east-1", time.Unix(0, 0), time.Unix(10, 0), []string{roleARN})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || client.callNum != 2 {
		t.Fatalf("expected one event over two pages, got %d event(s), %d calls", len(events), client.callNum)
	}
	if client.inputs[1].NextToken == nil || *client.inputs[1].NextToken != firstToken {
		t.Fatalf("second request did not use next token: %#v", client.inputs[1].NextToken)
	}
}

func TestLookupPermissionDeniedReturnsAPIError(t *testing.T) {
	wantErr := errors.New("lookup failed")
	client := &fakeCloudTrail{pages: []lookupPage{{err: wantErr}}}
	_, err := lookupPermissionDenied(context.Background(), client, "us-east-1", time.Time{}, time.Now(), nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped lookup error, got %v", err)
	}
}

func TestRoleAccountAndName(t *testing.T) {
	account, name := roleAccountAndName("arn:aws:iam::123456789012:role/path/to/ControlPlane")
	if account != "123456789012" || name != "ControlPlane" {
		t.Fatalf("unexpected role identity: %q/%q", account, name)
	}
	account, name = assumedRoleAccountAndName("arn:aws:sts::123456789012:assumed-role/ControlPlane/session")
	if account != "123456789012" || name != "ControlPlane" {
		t.Fatalf("unexpected assumed role identity: %q/%q", account, name)
	}
}

func eventJSON(identityFields string) string {
	return eventJSONWithCode("AccessDeniedException", identityFields)
}

func eventJSONWithCode(code, identityFields string) string {
	return fmt.Sprintf(`{"eventName":"CreateThing","eventSource":"service.amazonaws.com","errorCode":%q,"errorMessage":"denied","userIdentity":{%s}}`, code, identityFields)
}
