package awsutil

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/smithy-go"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/go-logr/logr"
)

// LoadBalancerClients contains the clients used to delete AWS load balancer resources.
type LoadBalancerClients struct {
	ELB   awsapi.ELBAPI
	ELBV2 awsapi.ELBV2API
}

// LoadBalancerSelector limits cleanup to a VPC and, for named cleanup, a cluster tag.
type LoadBalancerSelector struct {
	VPCID   string
	InfraID string
}

// LoadBalancerResourceType identifies the AWS API for a load balancer resource.
type LoadBalancerResourceType string

const (
	// ClassicLoadBalancerResource identifies an ELB classic load balancer.
	ClassicLoadBalancerResource LoadBalancerResourceType = "classic"
	// V2LoadBalancerResource identifies an ELBv2 load balancer.
	V2LoadBalancerResource LoadBalancerResourceType = "v2"
)

// ErrDescribeTagsAccessDenied identifies a denied ownership-tag lookup.
var ErrDescribeTagsAccessDenied = errors.New("DescribeTags access denied")

// LoadBalancerIdentity is an AWS load balancer positively verified as owned by the cluster.
type LoadBalancerIdentity struct {
	Name  string                   `json:"name"`
	Type  LoadBalancerResourceType `json:"type"`
	ARN   string                   `json:"arn,omitempty"`
	VPCID string                   `json:"vpcID"`
}

// NamedLoadBalancerObservation records whether a candidate exists and which resources are owned.
type NamedLoadBalancerObservation struct {
	Present    bool
	Unverified bool
	Owned      []LoadBalancerIdentity
	Err        error
}

// NamedLoadBalancerCleanupResult reports cleanup for one candidate name.
type NamedLoadBalancerCleanupResult struct {
	LoadBalancersRemoved bool
	Err                  error
}

// LoadBalancerCleanupResult separates per-name cleanup from the orphan target-group scan.
type LoadBalancerCleanupResult struct {
	ByName                       map[string]NamedLoadBalancerCleanupResult
	OrphanTargetGroupARNs        sets.Set[string]
	OrphanTargetGroupScanAttempt bool
	OrphanTargetGroupScanSuccess bool
	OrphanTargetGroupErr         error
}

type loadBalancerIdentityState struct {
	Present    bool
	Unverified bool
	Err        error
}

// ErrLoadBalancerOwnershipUnverified identifies a resource that failed ownership checks.
var ErrLoadBalancerOwnershipUnverified = errors.New("load balancer ownership could not be verified")

// DeleteLoadBalancers deletes load balancers and target groups selected by their VPC.
func DeleteLoadBalancers(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector, log logr.Logger) []error {
	if err := selector.validate(); err != nil {
		return []error{err}
	}

	var errs []error
	if clients.ELB != nil {
		errs = append(errs, deleteClassicLoadBalancers(ctx, clients.ELB, selector, log)...)
	}
	if clients.ELBV2 != nil {
		errs = append(errs, deleteV2LoadBalancers(ctx, clients.ELBV2, selector, log)...)
	}
	return errs
}

// DeleteLoadBalancersByName deletes only the named load balancers owned by the hosted cluster.
// ELBv2 resources are deleted in dependency order for each load balancer.
func DeleteLoadBalancersByName(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector, names []string, log logr.Logger) (bool, error) {
	names = uniqueNonEmpty(names)
	if len(names) == 0 {
		return true, nil
	}
	result, err := cleanupNamedLoadBalancers(ctx, clients, selector, names, clients.ELBV2 != nil, log)
	if err != nil {
		return false, err
	}
	var errs []error
	removed := !result.OrphanTargetGroupScanAttempt || result.OrphanTargetGroupScanSuccess
	for _, name := range names {
		nameResult := result.ByName[name]
		removed = removed && nameResult.LoadBalancersRemoved
		if nameResult.Err != nil {
			errs = append(errs, nameResult.Err)
		}
	}
	if result.OrphanTargetGroupErr != nil {
		errs = append(errs, result.OrphanTargetGroupErr)
	}
	return removed && len(errs) == 0, errors.Join(errs...)
}

// InspectLoadBalancersByName resolves and verifies all classic and v2 resources for each candidate.
func InspectLoadBalancersByName(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector, names []string) (map[string]NamedLoadBalancerObservation, error) {
	if err := selector.validateNamed(); err != nil {
		return nil, err
	}
	if clients.ELB == nil || clients.ELBV2 == nil {
		return nil, fmt.Errorf("both classic and v2 load balancer clients are required to verify ownership")
	}

	observations := make(map[string]NamedLoadBalancerObservation)
	for _, name := range uniqueNonEmpty(names) {
		observation := NamedLoadBalancerObservation{}
		classicOutput, err := clients.ELB.DescribeLoadBalancers(ctx, &elasticloadbalancing.DescribeLoadBalancersInput{LoadBalancerNames: []string{name}})
		if err != nil && !isNotFound(err) {
			observation.Err = errors.Join(observation.Err, fmt.Errorf("failed to inspect ELB: %w", err))
		} else if err == nil {
			for _, loadBalancer := range classicOutput.LoadBalancerDescriptions {
				if aws.ToString(loadBalancer.LoadBalancerName) != name {
					continue
				}
				observation.Present = true
				if selector.VPCID != aws.ToString(loadBalancer.VPCId) {
					observation.Unverified = true
					continue
				}
				owned, tagErr := classicLoadBalancerHasClusterTag(ctx, clients.ELB, loadBalancer.LoadBalancerName, selector.InfraID)
				if tagErr != nil {
					observation.Err = errors.Join(observation.Err, fmt.Errorf("failed to inspect ELB ownership tags: %w", tagErr))
					continue
				}
				if !owned {
					observation.Unverified = true
					continue
				}
				observation.Owned = append(observation.Owned, LoadBalancerIdentity{
					Name: name, Type: ClassicLoadBalancerResource, VPCID: selector.VPCID,
				})
			}
		}

		v2Output, err := clients.ELBV2.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{Names: []string{name}})
		if err != nil && !isNotFound(err) {
			observation.Err = errors.Join(observation.Err, fmt.Errorf("failed to inspect ELBV2 load balancer: %w", err))
		} else if err == nil {
			for _, loadBalancer := range v2Output.LoadBalancers {
				if aws.ToString(loadBalancer.LoadBalancerName) != name {
					continue
				}
				observation.Present = true
				if selector.VPCID != aws.ToString(loadBalancer.VpcId) {
					observation.Unverified = true
					continue
				}
				owned, tagErr := v2ResourceHasClusterTag(ctx, clients.ELBV2, loadBalancer.LoadBalancerArn, selector.InfraID)
				if tagErr != nil {
					observation.Err = errors.Join(observation.Err, fmt.Errorf("failed to inspect ELBV2 ownership tags: %w", tagErr))
					continue
				}
				if !owned {
					observation.Unverified = true
					continue
				}
				observation.Owned = append(observation.Owned, LoadBalancerIdentity{
					Name: name, Type: V2LoadBalancerResource, ARN: aws.ToString(loadBalancer.LoadBalancerArn), VPCID: selector.VPCID,
				})
			}
		}
		observations[name] = observation
	}
	return observations, nil
}

func cleanupNamedLoadBalancers(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector, names []string, scanOrphanTargetGroups bool, log logr.Logger) (LoadBalancerCleanupResult, error) {
	names = uniqueNonEmpty(names)
	if len(names) > 0 || scanOrphanTargetGroups {
		if err := selector.validateNamed(); err != nil {
			return LoadBalancerCleanupResult{}, err
		}
	}

	result := LoadBalancerCleanupResult{
		ByName: make(map[string]NamedLoadBalancerCleanupResult, len(names)),
	}
	targetGroupARNsByName := make(map[string]sets.Set[string], len(names))
	needOrphanTargetGroupScan := false
	for _, name := range names {
		var nameErrs []error
		if clients.ELB != nil {
			nameErrs = append(nameErrs, deleteNamedClassicLoadBalancers(ctx, clients.ELB, selector, []string{name}, log)...)
		}
		if clients.ELBV2 != nil {
			var loadBalancerErrs, targetGroupErrs []error
			var scan bool
			targetGroupARNsByName[name], loadBalancerErrs, targetGroupErrs, scan = deleteNamedV2LoadBalancersWithoutScan(ctx, clients.ELBV2, selector, []string{name}, log)
			needOrphanTargetGroupScan = needOrphanTargetGroupScan || scan
			nameErrs = append(nameErrs, loadBalancerErrs...)
			result.OrphanTargetGroupErr = errors.Join(result.OrphanTargetGroupErr, errors.Join(targetGroupErrs...))
		}
		result.ByName[name] = NamedLoadBalancerCleanupResult{Err: errors.Join(nameErrs...)}
	}

	scanOrphanTargetGroups = scanOrphanTargetGroups && needOrphanTargetGroupScan
	if scanOrphanTargetGroups {
		result.OrphanTargetGroupScanAttempt = true
		if clients.ELBV2 != nil {
			var scanErrs []error
			result.OrphanTargetGroupARNs, scanErrs = deleteOwnedV2TargetGroups(ctx, clients.ELBV2, selector, log)
			result.OrphanTargetGroupErr = errors.Join(result.OrphanTargetGroupErr, errors.Join(scanErrs...))
			result.OrphanTargetGroupScanSuccess = result.OrphanTargetGroupErr == nil
		} else {
			result.OrphanTargetGroupErr = errors.New("ELBV2 client is required for the orphan target-group scan")
		}
	}

	for _, name := range names {
		nameResult := result.ByName[name]
		remaining, verifyErr := countNamedLoadBalancerResources(ctx, clients, selector, []string{name}, targetGroupARNsByName[name])
		if verifyErr != nil {
			nameResult.Err = errors.Join(nameResult.Err, fmt.Errorf("failed to verify named load balancer cleanup: %w", verifyErr))
		}
		nameResult.LoadBalancersRemoved = nameResult.Err == nil && remaining == 0
		result.ByName[name] = nameResult
	}
	if result.OrphanTargetGroupScanAttempt && result.OrphanTargetGroupScanSuccess {
		remaining, verifyErr := countTargetGroupsByARN(ctx, clients.ELBV2, selector, result.OrphanTargetGroupARNs)
		if verifyErr != nil {
			result.OrphanTargetGroupErr = errors.Join(result.OrphanTargetGroupErr, verifyErr)
		}
		result.OrphanTargetGroupScanSuccess = result.OrphanTargetGroupErr == nil && remaining == 0
	}
	return result, nil
}

func verifyLoadBalancerIdentities(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector, identities []LoadBalancerIdentity) map[LoadBalancerIdentity]loadBalancerIdentityState {
	states := make(map[LoadBalancerIdentity]loadBalancerIdentityState, len(identities))
	for _, identity := range identities {
		state := loadBalancerIdentityState{}
		switch identity.Type {
		case ClassicLoadBalancerResource:
			if clients.ELB == nil {
				state.Err = errors.New("classic ELB client is unavailable")
				break
			}
			output, err := clients.ELB.DescribeLoadBalancers(ctx, &elasticloadbalancing.DescribeLoadBalancersInput{LoadBalancerNames: []string{identity.Name}})
			if isNotFound(err) {
				break
			}
			if err != nil {
				state.Err = fmt.Errorf("failed to verify classic load balancer: %w", err)
				break
			}
			for _, loadBalancer := range output.LoadBalancerDescriptions {
				if aws.ToString(loadBalancer.LoadBalancerName) != identity.Name {
					continue
				}
				state.Present = true
				if aws.ToString(loadBalancer.VPCId) != identity.VPCID || identity.VPCID != selector.VPCID {
					state.Unverified = true
					continue
				}
				owned, err := classicLoadBalancerHasClusterTag(ctx, clients.ELB, loadBalancer.LoadBalancerName, selector.InfraID)
				if err != nil {
					state.Err = errors.Join(state.Err, fmt.Errorf("failed to verify classic load balancer ownership: %w", err))
				} else if !owned {
					state.Unverified = true
				}
			}
		case V2LoadBalancerResource:
			if clients.ELBV2 == nil {
				state.Err = errors.New("ELBV2 client is unavailable")
				break
			}
			output, err := clients.ELBV2.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{LoadBalancerArns: []string{identity.ARN}})
			if isNotFound(err) {
				break
			}
			if err != nil {
				state.Err = fmt.Errorf("failed to verify ELBV2 load balancer: %w", err)
				break
			}
			for _, loadBalancer := range output.LoadBalancers {
				if aws.ToString(loadBalancer.LoadBalancerArn) != identity.ARN {
					continue
				}
				state.Present = true
				if aws.ToString(loadBalancer.LoadBalancerName) != identity.Name || aws.ToString(loadBalancer.VpcId) != identity.VPCID || identity.VPCID != selector.VPCID {
					state.Unverified = true
					continue
				}
				owned, err := v2ResourceHasClusterTag(ctx, clients.ELBV2, loadBalancer.LoadBalancerArn, selector.InfraID)
				if err != nil {
					state.Err = errors.Join(state.Err, fmt.Errorf("failed to verify ELBV2 load balancer ownership: %w", err))
				} else if !owned {
					state.Unverified = true
				}
			}
		default:
			state.Err = fmt.Errorf("unsupported load balancer identity type %q", identity.Type)
		}
		states[identity] = state
	}
	return states
}

// CleanupRecordedLoadBalancers deletes only identities previously verified and persisted by the caller.
func CleanupRecordedLoadBalancers(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector, identities []LoadBalancerIdentity, scanOrphanTargetGroups bool, log logr.Logger) (LoadBalancerCleanupResult, error) {
	if len(identities) > 0 || scanOrphanTargetGroups {
		if err := selector.validateNamed(); err != nil {
			return LoadBalancerCleanupResult{}, err
		}
	}
	result := LoadBalancerCleanupResult{ByName: make(map[string]NamedLoadBalancerCleanupResult)}
	byName := make(map[string][]LoadBalancerIdentity)
	for _, identity := range identities {
		byName[identity.Name] = append(byName[identity.Name], identity)
	}
	for name, nameIdentities := range byName {
		var errs []error
		for _, identity := range nameIdentities {
			switch identity.Type {
			case ClassicLoadBalancerResource:
				err := deleteRecordedClassicLoadBalancer(ctx, clients.ELB, selector, identity, log)
				errs = append(errs, err)
			case V2LoadBalancerResource:
				err := deleteRecordedV2LoadBalancer(ctx, clients.ELBV2, selector, identity, log)
				errs = append(errs, err)
			default:
				errs = append(errs, fmt.Errorf("unsupported load balancer identity type %q", identity.Type))
			}
		}
		result.ByName[name] = NamedLoadBalancerCleanupResult{Err: errors.Join(errs...)}
	}
	if scanOrphanTargetGroups {
		result.OrphanTargetGroupScanAttempt = true
		if clients.ELBV2 == nil {
			result.OrphanTargetGroupErr = errors.New("ELBV2 client is required for the orphan target-group scan")
		} else {
			var errs []error
			result.OrphanTargetGroupARNs, errs = deleteOwnedV2TargetGroups(ctx, clients.ELBV2, selector, log)
			result.OrphanTargetGroupErr = errors.Join(errs...)
		}
	}
	states := verifyLoadBalancerIdentities(ctx, clients, selector, identities)
	for name, nameIdentities := range byName {
		nameResult := result.ByName[name]
		allGone := true
		for _, identity := range nameIdentities {
			state := states[identity]
			if state.Err != nil {
				nameResult.Err = errors.Join(nameResult.Err, state.Err)
			}
			if state.Present || state.Unverified {
				allGone = false
			}
		}
		nameResult.LoadBalancersRemoved = nameResult.Err == nil && allGone
		result.ByName[name] = nameResult
	}
	if result.OrphanTargetGroupScanAttempt && result.OrphanTargetGroupErr == nil {
		remaining, verifyErr := countTargetGroupsByARN(ctx, clients.ELBV2, selector, result.OrphanTargetGroupARNs)
		result.OrphanTargetGroupErr = verifyErr
		result.OrphanTargetGroupScanSuccess = verifyErr == nil && remaining == 0
	}
	return result, nil
}

func deleteRecordedClassicLoadBalancer(ctx context.Context, client awsapi.ELBAPI, selector LoadBalancerSelector, identity LoadBalancerIdentity, log logr.Logger) error {
	if client == nil {
		return errors.New("classic ELB client is unavailable")
	}
	output, err := client.DescribeLoadBalancers(ctx, &elasticloadbalancing.DescribeLoadBalancersInput{LoadBalancerNames: []string{identity.Name}})
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to find recorded classic load balancer: %w", err)
	}
	for _, loadBalancer := range output.LoadBalancerDescriptions {
		if aws.ToString(loadBalancer.LoadBalancerName) != identity.Name {
			continue
		}
		if identity.VPCID != selector.VPCID || aws.ToString(loadBalancer.VPCId) != identity.VPCID {
			return ErrLoadBalancerOwnershipUnverified
		}
		owned, err := classicLoadBalancerHasClusterTag(ctx, client, loadBalancer.LoadBalancerName, selector.InfraID)
		if err != nil {
			return fmt.Errorf("failed to inspect recorded classic load balancer ownership: %w", err)
		}
		if !owned {
			return ErrLoadBalancerOwnershipUnverified
		}
		if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancing.DeleteLoadBalancerInput{LoadBalancerName: loadBalancer.LoadBalancerName}); err != nil && !isNotFound(err) {
			return fmt.Errorf("failed to delete recorded classic load balancer: %w", err)
		}
		log.Info("Deleted classic load balancer")
		return nil
	}
	return nil
}

func deleteRecordedV2LoadBalancer(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector, identity LoadBalancerIdentity, log logr.Logger) error {
	if client == nil {
		return errors.New("ELBV2 client is unavailable")
	}
	output, err := client.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{LoadBalancerArns: []string{identity.ARN}})
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to find recorded ELBV2 load balancer: %w", err)
	}
	for _, loadBalancer := range output.LoadBalancers {
		if aws.ToString(loadBalancer.LoadBalancerArn) != identity.ARN {
			continue
		}
		if aws.ToString(loadBalancer.LoadBalancerName) != identity.Name || identity.VPCID != selector.VPCID || aws.ToString(loadBalancer.VpcId) != identity.VPCID {
			return ErrLoadBalancerOwnershipUnverified
		}
		owned, err := v2ResourceHasClusterTag(ctx, client, loadBalancer.LoadBalancerArn, selector.InfraID)
		if err != nil {
			return fmt.Errorf("failed to inspect recorded ELBV2 load balancer ownership: %w", err)
		}
		if !owned {
			return ErrLoadBalancerOwnershipUnverified
		}
		_, loadBalancerErr, targetGroupErr := deleteNamedV2LoadBalancerDetailed(ctx, client, loadBalancer, selector, log)
		return errors.Join(loadBalancerErr, targetGroupErr)
	}
	return nil
}

// IsOnlyDescribeTagsAccessDenied reports whether every error leaf is a DescribeTags denial.
func IsOnlyDescribeTagsAccessDenied(err error) bool {
	if err == nil {
		return false
	}
	if multiple, ok := err.(interface{ Unwrap() []error }); ok {
		children := multiple.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !IsOnlyDescribeTagsAccessDenied(child) {
				return false
			}
		}
		return true
	}
	// The denial wrapper is a leaf for classification; its AWS cause is diagnostic context.
	if _, ok := err.(describeTagsAccessDeniedMarker); ok {
		return true
	}
	if single, ok := err.(interface{ Unwrap() error }); ok {
		return IsOnlyDescribeTagsAccessDenied(single.Unwrap())
	}
	return false
}

type describeTagsAccessDeniedError struct {
	err error
}

type describeTagsAccessDeniedMarker interface {
	describeTagsAccessDenied()
}

func (*describeTagsAccessDeniedError) describeTagsAccessDenied() {}

func (e *describeTagsAccessDeniedError) Error() string {
	return "DescribeTags access denied; delegated role needs elasticloadbalancing:DescribeTags to verify ownership"
}

func (e *describeTagsAccessDeniedError) Unwrap() error {
	return e.err
}

func (e *describeTagsAccessDeniedError) Is(target error) bool {
	return target == ErrDescribeTagsAccessDenied
}

// CountLoadBalancerResources counts load balancers and target groups that remain in a VPC.
func CountLoadBalancerResources(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector) (int, error) {
	if err := selector.validate(); err != nil {
		return 0, err
	}

	count := 0
	var errs []error
	if clients.ELB != nil {
		currentCount, err := countClassicLoadBalancers(ctx, clients.ELB, selector)
		count += currentCount
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to count ELB load balancers: %w", err))
		}
	}
	if clients.ELBV2 != nil {
		currentCount, err := countV2LoadBalancerResources(ctx, clients.ELBV2, selector)
		count += currentCount
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to count ELBV2 load balancer resources: %w", err))
		}
	}
	return count, errors.Join(errs...)
}

// LoadBalancerNameFromHostname extracts an AWS load balancer name from a Service status hostname.
func LoadBalancerNameFromHostname(hostname string) string {
	hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	if !isAWSLoadBalancerHostname(hostname) {
		return ""
	}

	labels := strings.Split(strings.TrimSuffix(hostname, ".cn"), ".")
	loadBalancerLabel := strings.TrimPrefix(labels[len(labels)-5], "internal-")
	if loadBalancerLabel == "" {
		return ""
	}
	if lastHyphen := strings.LastIndex(loadBalancerLabel, "-"); lastHyphen > 0 {
		return loadBalancerLabel[:lastHyphen]
	}
	return loadBalancerLabel
}

// LoadBalancerRegionFromHostname extracts the AWS region from a load balancer hostname.
func LoadBalancerRegionFromHostname(hostname string) string {
	hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	if !isAWSLoadBalancerHostname(hostname) {
		return ""
	}
	labels := strings.Split(strings.TrimSuffix(hostname, ".cn"), ".")
	last := len(labels) - 1
	if labels[last-2] == "elb" {
		return labels[last-3]
	}
	if labels[last-3] == "elb" {
		return labels[last-2]
	}
	return ""
}

func isAWSLoadBalancerHostname(hostname string) bool {
	labels := strings.Split(strings.TrimSuffix(hostname, ".cn"), ".")
	if len(labels) < 5 {
		return false
	}

	last := len(labels) - 1
	return (labels[last-2] == "elb" && labels[last-1] == "amazonaws" && labels[last] == "com") ||
		(labels[last-3] == "elb" && labels[last-1] == "amazonaws" && labels[last] == "com")
}

func (s LoadBalancerSelector) validate() error {
	if s.VPCID == "" {
		return fmt.Errorf("VPCID must be specified")
	}
	return nil
}

func (s LoadBalancerSelector) validateNamed() error {
	if err := s.validate(); err != nil {
		return err
	}
	if s.InfraID == "" {
		return fmt.Errorf("InfraID must be specified")
	}
	return nil
}

func deleteClassicLoadBalancers(ctx context.Context, client awsapi.ELBAPI, selector LoadBalancerSelector, log logr.Logger) []error {
	var errs []error
	paginator := elasticloadbalancing.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancing.DescribeLoadBalancersInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return append(errs, err)
		}
		for _, loadBalancer := range output.LoadBalancerDescriptions {
			if selector.VPCID != aws.ToString(loadBalancer.VPCId) {
				continue
			}
			if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancing.DeleteLoadBalancerInput{LoadBalancerName: loadBalancer.LoadBalancerName}); err != nil && !isNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete ELB: %w", err))
			} else if err == nil {
				log.Info("Deleted ELB")
			}
		}
	}
	return errs
}

func deleteNamedClassicLoadBalancers(ctx context.Context, client awsapi.ELBAPI, selector LoadBalancerSelector, names []string, log logr.Logger) []error {
	var errs []error
	for _, name := range names {
		output, err := client.DescribeLoadBalancers(ctx, &elasticloadbalancing.DescribeLoadBalancersInput{LoadBalancerNames: []string{name}})
		if isNotFound(err) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to find ELB: %w", err))
			continue
		}
		for _, loadBalancer := range output.LoadBalancerDescriptions {
			if selector.VPCID != aws.ToString(loadBalancer.VPCId) {
				continue
			}
			owned, err := classicLoadBalancerHasClusterTag(ctx, client, loadBalancer.LoadBalancerName, selector.InfraID)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to inspect ELB tags: %w", err))
				continue
			}
			if !owned {
				continue
			}
			if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancing.DeleteLoadBalancerInput{LoadBalancerName: loadBalancer.LoadBalancerName}); err != nil && !isNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete ELB: %w", err))
			} else if err == nil {
				log.Info("Deleted ELB")
			}
		}
	}
	return errs
}

func countClassicLoadBalancers(ctx context.Context, client awsapi.ELBAPI, selector LoadBalancerSelector) (int, error) {
	count := 0
	paginator := elasticloadbalancing.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancing.DescribeLoadBalancersInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return count, err
		}
		for _, loadBalancer := range output.LoadBalancerDescriptions {
			if selector.VPCID == aws.ToString(loadBalancer.VPCId) {
				count++
			}
		}
	}
	return count, nil
}

func deleteV2LoadBalancers(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector, log logr.Logger) []error {
	var errs []error
	lbPaginator := elasticloadbalancingv2.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancingv2.DescribeLoadBalancersInput{})
	for lbPaginator.HasMorePages() {
		output, err := lbPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, loadBalancer := range output.LoadBalancers {
			if selector.VPCID != aws.ToString(loadBalancer.VpcId) {
				continue
			}
			if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancingv2.DeleteLoadBalancerInput{LoadBalancerArn: loadBalancer.LoadBalancerArn}); err != nil && !isNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete ELBV2 load balancer: %w", err))
			} else if err == nil {
				log.Info("Deleted ELBV2 load balancer")
			}
		}
	}

	tgPaginator := elasticloadbalancingv2.NewDescribeTargetGroupsPaginator(client, &elasticloadbalancingv2.DescribeTargetGroupsInput{})
	for tgPaginator.HasMorePages() {
		output, err := tgPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, targetGroup := range output.TargetGroups {
			if selector.VPCID != aws.ToString(targetGroup.VpcId) {
				continue
			}
			if _, err := client.DeleteTargetGroup(ctx, &elasticloadbalancingv2.DeleteTargetGroupInput{TargetGroupArn: targetGroup.TargetGroupArn}); err != nil && !isNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete target group: %w", err))
			} else if err == nil {
				log.Info("Deleted target group")
			}
		}
	}
	return errs
}

func deleteNamedV2LoadBalancers(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector, names []string, log logr.Logger) (sets.Set[string], []error) {
	targetGroupARNs, loadBalancerErrs, targetGroupErrs, scanOrphanedTargetGroups := deleteNamedV2LoadBalancersWithoutScan(ctx, client, selector, names, log)
	var errs []error
	errs = append(errs, loadBalancerErrs...)
	errs = append(errs, targetGroupErrs...)
	if scanOrphanedTargetGroups {
		// Target groups can outlive a named load balancer that's gone or was partially removed in this pass.
		orphanedTargetGroupARNs, orphanedErrs := deleteOwnedV2TargetGroups(ctx, client, selector, log)
		for arn := range orphanedTargetGroupARNs {
			targetGroupARNs.Insert(arn)
		}
		errs = append(errs, orphanedErrs...)
	}
	return targetGroupARNs, errs
}

func deleteNamedV2LoadBalancersWithoutScan(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector, names []string, log logr.Logger) (sets.Set[string], []error, []error, bool) {
	targetGroupARNs := sets.New[string]()
	var loadBalancerErrs, targetGroupErrs []error
	scanOrphanedTargetGroups := false
	for _, name := range names {
		output, err := client.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{Names: []string{name}})
		if isNotFound(err) {
			scanOrphanedTargetGroups = true
			continue
		}
		if err != nil {
			loadBalancerErrs = append(loadBalancerErrs, fmt.Errorf("failed to find ELBV2 load balancer: %w", err))
			continue
		}
		if len(output.LoadBalancers) == 0 {
			scanOrphanedTargetGroups = true
			continue
		}
		for _, loadBalancer := range output.LoadBalancers {
			if selector.VPCID != aws.ToString(loadBalancer.VpcId) {
				continue
			}
			owned, err := v2ResourceHasClusterTag(ctx, client, loadBalancer.LoadBalancerArn, selector.InfraID)
			if err != nil {
				loadBalancerErrs = append(loadBalancerErrs, fmt.Errorf("failed to inspect ELBV2 load balancer tags: %w", err))
				continue
			}
			if !owned {
				continue
			}
			loadBalancerTargetGroups, loadBalancerErr, targetGroupErr := deleteNamedV2LoadBalancerDetailed(ctx, client, loadBalancer, selector, log)
			for arn := range loadBalancerTargetGroups {
				targetGroupARNs.Insert(arn)
			}
			// Child deletions can partially succeed before a later deletion returns an error.
			scanOrphanedTargetGroups = true
			if loadBalancerErr != nil {
				loadBalancerErrs = append(loadBalancerErrs, fmt.Errorf("failed to delete ELBV2 load balancer: %w", loadBalancerErr))
			}
			if targetGroupErr != nil {
				targetGroupErrs = append(targetGroupErrs, targetGroupErr)
			}
		}
	}
	return targetGroupARNs, loadBalancerErrs, targetGroupErrs, scanOrphanedTargetGroups
}

func deleteOwnedV2TargetGroups(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector, log logr.Logger) (sets.Set[string], []error) {
	targetGroupARNs := sets.New[string]()
	var errs []error
	paginator := elasticloadbalancingv2.NewDescribeTargetGroupsPaginator(client, &elasticloadbalancingv2.DescribeTargetGroupsInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to list ELBV2 target groups for cleanup: %w", err))
			break
		}
		for _, targetGroup := range output.TargetGroups {
			// Only clean up target groups that are no longer attached to a load balancer.
			if len(targetGroup.LoadBalancerArns) > 0 {
				continue
			}
			if selector.VPCID != aws.ToString(targetGroup.VpcId) || targetGroup.TargetGroupArn == nil {
				continue
			}
			owned, err := v2ResourceHasClusterTag(ctx, client, targetGroup.TargetGroupArn, selector.InfraID)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to inspect target group tags: %w", err))
				continue
			}
			if owned {
				targetGroupARNs.Insert(aws.ToString(targetGroup.TargetGroupArn))
			}
		}
	}

	arns := make([]string, 0, len(targetGroupARNs))
	for arn := range targetGroupARNs {
		arns = append(arns, arn)
	}
	sort.Strings(arns)
	for _, arn := range arns {
		if _, err := client.DeleteTargetGroup(ctx, &elasticloadbalancingv2.DeleteTargetGroupInput{TargetGroupArn: aws.String(arn)}); err != nil && !isNotFound(err) {
			errs = append(errs, fmt.Errorf("failed to delete target group: %w", err))
		} else if err == nil {
			log.Info("Deleted target group")
		}
	}
	return targetGroupARNs, errs
}

func deleteNamedV2LoadBalancer(ctx context.Context, client awsapi.ELBV2API, loadBalancer elbv2types.LoadBalancer, selector LoadBalancerSelector, log logr.Logger) (sets.Set[string], error) {
	targetGroupARNs, loadBalancerErr, targetGroupErr := deleteNamedV2LoadBalancerDetailed(ctx, client, loadBalancer, selector, log)
	return targetGroupARNs, errors.Join(loadBalancerErr, targetGroupErr)
}

func deleteNamedV2LoadBalancerDetailed(ctx context.Context, client awsapi.ELBV2API, loadBalancer elbv2types.LoadBalancer, selector LoadBalancerSelector, log logr.Logger) (sets.Set[string], error, error) {
	targetGroupARNs := sets.New[string]()
	var loadBalancerErrs, targetGroupErrs []error
	targetGroupsPaginator := elasticloadbalancingv2.NewDescribeTargetGroupsPaginator(client, &elasticloadbalancingv2.DescribeTargetGroupsInput{LoadBalancerArn: loadBalancer.LoadBalancerArn})
	for targetGroupsPaginator.HasMorePages() {
		targetGroups, err := targetGroupsPaginator.NextPage(ctx)
		if isNotFound(err) {
			break
		}
		if err != nil {
			targetGroupErrs = append(targetGroupErrs, fmt.Errorf("failed to find target groups for ELBV2 load balancer: %w", err))
			break
		}
		for _, targetGroup := range targetGroups.TargetGroups {
			if selector.VPCID != aws.ToString(targetGroup.VpcId) || targetGroup.TargetGroupArn == nil {
				continue
			}
			owned, err := v2ResourceHasClusterTag(ctx, client, targetGroup.TargetGroupArn, selector.InfraID)
			if err != nil {
				targetGroupErrs = append(targetGroupErrs, fmt.Errorf("failed to inspect target group tags: %w", err))
				continue
			}
			if owned {
				targetGroupARNs.Insert(aws.ToString(targetGroup.TargetGroupArn))
			}
		}
	}

	var listenerARNs []*string
	listenersPaginator := elasticloadbalancingv2.NewDescribeListenersPaginator(client, &elasticloadbalancingv2.DescribeListenersInput{LoadBalancerArn: loadBalancer.LoadBalancerArn})
	for listenersPaginator.HasMorePages() {
		listeners, err := listenersPaginator.NextPage(ctx)
		if isNotFound(err) {
			break
		}
		if err != nil {
			loadBalancerErrs = append(loadBalancerErrs, fmt.Errorf("failed to find listeners for ELBV2 load balancer: %w", err))
			break
		}
		for _, listener := range listeners.Listeners {
			listenerARNs = append(listenerARNs, listener.ListenerArn)
		}
	}
	for _, listenerARN := range listenerARNs {
		if _, err := client.DeleteListener(ctx, &elasticloadbalancingv2.DeleteListenerInput{ListenerArn: listenerARN}); err != nil && !isNotFound(err) {
			loadBalancerErrs = append(loadBalancerErrs, fmt.Errorf("failed to delete listener for ELBV2 load balancer: %w", err))
		}
	}
	sortedTargetGroupARNs := make([]string, 0, len(targetGroupARNs))
	for arn := range targetGroupARNs {
		sortedTargetGroupARNs = append(sortedTargetGroupARNs, arn)
	}
	sort.Strings(sortedTargetGroupARNs)
	for _, arn := range sortedTargetGroupARNs {
		if _, err := client.DeleteTargetGroup(ctx, &elasticloadbalancingv2.DeleteTargetGroupInput{TargetGroupArn: aws.String(arn)}); err != nil && !isNotFound(err) {
			targetGroupErrs = append(targetGroupErrs, fmt.Errorf("failed to delete target group: %w", err))
		} else if err == nil {
			log.Info("Deleted target group")
		}
	}

	if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancingv2.DeleteLoadBalancerInput{LoadBalancerArn: loadBalancer.LoadBalancerArn}); err != nil && !isNotFound(err) {
		loadBalancerErrs = append(loadBalancerErrs, fmt.Errorf("failed to delete ELBV2 load balancer: %w", err))
	} else if err == nil {
		log.Info("Deleted ELBV2 load balancer")
	}
	return targetGroupARNs, errors.Join(loadBalancerErrs...), errors.Join(targetGroupErrs...)
}

func countV2LoadBalancerResources(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector) (int, error) {
	count := 0
	var errs []error

	lbPaginator := elasticloadbalancingv2.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancingv2.DescribeLoadBalancersInput{})
	for lbPaginator.HasMorePages() {
		output, err := lbPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, loadBalancer := range output.LoadBalancers {
			if selector.VPCID == aws.ToString(loadBalancer.VpcId) {
				count++
			}
		}
	}

	tgPaginator := elasticloadbalancingv2.NewDescribeTargetGroupsPaginator(client, &elasticloadbalancingv2.DescribeTargetGroupsInput{})
	for tgPaginator.HasMorePages() {
		output, err := tgPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, targetGroup := range output.TargetGroups {
			if selector.VPCID == aws.ToString(targetGroup.VpcId) {
				count++
			}
		}
	}

	return count, errors.Join(errs...)
}

func countNamedLoadBalancerResources(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector, names []string, targetGroupARNs sets.Set[string]) (int, error) {
	count := 0
	var errs []error
	if clients.ELB != nil {
		for _, name := range names {
			output, err := clients.ELB.DescribeLoadBalancers(ctx, &elasticloadbalancing.DescribeLoadBalancersInput{LoadBalancerNames: []string{name}})
			if isNotFound(err) {
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to verify ELB: %w", err))
				continue
			}
			for _, loadBalancer := range output.LoadBalancerDescriptions {
				if selector.VPCID != aws.ToString(loadBalancer.VPCId) {
					continue
				}
				owned, tagErr := classicLoadBalancerHasClusterTag(ctx, clients.ELB, loadBalancer.LoadBalancerName, selector.InfraID)
				if tagErr != nil {
					errs = append(errs, fmt.Errorf("failed to verify ELB tags: %w", tagErr))
					continue
				}
				if owned {
					count++
				}
			}
		}
	}
	if clients.ELBV2 != nil {
		for _, name := range names {
			output, err := clients.ELBV2.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{Names: []string{name}})
			if isNotFound(err) {
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to verify ELBV2 load balancer: %w", err))
				continue
			}
			for _, loadBalancer := range output.LoadBalancers {
				if selector.VPCID != aws.ToString(loadBalancer.VpcId) {
					continue
				}
				owned, tagErr := v2ResourceHasClusterTag(ctx, clients.ELBV2, loadBalancer.LoadBalancerArn, selector.InfraID)
				if tagErr != nil {
					errs = append(errs, fmt.Errorf("failed to verify ELBV2 load balancer tags: %w", tagErr))
					continue
				}
				if owned {
					count++
				}
			}
		}
		for arn := range targetGroupARNs {
			output, err := clients.ELBV2.DescribeTargetGroups(ctx, &elasticloadbalancingv2.DescribeTargetGroupsInput{TargetGroupArns: []string{arn}})
			if isNotFound(err) {
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to verify target group: %w", err))
				continue
			}
			for _, targetGroup := range output.TargetGroups {
				if selector.VPCID != aws.ToString(targetGroup.VpcId) {
					continue
				}
				owned, tagErr := v2ResourceHasClusterTag(ctx, clients.ELBV2, targetGroup.TargetGroupArn, selector.InfraID)
				if tagErr != nil {
					errs = append(errs, fmt.Errorf("failed to verify target group tags: %w", tagErr))
					continue
				}
				if owned {
					count++
				}
			}
		}
	}
	return count, errors.Join(errs...)
}

func countTargetGroupsByARN(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector, targetGroupARNs sets.Set[string]) (int, error) {
	arns := make([]string, 0, len(targetGroupARNs))
	for arn := range targetGroupARNs {
		arns = append(arns, arn)
	}
	sort.Strings(arns)

	count := 0
	var errs []error
	for _, arn := range arns {
		output, err := client.DescribeTargetGroups(ctx, &elasticloadbalancingv2.DescribeTargetGroupsInput{TargetGroupArns: []string{arn}})
		if isNotFound(err) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to verify target group: %w", err))
			continue
		}
		for _, targetGroup := range output.TargetGroups {
			if aws.ToString(targetGroup.TargetGroupArn) != arn {
				continue
			}
			count++
			if selector.VPCID != aws.ToString(targetGroup.VpcId) {
				continue
			}
			if _, err := v2ResourceHasClusterTag(ctx, client, targetGroup.TargetGroupArn, selector.InfraID); err != nil {
				errs = append(errs, fmt.Errorf("failed to verify target group tags: %w", err))
			}
		}
	}
	return count, errors.Join(errs...)
}

func classicLoadBalancerHasClusterTag(ctx context.Context, client awsapi.ELBAPI, name *string, infraID string) (bool, error) {
	output, err := client.DescribeTags(ctx, &elasticloadbalancing.DescribeTagsInput{LoadBalancerNames: []string{aws.ToString(name)}})
	if isNotFound(err) {
		return false, nil
	}
	if isAccessDenied(err) {
		return false, &describeTagsAccessDeniedError{err: err}
	}
	if err != nil {
		return false, err
	}
	tagKey := ClusterTag(infraID)
	for _, description := range output.TagDescriptions {
		if aws.ToString(description.LoadBalancerName) != aws.ToString(name) {
			continue
		}
		for _, tag := range description.Tags {
			if aws.ToString(tag.Key) == tagKey && aws.ToString(tag.Value) == "owned" {
				return true, nil
			}
		}
	}
	return false, nil
}

func v2ResourceHasClusterTag(ctx context.Context, client awsapi.ELBV2API, arn *string, infraID string) (bool, error) {
	output, err := client.DescribeTags(ctx, &elasticloadbalancingv2.DescribeTagsInput{ResourceArns: []string{aws.ToString(arn)}})
	if isNotFound(err) {
		return false, nil
	}
	if isAccessDenied(err) {
		return false, &describeTagsAccessDeniedError{err: err}
	}
	if err != nil {
		return false, err
	}
	tagKey := ClusterTag(infraID)
	for _, description := range output.TagDescriptions {
		if aws.ToString(description.ResourceArn) != aws.ToString(arn) {
			continue
		}
		for _, tag := range description.Tags {
			if aws.ToString(tag.Key) == tagKey && aws.ToString(tag.Value) == "owned" {
				return true, nil
			}
		}
	}
	return false, nil
}

func uniqueNonEmpty(values []string) []string {
	seen := sets.New[string]()
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if seen.Has(value) {
			continue
		}
		seen.Insert(value)
		result = append(result, value)
	}
	return result
}

func isNotFound(err error) bool {
	var apiErr smithy.APIError
	if err == nil || !errors.As(err, &apiErr) {
		return false
	}
	switch strings.ToLower(apiErr.ErrorCode()) {
	case "loadbalancernotfound", "targetgroupnotfound", "listenernotfound":
		return true
	default:
		return false
	}
}

func isAccessDenied(err error) bool {
	code := strings.ToLower(AWSErrorCode(err))
	return code == strings.ToLower(AccessDenied) || code == "accessdeniedexception"
}
