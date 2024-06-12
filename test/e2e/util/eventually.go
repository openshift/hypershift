package util

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func defaultOptions() *EventuallyOptions {
	return &EventuallyOptions{
		interval:       1 * time.Second,
		timeout:        10 * time.Minute,
		immediate:      true,
		dumpConditions: true,
	}
}

// EventuallyOptions configure asynchronous assertion behavior.
type EventuallyOptions struct {
	interval  time.Duration
	timeout   time.Duration
	immediate bool

	dumpConditions      bool
	filterConditionDump []Condition
}

// EventuallyOption configures a
type EventuallyOption func(*EventuallyOptions)

// WithInterval sets the polling interval.
func WithInterval(interval time.Duration) EventuallyOption {
	return func(o *EventuallyOptions) {
		o.interval = interval
	}
}

// WithTimeout sets the polling timeout.
func WithTimeout(timeout time.Duration) EventuallyOption {
	return func(o *EventuallyOptions) {
		o.timeout = timeout
	}
}

// WithDelayedStart configures the asynchronous assertion to start immediately.
func WithDelayedStart() EventuallyOption {
	return func(o *EventuallyOptions) {
		o.immediate = false
	}
}

// WithoutConditionDump configures the asynchronous assertion to dump conditions on failure.
func WithoutConditionDump() EventuallyOption {
	return func(o *EventuallyOptions) {
		o.dumpConditions = false
	}
}

// WithFilteredConditionDump configures the asynchronous assertion to only dump the specified conditions.
func WithFilteredConditionDump(matchers ...Condition) EventuallyOption {
	return func(o *EventuallyOptions) {
		o.filterConditionDump = append(o.filterConditionDump, matchers...)
	}
}

// EventuallyObject polls until the predicate is fulfilled on the object.
func EventuallyObject[T client.Object](t *testing.T, ctx context.Context, objective string, getter func(context.Context) (T, error), predicate Predicate[T], options ...EventuallyOption) {
	t.Helper()
	opts := defaultOptions()
	for _, option := range options {
		option(opts)
	}

	t.Logf("waiting for %s", objective)
	start := time.Now()
	lastTimestamp := time.Now()
	var previousError string
	var previousReasons []string
	var object T
	err := wait.PollUntilContextTimeout(ctx, opts.interval, opts.timeout, opts.immediate, func(ctx context.Context) (bool, error) {
		var getErr error
		object, getErr = getter(ctx)
		if getErr != nil {
			if getErr.Error() != previousError {
				previousError = getErr.Error()
				t.Logf("failed to get %T: %v", object, getErr)
			}
			return false, nil
		}

		done, reasons, err := predicate(object)
		if err != nil {
			return false, err
		}
		if !done && len(reasons) == 0 {
			return false, fmt.Errorf("programmer error: predicate returned false with no message")
		}
		if diff := cmp.Diff(previousReasons, reasons); diff != "" {
			prefix := ""
			if !done {
				prefix = "in"
			}
			t.Logf("observed %T %s/%s %svalid at RV %s after %s:", object, object.GetNamespace(), object.GetName(), prefix, object.GetResourceVersion(), time.Since(lastTimestamp))
			for _, message := range reasons {
				t.Log(" - " + message)
			}
			previousReasons = reasons
		}

		lastTimestamp = time.Now()
		return done, nil
	})
	duration := time.Since(start).Round(time.Second)

	if err != nil {
		if opts.dumpConditions && !reflect.ValueOf(object).Elem().IsZero() { // can't use != nil here
			conditions, err := Conditions(object)
			if err != nil {
				t.Fatalf("failed to extract conditions from %T %s/%s: %v", object, object.GetNamespace(), object.GetName(), err)
			}
			t.Logf("%T %s/%s conditions:", object, object.GetNamespace(), object.GetName())
			for _, condition := range conditions {
				for _, matcher := range opts.filterConditionDump {
					if matcher.Matches(condition) {
						t.Logf(condition.String())
					}
				}
			}
		}
		t.Fatalf("Failed to wait for %s in %s: %v", objective, duration, err)
	}
	t.Logf("Successfully waited for %s in %s", objective, duration)
}

type predicateResult struct {
	done    bool
	reasons []string
}

// EventuallyObjects polls until the predicate is fulfilled on each of a set of objects.
func EventuallyObjects[T client.Object](t *testing.T, ctx context.Context, objective string, getter func(context.Context) ([]T, error), groupPredicate Predicate[[]T], predicate Predicate[T], options ...EventuallyOption) {
	t.Helper()
	opts := defaultOptions()
	for _, option := range options {
		option(opts)
	}

	t.Logf("waiting for %s", objective)
	start := time.Now()
	lastTimestamp := time.Now()
	var previousError string
	previousResults := map[types.NamespacedName]predicateResult{}
	var invalidObjects []T
	err := wait.PollUntilContextTimeout(ctx, opts.interval, opts.timeout, opts.immediate, func(ctx context.Context) (bool, error) {
		objects, getErr := getter(ctx)
		if getErr != nil {
			if getErr.Error() != previousError {
				previousError = getErr.Error()
				t.Logf("failed to get %T: %v", new(T), getErr)
			}
			return false, nil
		}

		currentResults := map[types.NamespacedName]predicateResult{}
		done, reasons, err := groupPredicate(objects)
		if err != nil {
			return false, err
		}
		if !done && len(reasons) == 0 {
			return false, fmt.Errorf("programmer error: predicate returned false with no message")
		}
		currentResults[types.NamespacedName{ /* empty sentinel */ }] = predicateResult{
			done:    done,
			reasons: reasons,
		}

		for _, object := range objects {
			objectDone, objectReasons, objectError := predicate(object)
			if objectError != nil {
				return false, objectError
			}
			if !objectDone && len(objectReasons) == 0 {
				return false, fmt.Errorf("programmer error: predicate returned false with no message")
			}
			currentResults[types.NamespacedName{Namespace: object.GetNamespace(), Name: object.GetName()}] = predicateResult{
				done:    objectDone,
				reasons: objectReasons,
			}
			if !objectDone {
				invalidObjects = append(invalidObjects, object)
			}
			done = done && objectDone
		}
		if diff := cmp.Diff(previousResults, currentResults, cmp.AllowUnexported(predicateResult{})); diff != "" {
			prefix := ""
			if !done {
				prefix = "in"
			}
			t.Logf("observed %svalid %T state after %s", prefix, new(T), time.Since(lastTimestamp))
			for key, result := range currentResults {
				previous, seen := previousResults[key]
				if diff := cmp.Diff(previous.reasons, result.reasons); !seen || diff != "" {
					prefix := ""
					if !result.done {
						prefix = "in"
					}
					identifier := "collection"
					if diff := cmp.Diff(key, types.NamespacedName{}); diff != "" {
						identifier = fmt.Sprintf("%s/%s", key.Namespace, key.Name)
					}
					t.Logf(" - observed %T %s %svalid:", new(T), identifier, prefix)
					for _, message := range result.reasons {
						t.Log("    - " + message)
					}
				}
			}
			previousResults = currentResults
		}

		lastTimestamp = time.Now()
		return done, nil
	})
	duration := time.Since(start).Round(time.Second)

	if err != nil {
		if opts.dumpConditions {
			for _, object := range invalidObjects {
				if !reflect.ValueOf(object).Elem().IsZero() { // can't use != nil here
					conditions, err := Conditions(object)
					if err != nil {
						t.Errorf("failed to extract conditions from %T %s/%s: %v", object, object.GetNamespace(), object.GetName(), err)
						continue
					}
					t.Logf("%T %s/%s conditions:", object, object.GetNamespace(), object.GetName())
					for _, condition := range conditions {
						for _, matcher := range opts.filterConditionDump {
							if matcher.Matches(condition) {
								t.Logf(condition.String())
							}
						}
					}
				}
			}
		}
		t.Fatalf("Failed to wait for %s in %s: %v", objective, duration, err)
	}
	t.Logf("Successfully waited for %s in %s", objective, duration)
}

// Predicate evaluates an object. Return whether the object in question matches your predicate, the reasons
// why or why not, and whether an error occurred. If determining that an object does not match a predicate,
// a message is required. Returning an error is fatal to the asynchronous assertion using this predicate.
type Predicate[T any] func(T) (done bool, reasons []string, err error)

// AggregatePredicates closes over a logical AND for all the child predicates.
func AggregatePredicates[T any](predicates ...Predicate[T]) Predicate[T] {
	return func(item T) (bool, []string, error) {
		var allReasons []string
		allDone := true
		for _, predicate := range predicates {
			done, reasons, err := predicate(item)
			if err != nil {
				return false, nil, err
			}
			allReasons = append(allReasons, reasons...)
			allDone = allDone && done
		}
		return allDone, allReasons, nil
	}
}

// Condition is a generic structure required to adapt all the different concrete condition types into one.
type Condition struct {
	Type    string
	Status  metav1.ConditionStatus
	Reason  string
	Message string
}

// String formats a condition in the canonical way.
func (c Condition) String() string {
	msg := fmt.Sprintf("%s=%s", c.Type, c.Status)
	if c.Reason != "" {
		msg += ": " + c.Reason
	}
	if c.Message != "" {
		msg += "(" + c.Message + ")"
	}
	return msg
}

// Conditions extracts conditions from the item and adapts them to the generic wrapper.
func Conditions(item client.Object) ([]Condition, error) {
	switch obj := item.(type) {
	case *corev1.Node:
		conditions := make([]Condition, len(obj.Status.Conditions))
		for _, condition := range obj.Status.Conditions {
			conditions = append(conditions, Condition{
				Type:    string(condition.Type),
				Status:  metav1.ConditionStatus(condition.Status),
				Reason:  condition.Reason,
				Message: condition.Message,
			})
		}
		return conditions, nil
	case *hyperv1.NodePool:
		conditions := make([]Condition, len(obj.Status.Conditions))
		for _, condition := range obj.Status.Conditions {
			conditions = append(conditions, Condition{
				Type:    condition.Type,
				Status:  metav1.ConditionStatus(condition.Status),
				Reason:  condition.Reason,
				Message: condition.Message,
			})
		}
		return conditions, nil
	case *hyperv1.HostedCluster:
		return adaptConditions(obj.Status.Conditions), nil
	case *hyperv1.HostedControlPlane:
		return adaptConditions(obj.Status.Conditions), nil
	case *certificatesv1alpha1.CertificateRevocationRequest:
		return adaptConditions(obj.Status.Conditions), nil
	case *certificatesv1.CertificateSigningRequest:
		conditions := make([]Condition, len(obj.Status.Conditions))
		for _, condition := range obj.Status.Conditions {
			conditions = append(conditions, Condition{
				Type:    string(condition.Type),
				Status:  metav1.ConditionStatus(condition.Status),
				Reason:  condition.Reason,
				Message: condition.Message,
			})
		}
		return conditions, nil
	default:
		return nil, fmt.Errorf("object %T unknown", item)
	}
}

func adaptConditions(in []metav1.Condition) []Condition {
	conditions := make([]Condition, len(in))
	for _, condition := range in {
		conditions = append(conditions, Condition{
			Type:    condition.Type,
			Status:  condition.Status,
			Reason:  condition.Reason,
			Message: condition.Message,
		})
	}
	return conditions
}

func (needle Condition) Matches(condition Condition) bool {
	return (needle.Status == "" || needle.Status == condition.Status) &&
		(needle.Reason == "" || needle.Reason == condition.Reason) &&
		(needle.Message == "" || needle.Message == condition.Message)
}

// ConditionPredicate returns a predicate that validates that a particular condition type exists and has the requisite status, reason and/or message.
func ConditionPredicate[T client.Object](needle Condition) Predicate[T] {
	return func(item T) (bool, []string, error) {
		haystack, err := Conditions(item)
		if err != nil {
			return false, nil, err
		}
		for _, condition := range haystack {
			if needle.Type == condition.Type {
				valid := needle.Matches(condition)
				prefix := ""
				if !valid {
					prefix = "in"
				}
				return valid, []string{fmt.Sprintf("%scorrect condition: wanted %s, got %s", prefix, needle.String(), condition.String())}, nil
			}
		}

		return false, []string{fmt.Sprintf("missing condition: wanted %s, did not find condition of this type", needle.String())}, nil
	}
}

func NoOpPredicate[T any]() Predicate[T] {
	return func(item T) (bool, []string, error) {
		return true, nil, nil
	}
}
