package resources

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	awsutil "github.com/openshift/hypershift/support/awsutil"
)

type cloudResourceCleanupComponent string

const (
	cloudResourceCleanupComponentEligibility      cloudResourceCleanupComponent = "cleanup eligibility check"
	cloudResourceCleanupComponentResourceCreation cloudResourceCleanupComponent = "resource creation blocking"
	cloudResourceCleanupComponentImageRegistry    cloudResourceCleanupComponent = "image registry cleanup"
	cloudResourceCleanupComponentIngress          cloudResourceCleanupComponent = "ingress controller cleanup"
	cloudResourceCleanupComponentAWSLoadBalancers cloudResourceCleanupComponent = "AWS load balancer cleanup"
	cloudResourceCleanupComponentLoadBalancers    cloudResourceCleanupComponent = "load balancer cleanup"
	cloudResourceCleanupComponentPersistentVolume cloudResourceCleanupComponent = "persistent volume cleanup"
	cloudResourceCleanupComponentVolumeSnapshot   cloudResourceCleanupComponent = "volume snapshot cleanup"
)

type safeLoadBalancerCleanupError struct {
	message string
	err     error
}

func (e *safeLoadBalancerCleanupError) Error() string { return e.message }
func (e *safeLoadBalancerCleanupError) Unwrap() error { return e.err }

func newSafeLoadBalancerCleanupError(message string, err error) error {
	return &safeLoadBalancerCleanupError{message: message, err: err}
}

type cloudResourceCleanupComponentError struct {
	component cloudResourceCleanupComponent
	err       error
}

func (e *cloudResourceCleanupComponentError) Error() string {
	return fmt.Sprintf("%s failed", e.component)
}

func (e *cloudResourceCleanupComponentError) Unwrap() error { return e.err }

func withCloudResourceCleanupComponent(component cloudResourceCleanupComponent, err error) error {
	if err == nil {
		return nil
	}
	return &cloudResourceCleanupComponentError{component: component, err: err}
}

func cloudResourcesDestroyedErrorMessage(err error) string {
	return fmt.Sprintf("Error: %s", safeCloudResourcesDestroyedErrorMessage(err))
}

func safeCloudResourcesDestroyedErrorMessage(err error) string {
	summary := cloudResourceCleanupErrorSummary{components: map[cloudResourceCleanupComponent]struct{}{}}
	collectCloudResourceCleanupError(err, "", &summary)

	if len(summary.components) == 0 {
		if message, ok := safeLoadBalancerCleanupErrorMessage(err); ok {
			return message
		}
		if summary.awsDescribeTagsDenials > 0 {
			if summary.awsOtherFailures == 0 && summary.otherFailures == 0 {
				return describeTagsAccessDeniedCleanupMessage
			}
			return mixedDescribeTagsCleanupMessage
		}
		return genericCloudResourceCleanupMessage
	}

	components := make([]cloudResourceCleanupComponent, 0, len(summary.components))
	for component := range summary.components {
		components = append(components, component)
	}
	sort.Slice(components, func(i, j int) bool { return components[i] < components[j] })
	messages := make([]string, 0, len(components)+len(summary.safeMessages)+2)
	for _, component := range components {
		messages = appendUniqueCloudCleanupMessage(messages, fmt.Sprintf("%s failed", string(component)))
	}

	if summary.awsDescribeTagsDenials > 0 {
		if summary.awsOtherFailures == 0 {
			messages = appendUniqueCloudCleanupMessage(messages, describeTagsAccessDeniedCleanupMessage)
		} else {
			messages = appendUniqueCloudCleanupMessage(messages, mixedDescribeTagsCleanupMessage)
		}
	} else {
		for _, message := range summary.safeMessages {
			messages = appendUniqueCloudCleanupMessage(messages, message)
		}
	}

	if summary.otherFailures > 0 || (summary.awsOtherFailures > 0 && len(summary.safeMessages) == 0) || len(messages) == len(components) {
		messages = appendUniqueCloudCleanupMessage(messages, genericCloudResourceCleanupMessage)
	}
	return strings.Join(messages, "; ")
}

const (
	describeTagsAccessDeniedCleanupMessage = "AWS load balancer ownership verification was denied; the delegated role needs elasticloadbalancing:DescribeTags, Kubernetes cleanup was requested, and AWS resources may remain"
	mixedDescribeTagsCleanupMessage        = "AWS cleanup remains incomplete; the delegated role needs elasticloadbalancing:DescribeTags, and other cleanup failures or ownership issues may also be present"
	genericCloudResourceCleanupMessage     = "cloud resource cleanup failed; error details were omitted to avoid exposing sensitive resource or endpoint information. " +
		"Check hosted-cluster-config-operator logs in the control plane namespace for cleanup progress; verify the failing component's credentials, permissions, and connectivity"
)

type cloudResourceCleanupErrorSummary struct {
	components             map[cloudResourceCleanupComponent]struct{}
	safeMessages           []string
	awsDescribeTagsDenials int
	awsOtherFailures       int
	otherFailures          int
}

func collectCloudResourceCleanupError(err error, component cloudResourceCleanupComponent, summary *cloudResourceCleanupErrorSummary) {
	if err == nil {
		return
	}
	if aggregate, ok := err.(interface{ Errors() []error }); ok {
		children := aggregate.Errors()
		if len(children) == 0 {
			recordCloudResourceCleanupFailure(err, component, summary)
			return
		}
		for _, child := range children {
			collectCloudResourceCleanupError(child, component, summary)
		}
		return
	}
	if aggregate, ok := err.(interface{ Unwrap() []error }); ok {
		children := aggregate.Unwrap()
		if len(children) == 0 {
			recordCloudResourceCleanupFailure(err, component, summary)
			return
		}
		for _, child := range children {
			collectCloudResourceCleanupError(child, component, summary)
		}
		return
	}
	var componentErr *cloudResourceCleanupComponentError
	if errors.As(err, &componentErr) {
		summary.components[componentErr.component] = struct{}{}
		collectCloudResourceCleanupError(componentErr.err, componentErr.component, summary)
		return
	}
	var safeErr *safeLoadBalancerCleanupError
	if errors.As(err, &safeErr) {
		if component == cloudResourceCleanupComponentAWSLoadBalancers {
			summary.safeMessages = appendUniqueCloudCleanupMessage(summary.safeMessages, safeErr.message)
		}
		collectCloudResourceCleanupError(safeErr.err, component, summary)
		return
	}
	if (component == cloudResourceCleanupComponentAWSLoadBalancers || component == "") && awsutil.IsOnlyDescribeTagsAccessDenied(err) {
		summary.awsDescribeTagsDenials++
		return
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok && wrapped.Unwrap() != nil {
		collectCloudResourceCleanupError(wrapped.Unwrap(), component, summary)
		return
	}
	recordCloudResourceCleanupFailure(err, component, summary)
}

func recordCloudResourceCleanupFailure(err error, component cloudResourceCleanupComponent, summary *cloudResourceCleanupErrorSummary) {
	if component == cloudResourceCleanupComponentAWSLoadBalancers && (awsutil.IsOnlyDescribeTagsAccessDenied(err) || errors.Is(err, awsutil.ErrDescribeTagsAccessDenied)) {
		summary.awsDescribeTagsDenials++
		return
	}
	if component == "" && (awsutil.IsOnlyDescribeTagsAccessDenied(err) || errors.Is(err, awsutil.ErrDescribeTagsAccessDenied)) {
		summary.awsDescribeTagsDenials++
		return
	}
	if component == cloudResourceCleanupComponentAWSLoadBalancers {
		if errors.Is(err, awsutil.ErrLoadBalancerOwnershipUnverified) {
			// Ownership failures are surfaced as pending cleanup by the candidate-verification path.
			return
		}
		summary.awsOtherFailures++
		return
	}
	summary.otherFailures++
}

func appendUniqueCloudCleanupMessage(messages []string, message string) []string {
	for _, existing := range messages {
		if existing == message {
			return messages
		}
	}
	return append(messages, message)
}

func safeLoadBalancerCleanupErrorMessage(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	if aggregate, ok := err.(interface{ Errors() []error }); ok {
		children := aggregate.Errors()
		if len(children) != 1 {
			return "", false
		}
		return safeLoadBalancerCleanupErrorMessage(children[0])
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		children := multi.Unwrap()
		if len(children) != 1 {
			return "", false
		}
		return safeLoadBalancerCleanupErrorMessage(children[0])
	}
	var safeErr *safeLoadBalancerCleanupError
	if errors.As(err, &safeErr) {
		return safeErr.message, true
	}
	if single, ok := err.(interface{ Unwrap() error }); ok {
		return safeLoadBalancerCleanupErrorMessage(single.Unwrap())
	}
	return "", false
}
