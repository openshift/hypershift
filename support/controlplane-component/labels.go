package controlplanecomponent

import (
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

var reservedControlPlaneLabelKeys = map[string]struct{}{
	"app":               {},
	"control-plane":     {},
	"name":              {},
	"k8s-app":           {},
	"olm.catalogSource": {},
	"infrastructure.openshift.io/cloud-controller-manager": {},
	"hypershift.openshift.io/need-management-kas-access":   {},
	"hypershift.openshift.io/need-metrics-server-access":   {},
	"hypershift.openshift.io/control-plane-component":      {},
	"hypershift.openshift.io/request-serving-component":    {},
	"hypershift.openshift.io/hosted-control-plane":         {},
	"hypershift.openshift.io/managed-by":                   {},
}

func isReservedLabelKey(key string) bool {
	_, reserved := reservedControlPlaneLabelKeys[key]
	return reserved
}

// FilterUserLabels removes labels that cannot be safely propagated to control-plane workloads.
// Reserved keys are operator-owned, and invalid keys or values would prevent the target Pod from being created.
func FilterUserLabels(labels map[string]string) (map[string]string, []string) {
	if labels == nil {
		return nil, nil
	}

	filtered := make(map[string]string, len(labels))
	var warnings []string
	for key, value := range labels {
		switch {
		case isReservedLabelKey(key):
			warnings = append(warnings, fmt.Sprintf("label %q is reserved for HyperShift control-plane use", key))
		case len(validation.IsQualifiedName(key)) > 0:
			warnings = append(warnings, fmt.Sprintf("label %q has an invalid key: %s", key, strings.Join(validation.IsQualifiedName(key), ", ")))
		case len(validation.IsValidLabelValue(value)) > 0:
			warnings = append(warnings, fmt.Sprintf("label %q has an invalid value: %s", key, strings.Join(validation.IsValidLabelValue(value), ", ")))
		default:
			filtered[key] = value
		}
	}
	sort.Strings(warnings)
	return filtered, warnings
}
