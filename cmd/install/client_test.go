package install

import (
	"io"
	"testing"

	. "github.com/onsi/gomega"
)

func TestApplyWithNilClient(t *testing.T) {
	NewWithT(t).Expect(apply(t.Context(), io.Discard, nil, nil)).To(MatchError("management-cluster client is required"))
}

func TestDryRunValidateCRDsWithNilClient(t *testing.T) {
	NewWithT(t).Expect(dryRunValidateCRDs(t.Context(), io.Discard, nil, nil)).To(MatchError("management-cluster client is required"))
}

func TestWaitUntilEstablishedWithNilClient(t *testing.T) {
	NewWithT(t).Expect(waitUntilEstablished(t.Context(), nil, nil)).To(MatchError("management-cluster client is required"))
}

func TestWaitUntilAvailableWithNilClient(t *testing.T) {
	_, err := WaitUntilAvailable(t.Context(), Options{}, nil)
	NewWithT(t).Expect(err).To(MatchError("management-cluster client is required"))
}
