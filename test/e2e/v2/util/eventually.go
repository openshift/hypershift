//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"time"

	. "github.com/onsi/ginkgo/v2"

	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/google/go-cmp/cmp"
)

func defaultEventuallyOptions() *EventuallyOptions {
	return &EventuallyOptions{
		interval:       3 * time.Second,
		timeout:        10 * time.Minute,
		immediate:      true,
		dumpConditions: true,
	}
}

// EventuallyOptions configure v2 asynchronous polling behavior.
type EventuallyOptions struct {
	interval  time.Duration
	timeout   time.Duration
	immediate bool

	dumpConditions      bool
	filterConditionDump []e2eutil.Condition
}

// EventuallyOption configures v2 asynchronous polling behavior.
type EventuallyOption func(*EventuallyOptions)

// WithInterval sets the polling interval for a v2 asynchronous assertion.
func WithInterval(interval time.Duration) EventuallyOption {
	return func(o *EventuallyOptions) {
		o.interval = interval
	}
}

// WithTimeout sets the polling timeout for a v2 asynchronous assertion.
func WithTimeout(timeout time.Duration) EventuallyOption {
	return func(o *EventuallyOptions) {
		o.timeout = timeout
	}
}

// WithDelayedStart configures a v2 asynchronous assertion to wait for the
// first interval before its first poll.
func WithDelayedStart() EventuallyOption {
	return func(o *EventuallyOptions) {
		o.immediate = false
	}
}

// WithoutConditionDump disables condition logging when a v2 asynchronous
// assertion fails.
func WithoutConditionDump() EventuallyOption {
	return func(o *EventuallyOptions) {
		o.dumpConditions = false
	}
}

// WithFilteredConditionDump limits failure condition logging to matching
// conditions.
func WithFilteredConditionDump(matchers ...e2eutil.Condition) EventuallyOption {
	return func(o *EventuallyOptions) {
		o.filterConditionDump = append(o.filterConditionDump, matchers...)
	}
}

// EventuallyObject polls until all predicates are fulfilled for an object.
// It returns an error instead of signaling failure through testing.TB, so the
// caller can report the failure through the v2 Ginkgo assertion path.
func EventuallyObject[T client.Object](ctx context.Context, objective string, getter func(context.Context) (T, error), predicates []e2eutil.Predicate[T], options ...EventuallyOption) error {
	opts := defaultEventuallyOptions()
	for _, option := range options {
		option(opts)
	}

	if os.Getenv("EVENTUALLY_VERBOSE") != "false" {
		GinkgoWriter.Printf("Waiting for %s\n", objective)
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
				GinkgoWriter.Printf("Failed to get %T: %v\n", object, getErr)
			}
			return false, nil
		}
		object = obj

		currentResults, err := evaluatePredicates(object, predicates)
		if err != nil {
			return false, err
		}
		done := summarizePredicateResults(currentResults)
		if os.Getenv("EVENTUALLY_VERBOSE") != "false" {
			printStatus(lastTimestamp, object, done, diffPredicateResults(previousResults, currentResults))
		}

		previousResults = currentResults
		lastTimestamp = time.Now()
		return done, nil
	})
	duration := time.Since(start).Round(25 * time.Millisecond)

	if err != nil {
		failure := fmt.Errorf("failed to wait for %s in %s: %w", objective, duration, err)
		if !isZeroObject(object) {
			results, resultsErr := evaluatePredicates(object, predicates)
			if resultsErr != nil {
				return fmt.Errorf("%w; failed to evaluate predicates: %w", failure, resultsErr)
			}
			done := summarizePredicateResults(results)
			if !done {
				var reasons []string
				for _, result := range results {
					if !result.done {
						reasons = append(reasons, result.reason)
					}
				}
				printStatus(start, object, done, reasons)
			}

			if opts.dumpConditions {
				if err := logConditions(object, opts.filterConditionDump); err != nil {
					return fmt.Errorf("%w; %w", failure, err)
				}
			}
		}
		return failure
	}

	GinkgoWriter.Printf("Successfully waited for %s in %s\n", objective, duration)
	return nil
}

// EventuallyObjects polls until the group predicates and all per-object
// predicates are fulfilled for a collection of objects. It returns an error
// instead of signaling failure through testing.TB.
func EventuallyObjects[T client.Object](ctx context.Context, objective string, getter func(context.Context) ([]T, error), groupPredicates []e2eutil.Predicate[[]T], predicates []e2eutil.Predicate[T], options ...EventuallyOption) error {
	opts := defaultEventuallyOptions()
	for _, option := range options {
		option(opts)
	}

	if os.Getenv("EVENTUALLY_VERBOSE") != "false" {
		GinkgoWriter.Printf("Waiting for %s\n", objective)
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
				GinkgoWriter.Printf("Failed to get %T: %v\n", new(T), getErr)
			}
			return false, nil
		}
		objects = objs

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
						done:    summarizePredicateResults(results),
						reasons: diff,
					}
				}
			}
			printCollectionStatus[T](lastTimestamp, done, reasons)
		}
		previousResults = currentResults

		lastTimestamp = time.Now()
		return done, nil
	})
	duration := time.Since(start).Round(25 * time.Millisecond)

	if err != nil {
		failure := fmt.Errorf("failed to wait for %s in %s: %w", objective, duration, err)
		finalResults, resultErr := evaluateCollectionPredicates(objects, groupPredicates, predicates)
		if resultErr != nil {
			return fmt.Errorf("%w; failed to evaluate predicates: %w", failure, resultErr)
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
			printCollectionStatus[T](start, done, reasons)
		}

		var invalidObjects []T
		for _, object := range objects {
			if !summarizePredicateResults(finalResults[types.NamespacedName{Namespace: object.GetNamespace(), Name: object.GetName()}]) {
				invalidObjects = append(invalidObjects, object)
			}
		}
		if opts.dumpConditions {
			for _, object := range invalidObjects {
				if isZeroObject(object) {
					continue
				}
				if err := logConditions(object, opts.filterConditionDump); err != nil {
					return fmt.Errorf("%w; %w", failure, err)
				}
			}
		}
		return failure
	}

	GinkgoWriter.Printf("Successfully waited for %s in %s\n", objective, duration)
	return nil
}

// EventuallyNotFound polls until the object is not found (deleted). It returns
// an error instead of signaling failure through testing.TB.
func EventuallyNotFound[T client.Object](ctx context.Context, c client.Client, obj T, options ...EventuallyOption) error {
	opts := defaultEventuallyOptions()
	for _, option := range options {
		option(opts)
	}

	objective := fmt.Sprintf("%T %s/%s to be deleted", obj, obj.GetNamespace(), obj.GetName())
	if os.Getenv("EVENTUALLY_VERBOSE") != "false" {
		GinkgoWriter.Printf("Waiting for %s\n", objective)
	}
	start := time.Now()
	err := wait.PollUntilContextTimeout(ctx, opts.interval, opts.timeout, opts.immediate, func(ctx context.Context) (bool, error) {
		err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj)
		if err != nil {
			if client.IgnoreNotFound(err) == nil {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
	duration := time.Since(start).Round(25 * time.Millisecond)

	if err != nil {
		return fmt.Errorf("failed to wait for %s in %s: %w", objective, duration, err)
	}

	GinkgoWriter.Printf("Successfully waited for %s in %s\n", objective, duration)
	return nil
}

type predicateResult struct {
	done   bool
	reason string
}

func evaluatePredicates[T any](object T, predicates []e2eutil.Predicate[T]) ([]predicateResult, error) {
	if reflect.TypeOf(object).Kind() != reflect.Slice && (reflect.ValueOf(object).IsZero() || reflect.ValueOf(object).Elem().IsZero()) {
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

func summarizePredicateResults(results []predicateResult) bool {
	done := true
	for i := range results {
		done = done && results[i].done
	}
	return done
}

func printStatus[T client.Object](lastTimestamp time.Time, object T, done bool, reasons []string) {
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
	GinkgoWriter.Printf("observed %T %s/%s %svalid at RV %s after %s:%s\n", object, object.GetNamespace(), object.GetName(), prefix, object.GetResourceVersion(), time.Since(lastTimestamp).Round(25*time.Millisecond), suffix)
	if len(reasons) > 1 {
		for _, message := range reasons {
			GinkgoWriter.Printf(" - %s\n", message)
		}
	}
}

func evaluateCollectionPredicates[T client.Object](objects []T, groupPredicates []e2eutil.Predicate[[]T], predicates []e2eutil.Predicate[T]) (map[types.NamespacedName][]predicateResult, error) {
	currentResults := map[types.NamespacedName][]predicateResult{}
	groupResults, err := evaluatePredicates(objects, groupPredicates)
	if err != nil {
		return nil, err
	}
	currentResults[types.NamespacedName{}] = groupResults

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
		done = done && summarizePredicateResults(result)
	}
	return done
}

type predicateReasons struct {
	done    bool
	reasons []string
}

func printCollectionStatus[T client.Object](lastTimestamp time.Time, done bool, reasons map[types.NamespacedName]predicateReasons) {
	prefix := ""
	if !done {
		prefix = "in"
	}
	GinkgoWriter.Printf("observed %svalid %T state after %s\n", prefix, new(T), time.Since(lastTimestamp).Round(25*time.Millisecond))
	for key, result := range reasons {
		if len(result.reasons) == 0 {
			continue
		}
		prefix := ""
		if !result.done {
			prefix = "in"
		}
		identifier := "collection"
		if key != (types.NamespacedName{}) {
			identifier = fmt.Sprintf("%s/%s", key.Namespace, key.Name)
		}
		suffix := ""
		if len(result.reasons) == 1 {
			suffix = " " + result.reasons[0]
		}
		GinkgoWriter.Printf(" - observed %T %s %svalid:%s\n", new(T), identifier, prefix, suffix)
		if len(result.reasons) > 1 {
			for _, message := range result.reasons {
				GinkgoWriter.Printf("    - %s\n", message)
			}
		}
	}
}

func logConditions(object client.Object, filters []e2eutil.Condition) error {
	conditions, err := e2eutil.Conditions(object)
	if err != nil {
		return fmt.Errorf("failed to extract conditions from %T %s/%s: %w", object, object.GetNamespace(), object.GetName(), err)
	}
	GinkgoWriter.Printf("%T %s/%s conditions:\n", object, object.GetNamespace(), object.GetName())
	for _, condition := range conditions {
		matches := len(filters) == 0
		for _, matcher := range filters {
			matches = matches || matcher.Matches(condition)
		}
		if matches {
			GinkgoWriter.Printf("%s\n", condition.String())
		}
	}
	return nil
}

func isZeroObject[T client.Object](object T) bool {
	value := reflect.ValueOf(object)
	return value.IsZero() || value.Elem().IsZero()
}
