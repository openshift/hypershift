//go:build e2ev2

package tests

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/test/e2e/v2/internal"

	"github.com/blang/semver"
)

func nativeOriginAtLeast(tc *internal.TestContext, minimum semver.Version) (bool, error) {
	version, err := healthReleaseVersion(tc)
	if err != nil {
		return false, fmt.Errorf("resolve originating release for minimum %s: %w", minimum, err)
	}
	return version.GTE(minimum), nil
}

func skipIfNativeOriginBelow(tc *internal.TestContext, minimum semver.Version) {
	GinkgoHelper()
	applicable, err := nativeOriginAtLeast(tc, minimum)
	Expect(err).NotTo(HaveOccurred())
	if !applicable {
		Skip(fmt.Sprintf("requires originating release %s or newer", minimum))
	}
}
