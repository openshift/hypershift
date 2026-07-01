package nodepool

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"

	"github.com/blang/semver"
)

// getRHELStreamForBootImage returns the RHEL stream name to pass to
// StreamForName when resolving platform-specific boot images (AMIs, VHDs,
// GCE images, etc.).
//
// It always delegates to GetRHELStream for version-aware default
// resolution, validation, and runc constraint checking. When
// spec.osImageStream.Name is unset, GetRHELStream derives the default
// from the release version: rhel-9 for OCP < 5.0, rhel-10 for
// OCP >= 5.0. This matches the dual-stream RHEL NodePool enhancement:
// https://github.com/openshift/enhancements/blob/master/enhancements/hypershift/dual-stream-rhel-nodepool.md
//
// On upgrade to OCP 5.0+, existing NodePools with unset
// spec.osImageStream will transition from rhel-9 to rhel-10 boot
// images. This is the intended behavior per the enhancement:
// implicit-stream NodePools automatically adopt the new default.
func getRHELStreamForBootImage(nodePool *hyperv1.NodePool, releaseImage *releaseinfo.ReleaseImage) (string, error) {
	version, err := semver.Parse(releaseImage.Version())
	if err != nil {
		return "", fmt.Errorf("failed to parse release image version %q: %w", releaseImage.Version(), err)
	}

	// TODO(CNTRLPLANE-3553): pass actual usesRunc once container runtime detection is wired in.
	return GetRHELStream(nodePool.Spec.OSImageStream.Name, version, false)
}

// validateOSImageStream checks that spec.osImageStream.Name, if set, is a
// valid stream for the given release version. Returns an error describing the
// problem or nil. It delegates to getRHELStreamForBootImage for version-aware
// validation.
func validateOSImageStream(nodePool *hyperv1.NodePool, releaseImage *releaseinfo.ReleaseImage) error {
	if nodePool.Spec.OSImageStream.Name == "" {
		return nil
	}
	_, err := getRHELStreamForBootImage(nodePool, releaseImage)
	return err
}
