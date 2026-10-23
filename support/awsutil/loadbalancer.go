package awsutil

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
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

var errOwnedTargetGroupStillAttached = errors.New("a cluster-owned target group is still attached to a load balancer")

type loadBalancerCleanupScope struct {
	arns         sets.Set[string]
	missingNames sets.Set[string]
}

func (s loadBalancerCleanupScope) matchesAttachment(targetGroup elbv2types.TargetGroup) (bool, error) {
	if s.arns.HasAny(targetGroup.LoadBalancerArns...) {
		return true, nil
	}
	if len(s.missingNames) == 0 {
		return false, nil
	}
	// Only stateless named cleanup uses this fallback; recorded cleanup requires an exact ARN.
	targetGroupARN, err := arn.Parse(aws.ToString(targetGroup.TargetGroupArn))
	if err != nil || targetGroupARN.Service != "elasticloadbalancing" {
		return false, errors.New("could not verify target group attachment")
	}
	for _, attachment := range targetGroup.LoadBalancerArns {
		loadBalancerARN, err := arn.Parse(attachment)
		if err != nil || loadBalancerARN.Service != "elasticloadbalancing" {
			return false, errors.New("could not verify target group attachment")
		}
		resource := strings.Split(loadBalancerARN.Resource, "/")
		if len(resource) != 4 || resource[0] != "loadbalancer" || resource[2] == "" || resource[3] == "" {
			return false, errors.New("could not verify target group attachment")
		}
		switch resource[1] {
		case "app", "net", "gwy":
		default:
			return false, errors.New("could not verify target group attachment")
		}
		if loadBalancerARN.Partition == targetGroupARN.Partition && loadBalancerARN.AccountID == targetGroupARN.AccountID && loadBalancerARN.Region == targetGroupARN.Region && s.missingNames.Has(resource[2]) {
			return true, nil
		}
	}
	return false, nil
}

// DeleteLoadBalancers deletes load balancers and target groups selected by their VPC.
func DeleteLoadBalancers(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector, log logr.Logger) []error {
	if err := selector.validate(); err != nil {
		return []error{err}
	}

	var errs []error
	if clients.ELB != nil {
		errs = append(errs, deleteClassicLoadBalancers(ctx, clients.ELB, selector, log)...)
	}
	if appendContextError(ctx, &errs) {
		return errs
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
		if err := ctx.Err(); err != nil {
			return observations, err
		}
		observation := NamedLoadBalancerObservation{}
		classicOutput, err := clients.ELB.DescribeLoadBalancers(ctx, &elasticloadbalancing.DescribeLoadBalancersInput{LoadBalancerNames: []string{name}})
		if err != nil && !isNotFound(err) {
			observation.Err = errors.Join(observation.Err, fmt.Errorf("failed to inspect ELB: %w", err))
		} else if err == nil {
			for _, loadBalancer := range classicOutput.LoadBalancerDescriptions {
				if err := ctx.Err(); err != nil {
					return observations, err
				}
				if aws.ToString(loadBalancer.LoadBalancerName) != name {
					continue
				}
				observation.Present = true
				if selector.VPCID != aws.ToString(loadBalancer.VPCId) {
					observation.Unverified = true
					continue
				}
				if err := ctx.Err(); err != nil {
					return observations, err
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

		if err := ctx.Err(); err != nil {
			return observations, err
		}
		v2Output, err := clients.ELBV2.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{Names: []string{name}})
		if err != nil && !isNotFound(err) {
			observation.Err = errors.Join(observation.Err, fmt.Errorf("failed to inspect ELBV2 load balancer: %w", err))
		} else if err == nil {
			for _, loadBalancer := range v2Output.LoadBalancers {
				if err := ctx.Err(); err != nil {
					return observations, err
				}
				if aws.ToString(loadBalancer.LoadBalancerName) != name {
					continue
				}
				observation.Present = true
				if selector.VPCID != aws.ToString(loadBalancer.VpcId) {
					observation.Unverified = true
					continue
				}
				if err := ctx.Err(); err != nil {
					return observations, err
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
		if err := ctx.Err(); err != nil {
			return observations, err
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
	cleanupScope := loadBalancerCleanupScope{arns: sets.New[string](), missingNames: sets.New[string]()}
	needOrphanTargetGroupScan := false
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		var nameErrs []error
		if clients.ELB != nil {
			nameErrs = append(nameErrs, deleteNamedClassicLoadBalancers(ctx, clients.ELB, selector, []string{name}, log)...)
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if clients.ELBV2 != nil {
			var loadBalancerErrs, targetGroupErrs []error
			var loadBalancerScope loadBalancerCleanupScope
			var scan bool
			targetGroupARNsByName[name], loadBalancerScope, loadBalancerErrs, targetGroupErrs, scan = deleteNamedV2LoadBalancersWithoutScan(ctx, clients.ELBV2, selector, []string{name}, log)
			cleanupScope.arns.Insert(loadBalancerScope.arns.UnsortedList()...)
			cleanupScope.missingNames.Insert(loadBalancerScope.missingNames.UnsortedList()...)
			needOrphanTargetGroupScan = needOrphanTargetGroupScan || scan
			nameErrs = append(nameErrs, loadBalancerErrs...)
			result.OrphanTargetGroupErr = errors.Join(result.OrphanTargetGroupErr, errors.Join(targetGroupErrs...))
		}
		result.ByName[name] = NamedLoadBalancerCleanupResult{Err: errors.Join(nameErrs...)}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	scanOrphanTargetGroups = scanOrphanTargetGroups && needOrphanTargetGroupScan
	if scanOrphanTargetGroups {
		result.OrphanTargetGroupScanAttempt = true
		if clients.ELBV2 != nil {
			var scanErrs []error
			result.OrphanTargetGroupARNs, scanErrs = deleteOwnedV2TargetGroups(ctx, clients.ELBV2, selector, cleanupScope, log)
			result.OrphanTargetGroupErr = errors.Join(result.OrphanTargetGroupErr, errors.Join(scanErrs...))
			result.OrphanTargetGroupScanSuccess = result.OrphanTargetGroupErr == nil
		} else {
			result.OrphanTargetGroupErr = errors.New("ELBV2 client is required for the orphan target-group scan")
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		nameResult := result.ByName[name]
		remaining, verifyErr := countNamedLoadBalancerResources(ctx, clients, selector, []string{name}, targetGroupARNsByName[name])
		if verifyErr != nil {
			nameResult.Err = errors.Join(nameResult.Err, fmt.Errorf("failed to verify named load balancer cleanup: %w", verifyErr))
		}
		nameResult.LoadBalancersRemoved = nameResult.Err == nil && remaining == 0
		result.ByName[name] = nameResult
	}
	if result.OrphanTargetGroupScanAttempt && result.OrphanTargetGroupScanSuccess {
		if err := ctx.Err(); err != nil {
			return result, err
		}
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
		if err := ctx.Err(); err != nil {
			state.Err = err
			states[identity] = state
			continue
		}
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
				if err := ctx.Err(); err != nil {
					state.Err = err
					break
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
				if err := ctx.Err(); err != nil {
					state.Err = err
					break
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
	cleanupLoadBalancerARNs := sets.New[string]()
	for _, identity := range identities {
		byName[identity.Name] = append(byName[identity.Name], identity)
		// Retain recorded ARNs even when AWS now reports the load balancer NotFound.
		if identity.Type == V2LoadBalancerResource && identity.ARN != "" {
			cleanupLoadBalancerARNs.Insert(identity.ARN)
		}
	}
	for name, nameIdentities := range byName {
		var errs []error
		for _, identity := range nameIdentities {
			if err := ctx.Err(); err != nil {
				return result, err
			}
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
			if err := ctx.Err(); err != nil {
				return result, err
			}
		}
		result.ByName[name] = NamedLoadBalancerCleanupResult{Err: errors.Join(errs...)}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if scanOrphanTargetGroups {
		result.OrphanTargetGroupScanAttempt = true
		if clients.ELBV2 == nil {
			result.OrphanTargetGroupErr = errors.New("ELBV2 client is required for the orphan target-group scan")
		} else {
			var errs []error
			result.OrphanTargetGroupARNs, errs = deleteOwnedV2TargetGroups(ctx, clients.ELBV2, selector, loadBalancerCleanupScope{arns: cleanupLoadBalancerARNs}, log)
			result.OrphanTargetGroupErr = errors.Join(errs...)
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
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
		if err := ctx.Err(); err != nil {
			return result, err
		}
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
	if err := ctx.Err(); err != nil {
		return err
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
		if err := ctx.Err(); err != nil {
			return err
		}
		owned, err := classicLoadBalancerHasClusterTag(ctx, client, loadBalancer.LoadBalancerName, selector.InfraID)
		if err != nil {
			return fmt.Errorf("failed to inspect recorded classic load balancer ownership: %w", err)
		}
		if !owned {
			return ErrLoadBalancerOwnershipUnverified
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancing.DeleteLoadBalancerInput{LoadBalancerName: loadBalancer.LoadBalancerName}); err != nil && !isNotFound(err) {
			return fmt.Errorf("failed to delete recorded classic load balancer: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
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
	if err := ctx.Err(); err != nil {
		return err
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
		if err := ctx.Err(); err != nil {
			return err
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
	if appendContextError(ctx, &errs) {
		return count, errors.Join(errs...)
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
	parts, ok := parseAWSLoadBalancerHostname(hostname)
	if !ok {
		return ""
	}
	return parts.Name
}

// LoadBalancerRegionFromHostname extracts the AWS region from a load balancer hostname.
func LoadBalancerRegionFromHostname(hostname string) string {
	parts, ok := parseAWSLoadBalancerHostname(hostname)
	if !ok {
		return ""
	}
	return parts.Region
}

type awsLoadBalancerHostnameParts struct {
	Name   string
	Region string
}

func parseAWSLoadBalancerHostname(hostname string) (awsLoadBalancerHostnameParts, bool) {
	hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	labels := strings.Split(strings.TrimSuffix(hostname, ".cn"), ".")
	if len(labels) < 5 {
		return awsLoadBalancerHostnameParts{}, false
	}

	last := len(labels) - 1
	if labels[last-1] != "amazonaws" || labels[last] != "com" {
		return awsLoadBalancerHostnameParts{}, false
	}

	regionIndex := last - 3
	if labels[last-2] != "elb" {
		if labels[last-3] != "elb" {
			return awsLoadBalancerHostnameParts{}, false
		}
		regionIndex = last - 2
	}

	loadBalancerLabel := strings.TrimPrefix(labels[last-4], "internal-")
	if loadBalancerLabel == "" || labels[regionIndex] == "" {
		return awsLoadBalancerHostnameParts{}, false
	}
	if lastHyphen := strings.LastIndex(loadBalancerLabel, "-"); lastHyphen > 0 {
		loadBalancerLabel = loadBalancerLabel[:lastHyphen]
	}
	if loadBalancerLabel == "" {
		return awsLoadBalancerHostnameParts{}, false
	}
	return awsLoadBalancerHostnameParts{Name: loadBalancerLabel, Region: labels[regionIndex]}, true
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
		if appendContextError(ctx, &errs) {
			return errs
		}
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return append(errs, err)
		}
		for _, loadBalancer := range output.LoadBalancerDescriptions {
			if appendContextError(ctx, &errs) {
				return errs
			}
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
	appendContextError(ctx, &errs)
	return errs
}

func deleteNamedClassicLoadBalancers(ctx context.Context, client awsapi.ELBAPI, selector LoadBalancerSelector, names []string, log logr.Logger) []error {
	var errs []error
	for _, name := range names {
		if appendContextError(ctx, &errs) {
			break
		}
		output, err := client.DescribeLoadBalancers(ctx, &elasticloadbalancing.DescribeLoadBalancersInput{LoadBalancerNames: []string{name}})
		if isNotFound(err) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to find ELB: %w", err))
			continue
		}
		for _, loadBalancer := range output.LoadBalancerDescriptions {
			if appendContextError(ctx, &errs) {
				return errs
			}
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
			if appendContextError(ctx, &errs) {
				return errs
			}
			if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancing.DeleteLoadBalancerInput{LoadBalancerName: loadBalancer.LoadBalancerName}); err != nil && !isNotFound(err) {
				errs = append(errs, fmt.Errorf("failed to delete ELB: %w", err))
			} else if err == nil {
				log.Info("Deleted ELB")
			}
		}
	}
	appendContextError(ctx, &errs)
	return errs
}

func countClassicLoadBalancers(ctx context.Context, client awsapi.ELBAPI, selector LoadBalancerSelector) (int, error) {
	count := 0
	paginator := elasticloadbalancing.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancing.DescribeLoadBalancersInput{})
	for paginator.HasMorePages() {
		if err := ctx.Err(); err != nil {
			return count, err
		}
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
	if err := ctx.Err(); err != nil {
		return count, err
	}
	return count, nil
}

func deleteV2LoadBalancers(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector, log logr.Logger) []error {
	var errs []error
	lbPaginator := elasticloadbalancingv2.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancingv2.DescribeLoadBalancersInput{})
	for lbPaginator.HasMorePages() {
		if appendContextError(ctx, &errs) {
			return errs
		}
		output, err := lbPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, loadBalancer := range output.LoadBalancers {
			if appendContextError(ctx, &errs) {
				return errs
			}
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
	if appendContextError(ctx, &errs) {
		return errs
	}

	tgPaginator := elasticloadbalancingv2.NewDescribeTargetGroupsPaginator(client, &elasticloadbalancingv2.DescribeTargetGroupsInput{})
	for tgPaginator.HasMorePages() {
		if appendContextError(ctx, &errs) {
			return errs
		}
		output, err := tgPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, targetGroup := range output.TargetGroups {
			if appendContextError(ctx, &errs) {
				return errs
			}
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
	appendContextError(ctx, &errs)
	return errs
}

func deleteNamedV2LoadBalancers(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector, names []string, log logr.Logger) (sets.Set[string], []error) {
	targetGroupARNs, cleanupScope, loadBalancerErrs, targetGroupErrs, scanOrphanedTargetGroups := deleteNamedV2LoadBalancersWithoutScan(ctx, client, selector, names, log)
	var errs []error
	errs = append(errs, loadBalancerErrs...)
	errs = append(errs, targetGroupErrs...)
	if scanOrphanedTargetGroups {
		// Target groups can outlive a named load balancer that's gone or was partially removed in this pass.
		orphanedTargetGroupARNs, orphanedErrs := deleteOwnedV2TargetGroups(ctx, client, selector, cleanupScope, log)
		for arn := range orphanedTargetGroupARNs {
			targetGroupARNs.Insert(arn)
		}
		errs = append(errs, orphanedErrs...)
	}
	return targetGroupARNs, errs
}

func deleteNamedV2LoadBalancersWithoutScan(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector, names []string, log logr.Logger) (sets.Set[string], loadBalancerCleanupScope, []error, []error, bool) {
	targetGroupARNs := sets.New[string]()
	cleanupScope := loadBalancerCleanupScope{arns: sets.New[string](), missingNames: sets.New[string]()}
	var loadBalancerErrs, targetGroupErrs []error
	scanOrphanedTargetGroups := false
	for _, name := range names {
		if appendContextError(ctx, &loadBalancerErrs) {
			break
		}
		output, err := client.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{Names: []string{name}})
		if isNotFound(err) {
			cleanupScope.missingNames.Insert(name)
			scanOrphanedTargetGroups = true
			continue
		}
		if err != nil {
			loadBalancerErrs = append(loadBalancerErrs, fmt.Errorf("failed to find ELBV2 load balancer: %w", err))
			continue
		}
		if len(output.LoadBalancers) == 0 {
			cleanupScope.missingNames.Insert(name)
			scanOrphanedTargetGroups = true
			continue
		}
		for _, loadBalancer := range output.LoadBalancers {
			if appendContextError(ctx, &loadBalancerErrs) {
				break
			}
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
			if appendContextError(ctx, &loadBalancerErrs) {
				break
			}
			cleanupScope.arns.Insert(aws.ToString(loadBalancer.LoadBalancerArn))
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
	appendContextError(ctx, &loadBalancerErrs)
	return targetGroupARNs, cleanupScope, loadBalancerErrs, targetGroupErrs, scanOrphanedTargetGroups
}

func deleteOwnedV2TargetGroups(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector, cleanupScope loadBalancerCleanupScope, log logr.Logger) (sets.Set[string], []error) {
	targetGroupARNs := sets.New[string]()
	var errs []error
	ownedTargetGroupStillAttached := false
	paginator := elasticloadbalancingv2.NewDescribeTargetGroupsPaginator(client, &elasticloadbalancingv2.DescribeTargetGroupsInput{})
	for paginator.HasMorePages() {
		if appendContextError(ctx, &errs) {
			return targetGroupARNs, errs
		}
		output, err := paginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to list ELBV2 target groups for cleanup: %w", err))
			break
		}
		for _, targetGroup := range output.TargetGroups {
			if appendContextError(ctx, &errs) {
				return targetGroupARNs, errs
			}
			if selector.VPCID != aws.ToString(targetGroup.VpcId) || targetGroup.TargetGroupArn == nil {
				continue
			}
			if len(targetGroup.LoadBalancerArns) > 0 {
				selected, err := cleanupScope.matchesAttachment(targetGroup)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				if !selected {
					continue
				}
			}
			owned, err := v2ResourceHasClusterTag(ctx, client, targetGroup.TargetGroupArn, selector.InfraID)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to inspect target group tags: %w", err))
				continue
			}
			if owned {
				if len(targetGroup.LoadBalancerArns) > 0 {
					// Keep cleanup pending until AWS detaches the group from the selected load balancer.
					ownedTargetGroupStillAttached = true
					continue
				}
				targetGroupARNs.Insert(aws.ToString(targetGroup.TargetGroupArn))
			}
		}
	}
	if appendContextError(ctx, &errs) {
		return targetGroupARNs, errs
	}
	if ownedTargetGroupStillAttached {
		errs = append(errs, errOwnedTargetGroupStillAttached)
	}

	errs = append(errs, deleteV2TargetGroupsByARN(ctx, client, targetGroupARNs, log)...)
	return targetGroupARNs, errs
}

func deleteNamedV2LoadBalancer(ctx context.Context, client awsapi.ELBV2API, loadBalancer elbv2types.LoadBalancer, selector LoadBalancerSelector, log logr.Logger) (sets.Set[string], error) {
	targetGroupARNs, loadBalancerErr, targetGroupErr := deleteNamedV2LoadBalancerDetailed(ctx, client, loadBalancer, selector, log)
	return targetGroupARNs, errors.Join(loadBalancerErr, targetGroupErr)
}

func deleteNamedV2LoadBalancerDetailed(ctx context.Context, client awsapi.ELBV2API, loadBalancer elbv2types.LoadBalancer, selector LoadBalancerSelector, log logr.Logger) (sets.Set[string], error, error) {
	targetGroupARNs, targetGroupErrs := findOwnedV2TargetGroupsForLoadBalancer(ctx, client, loadBalancer.LoadBalancerArn, selector)
	if appendContextError(ctx, &targetGroupErrs) {
		return targetGroupARNs, nil, errors.Join(targetGroupErrs...)
	}

	listenerARNs, loadBalancerErrs := describeV2LoadBalancerListeners(ctx, client, loadBalancer.LoadBalancerArn)
	if appendContextError(ctx, &loadBalancerErrs) {
		return targetGroupARNs, errors.Join(loadBalancerErrs...), errors.Join(targetGroupErrs...)
	}
	for _, listenerARN := range listenerARNs {
		if appendContextError(ctx, &loadBalancerErrs) {
			return targetGroupARNs, errors.Join(loadBalancerErrs...), errors.Join(targetGroupErrs...)
		}
		if _, err := client.DeleteListener(ctx, &elasticloadbalancingv2.DeleteListenerInput{ListenerArn: listenerARN}); err != nil && !isNotFound(err) {
			loadBalancerErrs = append(loadBalancerErrs, fmt.Errorf("failed to delete listener for ELBV2 load balancer: %w", err))
		}
	}
	if appendContextError(ctx, &loadBalancerErrs) {
		return targetGroupARNs, errors.Join(loadBalancerErrs...), errors.Join(targetGroupErrs...)
	}

	if _, err := client.DeleteLoadBalancer(ctx, &elasticloadbalancingv2.DeleteLoadBalancerInput{LoadBalancerArn: loadBalancer.LoadBalancerArn}); err != nil && !isNotFound(err) {
		loadBalancerErrs = append(loadBalancerErrs, fmt.Errorf("failed to delete ELBV2 load balancer: %w", err))
	} else if err == nil {
		log.Info("Deleted ELBV2 load balancer")
	}
	if appendContextError(ctx, &loadBalancerErrs) {
		return targetGroupARNs, errors.Join(loadBalancerErrs...), errors.Join(targetGroupErrs...)
	}

	targetGroupErrs = append(targetGroupErrs, deleteV2TargetGroupsByARN(ctx, client, targetGroupARNs, log)...)

	return targetGroupARNs, errors.Join(loadBalancerErrs...), errors.Join(targetGroupErrs...)
}

func findOwnedV2TargetGroupsForLoadBalancer(ctx context.Context, client awsapi.ELBV2API, loadBalancerARN *string, selector LoadBalancerSelector) (sets.Set[string], []error) {
	targetGroupARNs := sets.New[string]()
	var errs []error
	paginator := elasticloadbalancingv2.NewDescribeTargetGroupsPaginator(client, &elasticloadbalancingv2.DescribeTargetGroupsInput{LoadBalancerArn: loadBalancerARN})
	for paginator.HasMorePages() {
		if appendContextError(ctx, &errs) {
			return targetGroupARNs, errs
		}
		output, err := paginator.NextPage(ctx)
		if isNotFound(err) {
			break
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to find target groups for ELBV2 load balancer: %w", err))
			break
		}
		for _, targetGroup := range output.TargetGroups {
			if appendContextError(ctx, &errs) {
				break
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
	appendContextError(ctx, &errs)
	return targetGroupARNs, errs
}

func describeV2LoadBalancerListeners(ctx context.Context, client awsapi.ELBV2API, loadBalancerARN *string) ([]*string, []error) {
	var listenerARNs []*string
	var errs []error
	paginator := elasticloadbalancingv2.NewDescribeListenersPaginator(client, &elasticloadbalancingv2.DescribeListenersInput{LoadBalancerArn: loadBalancerARN})
	for paginator.HasMorePages() {
		if appendContextError(ctx, &errs) {
			return listenerARNs, errs
		}
		output, err := paginator.NextPage(ctx)
		if isNotFound(err) {
			break
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to find listeners for ELBV2 load balancer: %w", err))
			break
		}
		for _, listener := range output.Listeners {
			listenerARNs = append(listenerARNs, listener.ListenerArn)
		}
	}
	appendContextError(ctx, &errs)
	return listenerARNs, errs
}

func deleteV2TargetGroupsByARN(ctx context.Context, client awsapi.ELBV2API, targetGroupARNs sets.Set[string], log logr.Logger) []error {
	arns := make([]string, 0, len(targetGroupARNs))
	for arn := range targetGroupARNs {
		arns = append(arns, arn)
	}
	sort.Strings(arns)

	var errs []error
	for _, arn := range arns {
		if appendContextError(ctx, &errs) {
			return errs
		}
		if _, err := client.DeleteTargetGroup(ctx, &elasticloadbalancingv2.DeleteTargetGroupInput{TargetGroupArn: aws.String(arn)}); err != nil && !isNotFound(err) {
			errs = append(errs, fmt.Errorf("failed to delete target group: %w", err))
		} else if err == nil {
			log.Info("Deleted target group")
		}
	}
	appendContextError(ctx, &errs)
	return errs
}

func countV2LoadBalancerResources(ctx context.Context, client awsapi.ELBV2API, selector LoadBalancerSelector) (int, error) {
	count := 0
	var errs []error

	lbPaginator := elasticloadbalancingv2.NewDescribeLoadBalancersPaginator(client, &elasticloadbalancingv2.DescribeLoadBalancersInput{})
	for lbPaginator.HasMorePages() {
		if appendContextError(ctx, &errs) {
			break
		}
		output, err := lbPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, loadBalancer := range output.LoadBalancers {
			if appendContextError(ctx, &errs) {
				break
			}
			if selector.VPCID == aws.ToString(loadBalancer.VpcId) {
				count++
			}
		}
	}
	if appendContextError(ctx, &errs) {
		return count, errors.Join(errs...)
	}

	tgPaginator := elasticloadbalancingv2.NewDescribeTargetGroupsPaginator(client, &elasticloadbalancingv2.DescribeTargetGroupsInput{})
	for tgPaginator.HasMorePages() {
		if appendContextError(ctx, &errs) {
			break
		}
		output, err := tgPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, targetGroup := range output.TargetGroups {
			if appendContextError(ctx, &errs) {
				break
			}
			if selector.VPCID == aws.ToString(targetGroup.VpcId) {
				count++
			}
		}
	}
	appendContextError(ctx, &errs)

	return count, errors.Join(errs...)
}

func countNamedLoadBalancerResources(ctx context.Context, clients LoadBalancerClients, selector LoadBalancerSelector, names []string, targetGroupARNs sets.Set[string]) (int, error) {
	count := 0
	var errs []error
	if clients.ELB != nil {
		for _, name := range names {
			if appendContextError(ctx, &errs) {
				return count, errors.Join(errs...)
			}
			output, err := clients.ELB.DescribeLoadBalancers(ctx, &elasticloadbalancing.DescribeLoadBalancersInput{LoadBalancerNames: []string{name}})
			if isNotFound(err) {
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to verify ELB: %w", err))
				continue
			}
			for _, loadBalancer := range output.LoadBalancerDescriptions {
				if appendContextError(ctx, &errs) {
					return count, errors.Join(errs...)
				}
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
			if appendContextError(ctx, &errs) {
				return count, errors.Join(errs...)
			}
			output, err := clients.ELBV2.DescribeLoadBalancers(ctx, &elasticloadbalancingv2.DescribeLoadBalancersInput{Names: []string{name}})
			if isNotFound(err) {
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to verify ELBV2 load balancer: %w", err))
				continue
			}
			for _, loadBalancer := range output.LoadBalancers {
				if appendContextError(ctx, &errs) {
					return count, errors.Join(errs...)
				}
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
			if appendContextError(ctx, &errs) {
				return count, errors.Join(errs...)
			}
			output, err := clients.ELBV2.DescribeTargetGroups(ctx, &elasticloadbalancingv2.DescribeTargetGroupsInput{TargetGroupArns: []string{arn}})
			if isNotFound(err) {
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to verify target group: %w", err))
				continue
			}
			for _, targetGroup := range output.TargetGroups {
				if appendContextError(ctx, &errs) {
					return count, errors.Join(errs...)
				}
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
	appendContextError(ctx, &errs)
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
		if appendContextError(ctx, &errs) {
			return count, errors.Join(errs...)
		}
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
			if appendContextError(ctx, &errs) {
				return count, errors.Join(errs...)
			}
			if _, err := v2ResourceHasClusterTag(ctx, client, targetGroup.TargetGroupArn, selector.InfraID); err != nil {
				errs = append(errs, fmt.Errorf("failed to verify target group tags: %w", err))
			}
		}
	}
	appendContextError(ctx, &errs)
	return count, errors.Join(errs...)
}

func appendContextError(ctx context.Context, errs *[]error) bool {
	if err := ctx.Err(); err != nil {
		for _, existing := range *errs {
			if errors.Is(existing, err) {
				return true
			}
		}
		*errs = append(*errs, err)
		return true
	}
	return false
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
