package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/smithy-go"
)

const globalServicesRegion = "us-east-1"

var permissionDeniedCodes = map[string]struct{}{
	"AccessDenied":                 {},
	"AccessDeniedException":        {},
	"Client.UnauthorizedAccess":    {},
	"Client.UnauthorizedOperation": {},
	"UnauthorizedOperation":        {},
}

type permissionDeniedEvent struct {
	Region      string    `json:"region"`
	EventTime   time.Time `json:"eventTime"`
	EventName   string    `json:"eventName"`
	EventSource string    `json:"eventSource"`
	ErrorCode   string    `json:"errorCode"`
	RoleARN     string    `json:"roleARN"`
}

type cloudTrailReport struct {
	StartTime time.Time               `json:"startTime"`
	EndTime   time.Time               `json:"endTime"`
	Regions   []string                `json:"regions"`
	RoleARNs  []string                `json:"roleARNs"`
	Events    []permissionDeniedEvent `json:"events"`
	Partial   bool                    `json:"partial,omitempty"`
	Warnings  []string                `json:"warnings,omitempty"`
}

type cloudTrailEventPayload struct {
	EventName    string `json:"eventName"`
	EventSource  string `json:"eventSource"`
	ErrorCode    string `json:"errorCode"`
	UserIdentity struct {
		ARN            string `json:"arn"`
		SessionContext struct {
			SessionIssuer struct {
				ARN string `json:"arn"`
			} `json:"sessionIssuer"`
		} `json:"sessionContext"`
	} `json:"userIdentity"`
}

type cloudTrailLookup interface {
	LookupEvents(context.Context, *cloudtrail.LookupEventsInput, ...func(*cloudtrail.Options)) (*cloudtrail.LookupEventsOutput, error)
}

func queryCloudTrail(ctx context.Context, start, end time.Time, regions, roleARNs []string) (cloudTrailReport, error) {
	report := cloudTrailReport{
		StartTime: start,
		EndTime:   end,
		Regions:   regions,
		RoleARNs:  roleARNs,
		Events:    []permissionDeniedEvent{},
	}
	var queryErrors []error
	for _, region := range regions {
		cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
		if err != nil {
			queryErrors = append(queryErrors, fmt.Errorf("load AWS configuration for %s: %w", region, err))
			continue
		}
		client := cloudtrail.NewFromConfig(cfg)
		events, err := lookupPermissionDenied(ctx, client, region, start, end, roleARNs)
		if err != nil {
			queryErrors = append(queryErrors, err)
			continue
		}
		report.Events = append(report.Events, events...)
	}
	return report, errors.Join(queryErrors...)
}

func lookupPermissionDenied(ctx context.Context, client cloudTrailLookup, region string, start, end time.Time, roleARNs []string) ([]permissionDeniedEvent, error) {
	roleSet := make(map[string]struct{}, len(roleARNs))
	roleAccountNames := make(map[string]struct{}, len(roleARNs))
	for _, roleARN := range roleARNs {
		roleSet[roleARN] = struct{}{}
		if account, name := roleAccountAndName(roleARN); account != "" && name != "" {
			roleAccountNames[account+"/"+name] = struct{}{}
		}
	}

	input := &cloudtrail.LookupEventsInput{
		StartTime:  aws.Time(start),
		EndTime:    aws.Time(end),
		MaxResults: aws.Int32(50),
	}
	var events []permissionDeniedEvent
	for {
		output, err := client.LookupEvents(ctx, input)
		if err != nil {
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && apiErr.ErrorCode() == "ThrottlingException" {
				return events, fmt.Errorf("CloudTrail throttled request in %s: %w", region, err)
			}
			return events, fmt.Errorf("lookup CloudTrail events in %s: %w", region, err)
		}
		for _, event := range output.Events {
			if event.CloudTrailEvent == nil {
				continue
			}
			var payload cloudTrailEventPayload
			if err := json.Unmarshal([]byte(*event.CloudTrailEvent), &payload); err != nil {
				continue
			}
			if _, denied := permissionDeniedCodes[payload.ErrorCode]; !denied {
				continue
			}
			roleARN, ok := matchEventRole(payload, roleSet, roleAccountNames)
			if !ok {
				continue
			}
			eventTime := time.Time{}
			if event.EventTime != nil {
				eventTime = *event.EventTime
			}
			events = append(events, permissionDeniedEvent{
				Region:      region,
				EventTime:   eventTime,
				EventName:   payload.EventName,
				EventSource: payload.EventSource,
				ErrorCode:   payload.ErrorCode,
				RoleARN:     roleARN,
			})
		}
		if output.NextToken == nil || aws.ToString(output.NextToken) == "" {
			break
		}
		input.NextToken = output.NextToken
	}
	return events, nil
}

func matchEventRole(payload cloudTrailEventPayload, roles, accountNames map[string]struct{}) (string, bool) {
	issuer := payload.UserIdentity.SessionContext.SessionIssuer.ARN
	if _, ok := roles[issuer]; ok {
		return issuer, true
	}
	account, roleName := assumedRoleAccountAndName(payload.UserIdentity.ARN)
	if account == "" || roleName == "" {
		return "", false
	}
	if _, ok := accountNames[account+"/"+roleName]; !ok {
		return "", false
	}
	if issuer != "" {
		return issuer, true
	}
	return fmt.Sprintf("arn:aws:iam::%s:role/%s", account, roleName), true
}

func roleAccountAndName(arn string) (string, string) {
	parts := strings.Split(arn, ":")
	if len(parts) < 6 {
		return "", ""
	}
	resource := parts[5]
	roleIndex := strings.Index(resource, "role/")
	if roleIndex < 0 {
		return "", ""
	}
	roleName := resource[roleIndex+len("role/"):]
	if slash := strings.LastIndex(roleName, "/"); slash >= 0 {
		roleName = roleName[slash+1:]
	}
	return parts[4], roleName
}

func assumedRoleAccountAndName(arn string) (string, string) {
	parts := strings.SplitN(arn, ":assumed-role/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	arnParts := strings.Split(parts[0], ":")
	if len(arnParts) < 5 || arnParts[4] == "" {
		return "", ""
	}
	roleName := strings.SplitN(parts[1], "/", 2)[0]
	return arnParts[4], roleName
}
