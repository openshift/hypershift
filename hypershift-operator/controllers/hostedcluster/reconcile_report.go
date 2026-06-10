package hostedcluster

import (
	"fmt"
	"strings"
	"time"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

// operationCategory classifies a reconcile operation for error handling and blocking.
type operationCategory int

const (
	// critical operations block all downstream component operations when they fail.
	critical operationCategory = iota
	// nonCritical operations collect errors but never block other work.
	nonCritical
)

// operationResult records the outcome of a single reconcile operation.
type operationResult struct {
	name     string
	category operationCategory
	err      error
	blocked  bool
}

// reconcileReport collects results from all reconcile operations and produces
// the final error aggregate, condition messages, and requeue signals.
type reconcileReport struct {
	results      []operationResult
	requeueAfter *time.Duration
	legacy       bool
}

// execute runs fn and records its result.
// In legacy mode, any prior failure blocks execution.
func (r *reconcileReport) execute(name string, cat operationCategory, fn func() error) {
	if r.legacy && r.hasAnyFailure() {
		r.results = append(r.results, operationResult{name: name, category: cat, blocked: true})
		return
	}
	r.results = append(r.results, operationResult{name: name, category: cat, err: fn()})
}

// executeOrBlock runs fn and records its result as nonCritical, or records
// a blocked entry if a prior operation should block downstream work.
// In fail-fast mode, any prior failure blocks; in error-collecting mode,
// only critical failures block.
func (r *reconcileReport) executeOrBlock(name string, fn func() error) {
	if r.shouldBlock() {
		r.results = append(r.results, operationResult{name: name, category: nonCritical, blocked: true})
		return
	}
	r.results = append(r.results, operationResult{name: name, category: nonCritical, err: fn()})
}

// hasCriticalFailure returns true if any critical operation has actually failed (not blocked).
func (r *reconcileReport) hasCriticalFailure() bool {
	for _, res := range r.results {
		if res.category == critical && res.err != nil && !res.blocked {
			return true
		}
	}
	return false
}

// hasAnyFailure returns true if any operation has actually failed (not blocked).
func (r *reconcileReport) hasAnyFailure() bool {
	for _, res := range r.results {
		if res.err != nil && !res.blocked {
			return true
		}
	}
	return false
}

// shouldBlock returns true if subsequent dependent operations should be skipped.
// In legacy mode, any failure blocks. Otherwise only critical failures block.
func (r *reconcileReport) shouldBlock() bool {
	if r.legacy {
		return r.hasAnyFailure()
	}
	return r.hasCriticalFailure()
}

// criticalFailureNames returns the deduplicated names of critical operations that failed.
func (r *reconcileReport) criticalFailureNames() []string {
	var names []string
	seen := make(map[string]bool)
	for _, res := range r.results {
		if res.category == critical && res.err != nil && !res.blocked && !seen[res.name] {
			names = append(names, res.name)
			seen[res.name] = true
		}
	}
	return names
}

// blockedNames returns the deduplicated names of operations that were blocked.
func (r *reconcileReport) blockedNames() []string {
	var names []string
	seen := make(map[string]bool)
	for _, res := range r.results {
		if res.blocked && !seen[res.name] {
			names = append(names, res.name)
			seen[res.name] = true
		}
	}
	return names
}

// allErrors returns all non-nil errors from operations that actually ran.
// Blocked operations are excluded — they carry no diagnostic value beyond
// what conditionMessage() already reports.
func (r *reconcileReport) allErrors() []error {
	var errs []error
	for _, res := range r.results {
		if res.err != nil && !res.blocked {
			errs = append(errs, res.err)
		}
	}
	return errs
}

// aggregate returns the final error for the reconcile return value.
// When critical failures exist, only critical errors are returned with a
// summary of blocked operations — non-critical errors are suppressed since
// the user should fix the critical issue first.
// When no critical failures exist, all errors are returned as-is.
func (r *reconcileReport) aggregate() error {
	if !r.hasCriticalFailure() {
		return utilerrors.NewAggregate(r.allErrors())
	}
	critErr := utilerrors.NewAggregate(r.criticalErrors())
	if blocked := r.blockedNames(); len(blocked) > 0 {
		return fmt.Errorf("critical error: %w; blocked operations: [%s]",
			critErr, strings.Join(blocked, ", "))
	}
	return fmt.Errorf("critical error: %w", critErr)
}

// criticalErrors returns errors only from critical operations that actually failed.
func (r *reconcileReport) criticalErrors() []error {
	var errs []error
	for _, res := range r.results {
		if res.category == critical && res.err != nil && !res.blocked {
			errs = append(errs, res.err)
		}
	}
	return errs
}

// conditionMessage builds a short summary for structured logging.
func (r *reconcileReport) conditionMessage() string {
	criticalNames := r.criticalFailureNames()
	if len(criticalNames) == 0 {
		return ""
	}
	blockedOps := r.blockedNames()
	if len(blockedOps) == 0 {
		return fmt.Sprintf("critical failures: [%s]",
			strings.Join(criticalNames, ", "))
	}
	return fmt.Sprintf("critical failures: [%s]; blocked operations: [%s]",
		strings.Join(criticalNames, ", "),
		strings.Join(blockedOps, ", "))
}
