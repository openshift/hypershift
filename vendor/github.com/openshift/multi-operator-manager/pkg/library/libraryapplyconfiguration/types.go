package libraryapplyconfiguration

import (
	"slices"
	"strings"
)

type ApplyConfigurationRunResult struct {
	ControllerResults []ControllerRunResult `json:"controllerResults"`
}

type ControllerRunResult struct {
	ControllerName string              `json:"controllerName"`
	Status         ControllerRunStatus `json:"status"`
	Errors         []ErrorDetails      `json:"errors,omitempty"`
	PanicStack     string              `json:"panicStack,omitempty"`
}

type ControllerRunStatus string

var (
	ControllerRunStatusUnknown   ControllerRunStatus = "Unknown"
	ControllerRunStatusSucceeded ControllerRunStatus = "Succeeded"
	ControllerRunStatusSkipped   ControllerRunStatus = "Skipped"
	ControllerRunStatusFailed    ControllerRunStatus = "Failed"
	ControllerRunStatusPanicked  ControllerRunStatus = "Panicked"
)

// TODO perhaps we add indications about interfaces this matches?
type ErrorDetails struct {
	Message string `json:"message"`
}

func CanonicalizeApplyConfigurationRunResult(obj *ApplyConfigurationRunResult) {
	if obj == nil {
		return
	}
	slices.SortStableFunc(obj.ControllerResults, sortControllerRunResult)
}

// TODO sort with error details
func sortControllerRunResult(a, b ControllerRunResult) int {
	if c := strings.Compare(a.ControllerName, b.ControllerName); c != 0 {
		return c
	}
	if c := strings.Compare(string(a.Status), string(b.Status)); c != 0 {
		return c
	}
	if c := strings.Compare(a.PanicStack, b.PanicStack); c != 0 {
		return c
	}

	return 0
}
