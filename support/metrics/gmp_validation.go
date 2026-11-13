package metrics

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const (
	// gmpKeepNothingRegex keeps no sample. A rule that drops every metric cannot
	// be written as a catch-all drop, because GMP rejects any relabeling regex
	// that matches a protected label name; keeping a value no sample can have
	// instead filters everything out without that regex.
	gmpKeepNothingRegex = "^$"

	// gmpAlwaysPresentLabel is a label every series carries with a non-empty
	// value. Pinning the rewritten keep rule to it guarantees ^$ never matches,
	// which is what makes "keep nothing" equivalent to the original catch-all
	// drop even when that rule had no source labels or referenced a label some
	// series omit.
	gmpAlwaysPresentLabel prometheusoperatorv1.LabelName = "__name__"
)

// admissionProtectedLabels are the label names GMP's protected-labels
// ValidatingAdmissionPolicy checks a metric relabeling regex against. The policy
// only inspects a rule that sets regex explicitly; a rule that omits it is not
// checked. job is deliberately absent here: the admission policy does not
// protect it, so a keep or drop filter that mentions job is valid.
var admissionProtectedLabels = map[string]struct{}{
	"project_id":                {},
	"location":                  {},
	"cluster":                   {},
	"namespace":                 {},
	"instance":                  {},
	"top_level_controller":      {},
	"top_level_controller_type": {},
	"__address__":               {},
}

// collectorProtectedLabels are the label names the GMP collector refuses to
// drop. It inspects only labeldrop (whose regex must not match any of these) and
// labelkeep (whose regex must match all of them); keep and drop are not
// regex-checked. Its set includes job but not the top_level_controller pair.
var collectorProtectedLabels = map[string]struct{}{
	"project_id":  {},
	"location":    {},
	"cluster":     {},
	"namespace":   {},
	"job":         {},
	"instance":    {},
	"__address__": {},
}

// targetLabelProtectedLabels are the labels GMP refuses to write onto. This is
// the union of the admission and collector sets, matching the PodMonitoring
// CRD's targetLabel validation. It is spelled out rather than computed so a
// change to either set is a deliberate edit here too; a test pins the union.
var targetLabelProtectedLabels = map[string]struct{}{
	"project_id":                {},
	"location":                  {},
	"cluster":                   {},
	"namespace":                 {},
	"job":                       {},
	"instance":                  {},
	"top_level_controller":      {},
	"top_level_controller_type": {},
	"__address__":               {},
}

// SanitizeGMPRelabelConfigs adapts CoreOS metric relabel configs to the rules
// GMP accepts.
//
// GMP enforces its protected labels in two places with different sets. Admission
// rejects a rule whose explicit regex matches an admission-protected label name,
// whatever the rule's action. The collector separately rejects a labeldrop whose
// regex matches a collector-protected label and a labelkeep whose regex does not
// match all of them. A rule that omits regex entirely is not inspected by either
// check.
//
// CoreOS uses a catch-all drop to silence a component entirely. Dropping
// everything becomes keeping nothing, with the source label pinned to one that is
// always present so the rewrite is equivalent for every series. Keeping
// everything becomes no rule at all. A narrower regex that still matches a
// protected label has no equivalent and is an error.
func SanitizeGMPRelabelConfigs(source []prometheusoperatorv1.RelabelConfig) ([]prometheusoperatorv1.RelabelConfig, error) {
	if len(source) == 0 {
		return nil, nil
	}

	result := make([]prometheusoperatorv1.RelabelConfig, 0, len(source))
	for i, rule := range source {
		// A rule that is invalid for reasons unrelated to its regex is left for
		// the converter to reject with its own message, so a structural error is
		// not reported as a protected-label error.
		if hasStructuralRelabelProblem(rule) {
			result = append(result, rule)
			continue
		}

		if rule.TargetLabel != "" {
			if _, protected := targetLabelProtectedLabels[rule.TargetLabel]; protected {
				return nil, fmt.Errorf("metric relabeling %d: target label %q is protected by GMP", i, rule.TargetLabel)
			}
		}

		action := strings.ToLower(rule.Action)
		if action == "labelkeep" {
			if rule.Regex == "" {
				// An omitted regex defaults to (.*), which matches every protected
				// label the collector requires a labelkeep regex to match, so the
				// rule keeps every label and has no effect.
				continue
			}
			// With an explicit regex, the collector requires it to match every
			// protected label while admission rejects a regex that matches any of
			// them. The two sets overlap, so no rule can satisfy both.
			return nil, fmt.Errorf("metric relabeling %d: GMP cannot represent a labelkeep rule with an explicit regex", i)
		}

		// A rule without a regex is admitted unchanged: admission skips it, and
		// the collector only inspects labeldrop and labelkeep regexes. labeldrop
		// is required to set one by the structural check above.
		if rule.Regex == "" {
			result = append(result, rule)
			continue
		}

		matched, err := relabelRegexMatchesProtectedLabels(rule.Regex, admissionProtectedLabels)
		if err != nil {
			return nil, fmt.Errorf("metric relabeling %d: %w", i, err)
		}
		if len(matched) == 0 {
			if action == "labeldrop" {
				matched, err = relabelRegexMatchesProtectedLabels(rule.Regex, collectorProtectedLabels)
				if err != nil {
					return nil, fmt.Errorf("metric relabeling %d: %w", i, err)
				}
				if len(matched) > 0 {
					return nil, fmt.Errorf("metric relabeling %d: regex %q matches protected label %q, which the GMP collector refuses to drop", i, rule.Regex, matched[0])
				}
			}
			result = append(result, rule)
			continue
		}

		if !isCatchAllRelabelRegex(rule.Regex) {
			return nil, fmt.Errorf("metric relabeling %d: regex %q matches protected label %q, which GMP rejects", i, rule.Regex, matched[0])
		}

		switch action {
		case "keep":
			// Keeping every sample is the absence of a filter.
			continue
		case "drop":
			sanitized := *rule.DeepCopy()
			sanitized.Action = "keep"
			sanitized.Regex = gmpKeepNothingRegex
			sanitized.SourceLabels = []prometheusoperatorv1.LabelName{gmpAlwaysPresentLabel}
			result = append(result, sanitized)
		default:
			return nil, fmt.Errorf("metric relabeling %d: action %q with a catch-all regex is not representable in GMP", i, rule.Action)
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

// hasStructuralRelabelProblem reports whether a rule is invalid independently of
// its regex. The converter rejects such a rule with a specific message;
// sanitizing it first would replace that message with a misleading one. A
// labeldrop must carry a regex (the CRD's CEL enforces it); a labelkeep without
// one is valid and is handled as a no-op above.
func hasStructuralRelabelProblem(rule prometheusoperatorv1.RelabelConfig) bool {
	switch strings.ToLower(rule.Action) {
	case "", "replace", "lowercase", "uppercase", "keep", "drop", "keepequal", "dropequal", "hashmod", "labeldrop", "labelkeep":
	default:
		return true
	}
	if rule.Separator != nil && *rule.Separator == "" {
		return true
	}
	if rule.Replacement != nil && *rule.Replacement == "" {
		return true
	}
	if strings.ToLower(rule.Action) == "labeldrop" && rule.Regex == "" {
		return true
	}
	return false
}

// effectiveRelabelRegex returns the regex a rule matches: Prometheus reads an
// omitted regex as a catch-all.
func effectiveRelabelRegex(regex string) string {
	if regex == "" {
		return "(.*)"
	}
	return regex
}

// catchAllRelabelRegexes are the exact spellings that match every value a
// relabeling can see. The check is textual on purpose: probing a handful of
// sample strings cannot prove universality, and treating a merely broad regex as
// a catch-all would silently rewrite a narrow filter into one that drops every
// metric.
var catchAllRelabelRegexes = map[string]struct{}{
	"":        {},
	".*":      {},
	"(.*)":    {},
	"^.*$":    {},
	"^(.*)$":  {},
	"(?s).*":  {},
	"(?s:.*)": {},
}

// isCatchAllRelabelRegex reports whether the regex matches every string, which is
// what makes the keep/drop rewrite in SanitizeGMPRelabelConfigs sound. Only the
// exact spellings above are recognized; anything else is treated as narrow, so an
// unrepresentable rule fails generation rather than being broadened silently.
func isCatchAllRelabelRegex(regex string) bool {
	_, ok := catchAllRelabelRegexes[regex]
	return ok
}

// relabelRegexMatchesProtectedLabels returns the sorted protected label names
// the rule's regex matches.
//
// It applies the regex the way GMP's admission policy does: CEL's matches() is
// an unanchored RE2 search, so a regex that matches only part of a protected
// label name is rejected too, even though the relabel config itself would only
// match whole values.
func relabelRegexMatchesProtectedLabels(regex string, protected map[string]struct{}) ([]string, error) {
	compiled, err := regexp.Compile(effectiveRelabelRegex(regex))
	if err != nil {
		return nil, fmt.Errorf("invalid regex %q: %w", effectiveRelabelRegex(regex), err)
	}
	var matched []string
	for name := range protected {
		if compiled.MatchString(name) {
			matched = append(matched, name)
		}
	}
	sort.Strings(matched)
	return matched, nil
}
