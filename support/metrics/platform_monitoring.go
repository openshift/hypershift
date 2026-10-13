package metrics

import "fmt"

// PlatformMonitoring is used to indicate which metrics will be scraped by the
// management cluster's platform monitoring stack:
// - OperatorOnly indicates that only the hypershift operator will be scraped
// - All indicates that the hypershift operator and any control planes created by it will be scraped
// - None indicates that neither operator nor control planes will be scraped
type PlatformMonitoring string

var (
	PlatformMonitoringOperatorOnly PlatformMonitoring = "OperatorOnly"
	PlatformMonitoringAll          PlatformMonitoring = "All"
	PlatformMonitoringNone         PlatformMonitoring = "None"
)

func (o *PlatformMonitoring) String() string {
	if o == nil {
		return ""
	}
	return string(*o)
}

func (o *PlatformMonitoring) Set(value string) error {
	switch value {
	case string(PlatformMonitoringOperatorOnly):
		*o = PlatformMonitoringOperatorOnly
	case string(PlatformMonitoringAll):
		*o = PlatformMonitoringAll
	case string(PlatformMonitoringNone):
		*o = PlatformMonitoringNone
	default:
		return fmt.Errorf("invalid OCP monitoring option: %s, valid values are: %s, %s, %s", value, string(PlatformMonitoringOperatorOnly), string(PlatformMonitoringAll), string(PlatformMonitoringNone))
	}
	return nil
}

func (o *PlatformMonitoring) Type() string {
	return "PlatformMonitoringOption"
}

func (o *PlatformMonitoring) IsEnabled() bool {
	return o != nil && (*o != PlatformMonitoringNone)
}
