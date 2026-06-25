package nodepool

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"

	"github.com/blang/semver"
)

// getRHELStream returns the effective RHEL stream for the NodePool.
// It delegates to GetRHELStream, which validates stream/version
// combinations and handles runc constraints.
// When spec.osImageStream.Name is unset, GetRHELStream returns the
// version-derived default (StreamRHEL9 for < 5.0, StreamRHEL10 for >= 5.0).
func getRHELStream(nodePool *hyperv1.NodePool, releaseImage *releaseinfo.ReleaseImage) (string, error) {
	version, err := semver.Parse(releaseImage.Version())
	if err != nil {
		return "", fmt.Errorf("failed to parse release image version %q: %w", releaseImage.Version(), err)
	}

	// TODO(CNTRLPLANE-3553): pass actual usesRunc once container runtime detection is wired in.
	return GetRHELStream(nodePool.Spec.OSImageStream.Name, version, false)
}

// validateOSImageStream checks that spec.osImageStream.Name, if set, is a
// valid stream for the given release version. Returns an error describing the
// problem or nil. It delegates to GetRHELStream for version-aware validation.
func validateOSImageStream(nodePool *hyperv1.NodePool, releaseImage *releaseinfo.ReleaseImage) error {
	name := nodePool.Spec.OSImageStream.Name
	if name == "" {
		return nil
	}
	version, err := semver.Parse(releaseImage.Version())
	if err != nil {
		return fmt.Errorf("failed to parse release image version %q: %w", releaseImage.Version(), err)
	}

	// TODO(CNTRLPLANE-3553): pass actual usesRunc once container runtime detection is wired in.
	_, err = GetRHELStream(name, version, false)
	return err
}
