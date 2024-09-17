package util

import (
	"context"
	"fmt"
	"os"
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
func EventuallyObject[T client.Object](t *testing.T, ctx context.Context, objective string, getter func(context.Context) (T, error), predicates []Predicate[T], options ...EventuallyOption) {
	t.Helper()
	opts := defaultOptions()
	for _, option := range options {
		option(opts)
	}

	if os.Getenv("EVENTUALLY_VERBOSE") != "false" {
		t.Logf("Waiting for %s", objective)
	}
	start := time.Now()
	lastTimestamp := time.Now()
	var previousError string
	var previousResults []predicateResult
	var object T
	err := wait.PollUntilContextTimeout(ctx, opts.interval, opts.timeout, opts.immediate, func(ctx context.Context) (bool, error) {
		obj, getErr := getter(ctx)
		if getErr != nil {
			if getErr.Error() != previousError {
				previousError = getErr.Error()
				t.Logf("Failed to get %T: %v", object, getErr)
			}
			return false, nil
		}
		object = obj // only update the shared state if we didn't error on fetch

		currentResults, err := evaluatePredicates(object, predicates)
		if err != nil {
			return false, err
		}
		done := summarizePredicteResults(currentResults)
		if os.Getenv("EVENTUALLY_VERBOSE") != "false" {
			printStatus(t, lastTimestamp, object, done, diffPredicateResults(previousResults, currentResults))
		}

		previousResults = currentResults
		lastTimestamp = time.Now()
		return done, nil
	})
	duration := time.Since(start).Round(25 * time.Millisecond)

	if err != nil {
		t.Errorf("Failed to wait for %s in %s: %v", objective, duration, err)
		if !(reflect.ValueOf(object).IsZero() || reflect.ValueOf(object).Elem().IsZero()) { // can't use != nil here
			// evaluate the predicates one last time to give a summary of *only* the failed ones
			results, resultsErr := evaluatePredicates(object, predicates)
			if resultsErr != nil {
				t.Errorf("Failed to evaluate predicates: %v", resultsErr)
			}
			done := summarizePredicteResults(results)
			if !done {
				var reasons []string
				for _, result := range results {
					if !result.done {
						reasons = append(reasons, result.reason)
					}
				}
				printStatus(t, start, object, done, reasons)
			}

			if opts.dumpConditions {
				conditions, conditionsErr := Conditions(object)
				if conditionsErr != nil {
					t.Fatalf("Failed to extract conditions from %T %s/%s: %v", object, object.GetNamespace(), object.GetName(), conditionsErr)
				}
				t.Logf("%T %s/%s conditions:", object, object.GetNamespace(), object.GetName())
				for _, condition := range conditions {
					matches := len(opts.filterConditionDump) == 0
					for _, matcher := range opts.filterConditionDump {
						matches = matches || matcher.Matches(condition)
					}
					if matches {
						t.Logf("Condition: %s", condition)
					}
				}
			}
		}
		t.FailNow()
	}
	t.Logf("Successfully waited for %s in %s", objective, duration)
}

func evaluatePredicates[T any](object T, predicates []Predicate[T]) ([]predicateResult, error) {
	if reflect.TypeOf(object).Kind() != reflect.Slice && (reflect.ValueOf(object).IsZero() || reflect.ValueOf(object).Elem().IsZero()) { // can't use != nil here
		panic(fmt.Sprintf("programmer error: can't evaluate predicates on empty object %#v", object))
	}
	results := make([]predicateResult, len(predicates))
	for i, predicate := range predicates {
		if predicate == nil {
			panic(fmt.Sprintf("programmer error: can't evaluate empty predicate %d: %#v", i, object))
		}
		done, reason, err := predicate(object)
		if err != nil {
			return nil, err
		}
		if !done && len(reason) == 0 {
			panic("programmer error: predicate returned false with no message")
		}
		results[i] = predicateResult{done: done, reason: reason}
	}
	return results, nil
}

// diffPredicateResults divulges the reasons that changed from before to after. The lists of results must be in the same order
// and contain the results of the same number of predicates
func diffPredicateResults(before, after []predicateResult) []string {
	var diff []string
	if len(before) != 0 && len(before) != len(after) {
		panic(fmt.Sprintf("programmer error: predicates are different lengths, before=%d, after=%d", len(before), len(after)))
	}
	for i := range after {
		if len(before) == 0 || before[i].reason != after[i].reason {
			diff = append(diff, after[i].reason)
		}
	}
	return diff
}

// summarizePredicteResults summarizes the predicates to determine if the conditions have been met
func summarizePredicteResults(results []predicateResult) bool {
	done := true
	for i := range results {
		done = done && results[i].done
	}
	return done
}

func printStatus[T client.Object](t *testing.T, lastTimestamp time.Time, object T, done bool, reasons []string) {
	if len(reasons) == 0 {
		return
	}

	prefix := ""
	if !done {
		prefix = "in"
	}
	suffix := ""
	if len(reasons) == 1 {
		suffix = " " + reasons[0]
	}
	t.Logf("observed %T %s/%s %svalid at RV %s after %s:%s", object, object.GetNamespace(), object.GetName(), prefix, object.GetResourceVersion(), time.Since(lastTimestamp).Round(25*time.Millisecond), suffix)
	if len(reasons) > 1 {
		for _, message := range reasons {
			t.Log(" - " + message)
		}
	}
}

type predicateResult struct {
	done   bool
	reason string
}

// EventuallyObjects polls until the predicate is fulfilled on each of a set of objects.
func EventuallyObjects[T client.Object](t *testing.T, ctx context.Context, objective string, getter func(context.Context) ([]T, error), groupPredicates []Predicate[[]T], predicates []Predicate[T], options ...EventuallyOption) {
	t.Helper()
	opts := defaultOptions()
	for _, option := range options {
		option(opts)
	}

	if os.Getenv("EVENTUALLY_VERBOSE") != "false" {
		t.Logf("Waiting for %s", objective)
	}
	start := time.Now()
	lastTimestamp := time.Now()
	var previousError string
	previousResults := map[types.NamespacedName][]predicateResult{}
	var objects []T
	err := wait.PollUntilContextTimeout(ctx, opts.interval, opts.timeout, opts.immediate, func(ctx context.Context) (bool, error) {
		objs, getErr := getter(ctx)
		if getErr != nil {
			if getErr.Error() != previousError {
				previousError = getErr.Error()
				t.Logf("Failed to get %T: %v", new(T), getErr)
			}
			return false, nil
		}
		objects = objs // only update the shared state if we didn't error on fetch

		currentResults, err := evaluateCollectionPredicates(objects, groupPredicates, predicates)
		if err != nil {
			return false, err
		}
		done := summarizeCollectionPredicateResults(currentResults)
		if diff := cmp.Diff(previousResults, currentResults, cmp.AllowUnexported(predicateResult{})); diff != "" && os.Getenv("EVENTUALLY_VERBOSE") != "false" {
			reasons := map[types.NamespacedName]predicateReasons{}
			for key, results := range currentResults {
				if diff := diffPredicateResults(previousResults[key], results); len(diff) > 0 {
					reasons[key] = predicateReasons{
						done:    summarizePredicteResults(results),
						reasons: diff,
					}
				}
			}
			printCollectionStatus[T](t, lastTimestamp, done, reasons)
		}
		previousResults = currentResults

		lastTimestamp = time.Now()
		return done, nil
	})
	duration := time.Since(start).Round(25 * time.Millisecond)

	if err != nil {
		t.Errorf("Failed to wait for %s in %s: %v", objective, duration, err)
		// evaluate the predicates one last time to give a summary of *only* the failed ones
		finalResults, resultErr := evaluateCollectionPredicates(objects, groupPredicates, predicates)
		if resultErr != nil {
			t.Errorf("Failed to evaluate predicates: %v", resultErr)
		}
		done := summarizeCollectionPredicateResults(finalResults)
		if !done {
			reasons := map[types.NamespacedName]predicateReasons{}
			for key, results := range finalResults {
				var failingReasons []string
				for _, result := range results {
					if !result.done {
						failingReasons = append(failingReasons, result.reason)
					}
				}
				if len(failingReasons) > 0 {
					reasons[key] = predicateReasons{
						done:    false,
						reasons: failingReasons,
					}
				}
			}
			printCollectionStatus[T](t, start, done, reasons)
		}

		var invalidObjects []T
		for _, object := range objects {
			if !summarizePredicteResults(finalResults[types.NamespacedName{Namespace: object.GetNamespace(), Name: object.GetName()}]) {
				invalidObjects = append(invalidObjects, object)
			}
		}
		if opts.dumpConditions {
			for _, object := range invalidObjects {
				if !(reflect.ValueOf(object).IsZero() || reflect.ValueOf(object).Elem().IsZero()) { // can't use != nil here
					conditions, conditionsErr := Conditions(object)
					if conditionsErr != nil {
						t.Errorf("failed to extract conditions from %T %s/%s: %v", object, object.GetNamespace(), object.GetName(), conditionsErr)
						continue
					}
					t.Logf("%T %s/%s conditions:", object, object.GetNamespace(), object.GetName())
					for _, condition := range conditions {
						matches := len(opts.filterConditionDump) == 0
						for _, matcher := range opts.filterConditionDump {
							matches = matches || matcher.Matches(condition)
						}
						if matches {
							t.Logf("Condition: %s", condition)
						}
					}
				}
			}
		}
		t.FailNow()
	}
	t.Logf("Successfully waited for %s in %s", objective, duration)
}

func evaluateCollectionPredicates[T client.Object](objects []T, groupPredicates []Predicate[[]T], predicates []Predicate[T]) (map[types.NamespacedName][]predicateResult, error) {
	currentResults := map[types.NamespacedName][]predicateResult{}
	groupResults, err := evaluatePredicates(objects, groupPredicates)
	if err != nil {
		return nil, err
	}
	currentResults[types.NamespacedName{ /* empty sentinel */ }] = groupResults

	for _, object := range objects {
		objectResults, objectError := evaluatePredicates(object, predicates)
		if objectError != nil {
			return nil, objectError
		}
		currentResults[types.NamespacedName{Namespace: object.GetNamespace(), Name: object.GetName()}] = objectResults
	}
	return currentResults, nil
}

func summarizeCollectionPredicateResults(results map[types.NamespacedName][]predicateResult) bool {
	done := true
	for _, result := range results {
		done = done && summarizePredicteResults(result)
	}
	return done
}

type predicateReasons struct {
	done    bool
	reasons []string
}

func printCollectionStatus[T client.Object](t *testing.T, lastTimestamp time.Time, done bool, reasons map[types.NamespacedName]predicateReasons) {
	prefix := ""
	if !done {
		prefix = "in"
	}
	t.Logf("observed %svalid %T state after %s", prefix, new(T), time.Since(lastTimestamp).Round(25*time.Millisecond))
	for key, result := range reasons {
		if len(result.reasons) == 0 {
			continue
		}
		prefix := ""
		if !result.done {
			prefix = "in"
		}
		identifier := "collection"
		if diff := cmp.Diff(key, types.NamespacedName{}); diff != "" {
			identifier = fmt.Sprintf("%s/%s", key.Namespace, key.Name)
		}
		suffix := ""
		if len(result.reasons) == 1 {
			suffix = " " + result.reasons[0]
		}
		t.Logf(" - observed %T %s %svalid:%s", new(T), identifier, prefix, suffix)
		if len(result.reasons) > 1 {
			for _, message := range result.reasons {
				t.Log("    - " + message)
			}
		}
	}
}

// Predicate evaluates an object. Return whether the object in question matches your predicate, the reasons
// why or why not, and whether an error occurred. If determining that an object does not match a predicate,
// a message is required. Returning an error is fatal to the asynchronous assertion using this predicate.
type Predicate[T any] func(T) (done bool, reasons string, err error)

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
	if reflect.ValueOf(item).IsZero() || reflect.ValueOf(item).Elem().IsZero() {
		panic(fmt.Sprintf("programmer error: got a nil value under client.Object: %#v", item))
	}
	switch obj := item.(type) {
	case *corev1.Node:
		conditions := make([]Condition, len(obj.Status.Conditions))
		for i := range obj.Status.Conditions {
			conditions[i] = Condition{
				Type:    string(obj.Status.Conditions[i].Type),
				Status:  metav1.ConditionStatus(obj.Status.Conditions[i].Status),
				Reason:  obj.Status.Conditions[i].Reason,
				Message: obj.Status.Conditions[i].Message,
			}
		}
		return conditions, nil
	case *corev1.Pod:
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
		for i := range obj.Status.Conditions {
			conditions[i] = Condition{
				Type:    obj.Status.Conditions[i].Type,
				Status:  metav1.ConditionStatus(obj.Status.Conditions[i].Status),
				Reason:  obj.Status.Conditions[i].Reason,
				Message: obj.Status.Conditions[i].Message,
			}
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
		for i := range obj.Status.Conditions {
			conditions[i] = Condition{
				Type:    string(obj.Status.Conditions[i].Type),
				Status:  metav1.ConditionStatus(obj.Status.Conditions[i].Status),
				Reason:  obj.Status.Conditions[i].Reason,
				Message: obj.Status.Conditions[i].Message,
			}
		}
		return conditions, nil
	default:
		return nil, fmt.Errorf("object %T unknown", item)
	}
}

func adaptConditions(in []metav1.Condition) []Condition {
	conditions := make([]Condition, len(in))
	for i := range in {
		conditions[i] = Condition{
			Type:    in[i].Type,
			Status:  in[i].Status,
			Reason:  in[i].Reason,
			Message: in[i].Message,
		}
	}
	return conditions
}

func (needle Condition) Matches(condition Condition) bool {
	return (needle.Type == "" || needle.Type == condition.Type) &&
		(needle.Status == "" || needle.Status == condition.Status) &&
		(needle.Reason == "" || needle.Reason == condition.Reason) &&
		(needle.Message == "" || needle.Message == condition.Message)
}

// ConditionPredicate returns a predicate that validates that a particular condition type exists and has the requisite status, reason and/or message.
func ConditionPredicate[T client.Object](needle Condition) Predicate[T] {
	return func(item T) (bool, string, error) {
		haystack, err := Conditions(item)
		if err != nil {
			return false, "", err
		}
		for _, condition := range haystack {
			if needle.Type == condition.Type {
				valid := needle.Matches(condition)
				prefix := ""
				if !valid {
					prefix = "in"
				}
				return valid, fmt.Sprintf("%scorrect condition: wanted %s, got %s", prefix, needle.String(), condition.String()), nil
			}
		}

		return false, fmt.Sprintf("missing condition: wanted %s, did not find condition of this type", needle.String()), nil
	}
}
