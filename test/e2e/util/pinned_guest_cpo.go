package util

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/openshift/hypershift/support/releaseinfo"

	"github.com/blang/semver"
)

// Pinned guest releases for the main-CPO-on-4.20 experiment. Always multi-arch
// manifest lists so the same constants are correct for amd64/arm64/multi-arch jobs.
//
// The release payload the CI job passes to e2e is used ONLY to resolve the control
// plane operator image (its embedded "hypershift" component is the CPO built from the
// PR under test). It is NEVER used as a guest release; guests are hard-pinned to the
// constants below.
//
// All versions are DERIVED from the base HostedCluster version (4.20.42). HyperShift
// enforces two invariants on NodePool vs HostedCluster version (ValidateVersionSkew):
//  1. NodePool version must be <= HostedCluster version.
//  2. HostedCluster.minor - NodePool.minor must be <= 3 (n-3 skew policy).
//
// So, with base HC = 4.20.42:
//   - Upgrade TARGET must be <= HC. We make it == HC (4.20.42); the upgrade CREATE-FROM
//     (Previous) is a lower z-stream (4.20.41) so the upgrade is a real 4.20.41 -> 4.20.42
//     hop that never exceeds the HC version.
//   - Prev-release N-1..N-3 are supported skew (minor diff 1,2,3): 4.19 / 4.18 / 4.17.
//   - Prev-release N-4 is UNSUPPORTED skew (minor diff 4): 4.16. The N4 test asserts
//     SupportedVersionSkew=False, so it must be exactly one minor beyond the n-3 window.
//
// Latest published z-stream chosen per minor so NodePools get real, bootable nodes.
const (
	// PinnedGuestReleaseImage is the BASE version every normally-created hosted cluster
	// is pinned to.
	PinnedGuestReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.20.42-multi"
	// PinnedGuestUpgradeReleaseImage is the upgrade TARGET (== base HC; cannot exceed it).
	PinnedGuestUpgradeReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.20.42-multi"
	// PinnedGuestPreviousReleaseImage is the upgrade CREATE-FROM: a lower z-stream of the
	// base minor, so upgrade tests perform a real 4.20.41 -> 4.20.42 z-stream upgrade.
	PinnedGuestPreviousReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.20.41-multi"
	// PinnedGuestN1ReleaseImage..N4 are the prev-minor NodePool releases. N1..N3 are
	// within the n-3 supported skew window; N4 is deliberately one minor beyond it
	// (unsupported) because TestNodePoolPrevReleaseN4 asserts SupportedVersionSkew=False.
	PinnedGuestN1ReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.19.52-multi"
	PinnedGuestN2ReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.18.58-multi"
	PinnedGuestN3ReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.17.59-multi"
	PinnedGuestN4ReleaseImage = "quay.io/openshift-release-dev/ocp-release:4.16.73-multi"
)

// ResolvedCPO carries the control-plane-operator image resolved from the CI-passed
// release payload, along with evidence of where it came from (the payload pullspec and
// version). The payload version is the key "this CPO is from main / a newer release than
// the pinned guest" evidence: the guest is pinned to 4.20.x while this version is
// whatever the PR built against.
type ResolvedCPO struct {
	// CPOImage is the "hypershift" component image pulled from the payload. This is the
	// CPO built from the PR under test.
	CPOImage string
	// PayloadPullSpec is the release image CI passed in that the CPO was resolved from.
	PayloadPullSpec string
	// PayloadVersion is the OCP version of the passed payload (evidence the CPO is newer
	// than the pinned 4.20.x guest).
	PayloadVersion string
}

// ResolveCPOImageFromPayload resolves the control-plane-operator image from the
// release payload the CI job passed in. The payload's embedded "hypershift" component
// image IS the CPO built from the PR under test. This mirrors the precedence-#2 lookup
// in support/imageregistry.GetControlPlaneOperatorImage (ComponentImages()["hypershift"]).
//
// It returns an error if the pull secret cannot be read, the payload cannot be looked
// up, or the payload does not embed a "hypershift" component image. It never falls back
// to a default image: a missing "hypershift" component means the passed payload is not a
// usable CPO source, and silently proceeding would defeat the experiment.
func ResolveCPOImageFromPayload(ctx context.Context, passedReleaseImage, pullSecretFile string) (ResolvedCPO, error) {
	data, err := os.ReadFile(pullSecretFile)
	if err != nil {
		return ResolvedCPO{}, fmt.Errorf("error reading pull secret file %q: %w", pullSecretFile, err)
	}
	provider := releaseinfo.RegistryClientProvider{}
	releaseImage, err := provider.Lookup(ctx, passedReleaseImage, data)
	if err != nil {
		return ResolvedCPO{}, fmt.Errorf("error looking up passed release image %q: %w", passedReleaseImage, err)
	}
	cpoImage, ok := releaseImage.ComponentImages()["hypershift"]
	if !ok {
		return ResolvedCPO{}, fmt.Errorf("passed release image %q does not contain a \"hypershift\" component image; cannot resolve CPO from payload", passedReleaseImage)
	}
	return ResolvedCPO{
		CPOImage:        cpoImage,
		PayloadPullSpec: passedReleaseImage,
		PayloadVersion:  releaseImage.Version(),
	}, nil
}

// LogExperimentPinning emits a single, clearly-delimited audit block describing the
// main-CPO-on-4.20 experiment pinning: the CPO image and its provenance (resolved from
// the passed payload), and the pinned guest release versions. It compares the payload
// version against the pinned base guest version and reports the TRUE ordering (newer /
// same / older) rather than asserting drift that may not exist. logf is any printf-style
// logger (t.Logf, log.Printf, GinkgoWriter.Printf, etc.) so the same format is used
// everywhere the pinning happens.
func LogExperimentPinning(logf func(format string, args ...any), where string, cpo ResolvedCPO, baseRelease, upgradeRelease string) {
	logf("===== EXPERIMENT main-CPO-on-4.20 PINNING [%s] =====", where)
	logf("  CPO image (from PR payload): %s", cpo.CPOImage)
	logf("  CPO provenance: resolved as ComponentImages()[\"hypershift\"] of passed payload %s (version %s)", cpo.PayloadPullSpec, cpo.PayloadVersion)
	logf("  CPO-vs-guest version relationship: %s", cpoVersusGuest(cpo.PayloadVersion, baseRelease))
	logf("  HC guest release (base):    %s", baseRelease)
	if upgradeRelease != "" {
		logf("  HC guest release (upgrade): %s", upgradeRelease)
	}
	logf("  applied via annotation: hypershift.openshift.io/control-plane-operator-image=%s", cpo.CPOImage)
	logf("=====================================================")
}

// cpoVersusGuest returns a human-readable, truthful comparison of the payload (CPO)
// version against the pinned guest release. It parses the guest version out of the pinned
// pullspec tag (e.g. ".../ocp-release:4.20.42-multi" -> "4.20.42"). If either version
// cannot be parsed/compared, it reports both without asserting an order.
func cpoVersusGuest(payloadVersion, guestPullSpec string) string {
	guestVersion := guestVersionFromPullSpec(guestPullSpec)
	pv, perr := semver.Parse(normalizeSemver(payloadVersion))
	gv, gerr := semver.Parse(normalizeSemver(guestVersion))
	if perr != nil || gerr != nil {
		return fmt.Sprintf("payload version %q vs guest version %q (could not compare)", payloadVersion, guestVersion)
	}
	switch pv.Compare(gv) {
	case 1:
		return fmt.Sprintf("payload %s is NEWER than guest %s (CPO drives an older guest)", payloadVersion, guestVersion)
	case -1:
		return fmt.Sprintf("payload %s is OLDER than guest %s", payloadVersion, guestVersion)
	default:
		return fmt.Sprintf("payload %s is the SAME version as guest %s", payloadVersion, guestVersion)
	}
}

// guestVersionFromPullSpec extracts the version from a release pullspec's tag, e.g.
// "quay.io/openshift-release-dev/ocp-release:4.20.42-multi" -> "4.20.42".
func guestVersionFromPullSpec(pullSpec string) string {
	idx := strings.LastIndex(pullSpec, ":")
	if idx < 0 || idx == len(pullSpec)-1 {
		return pullSpec
	}
	tag := pullSpec[idx+1:]
	// Strip a trailing arch/variant suffix like "-multi" or "-x86_64".
	if dash := strings.Index(tag, "-"); dash >= 0 {
		tag = tag[:dash]
	}
	return tag
}

// normalizeSemver trims a leading "v" so semver.Parse accepts tags like "v4.20.42".
func normalizeSemver(v string) string {
	return strings.TrimPrefix(v, "v")
}
