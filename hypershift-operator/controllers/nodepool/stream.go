package nodepool

import (
	"fmt"

	"github.com/openshift/hypershift/support/releaseinfo"

	"github.com/blang/semver"
)

const (
	StreamRHEL9  = releaseinfo.StreamRHEL9
	StreamRHEL10 = releaseinfo.StreamRHEL10
)

// GetRHELStream resolves which RHEL CoreOS stream a NodePool should use.
// Returns the resolved stream name, or an error for invalid combinations.
// For OCP < 5.0 with no explicit stream it returns StreamRHEL9 (the only
// stream available on those releases).
// Exported for use by integration tests and future Phase 2 consumers
// (token secret plumbing, validMachineConfigCondition).
func GetRHELStream(explicitStream string, releaseVersion semver.Version, usesRunc bool) (string, error) {
	isOCP5Plus := releaseVersion.Major >= 5

	if explicitStream != "" {
		switch explicitStream {
		case StreamRHEL9:
			return StreamRHEL9, nil
		case StreamRHEL10:
			if !isOCP5Plus {
				return "", fmt.Errorf("stream %q requires OCP >= 5.0, but release version is %s", explicitStream, releaseVersion)
			}
			if usesRunc {
				return "", fmt.Errorf("stream %q is incompatible with runc: RHEL 10 does not ship runc", explicitStream)
			}
			return StreamRHEL10, nil
		default:
			return "", fmt.Errorf("unknown RHEL stream %q; valid values are %q and %q", explicitStream, StreamRHEL9, StreamRHEL10)
		}
	}

	if !isOCP5Plus {
		return StreamRHEL9, nil
	}

	if usesRunc {
		return StreamRHEL9, nil
	}

	return StreamRHEL10, nil
}
