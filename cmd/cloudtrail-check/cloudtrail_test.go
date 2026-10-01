package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
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
	f.inputs = append(f.inputs, input)
	if f.callNum >= len(f.pages) {
		return nil, errors.New("unexpected extra LookupEvents call")
	}
	page := f.pages[f.callNum]
	f.callNum++
	return page.output, page.err
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
