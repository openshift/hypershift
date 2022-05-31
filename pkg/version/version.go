package version

import (
	"fmt"
	"runtime/debug"
)

// GetRevision returns the overall codebase version. It's for detecting
// what code a binary was built from.
func GetRevision() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "<unknown>"
	}

	for _, setting := range bi.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value
		}
	}
	return "<unknown>"
}

func String() string {
	return fmt.Sprintf("openshift/hypershift %s", GetRevision())
}
