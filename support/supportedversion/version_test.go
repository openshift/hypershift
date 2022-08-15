package supportedversion

import (
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
)

func TestSupportedVersions(t *testing.T) {
	g := NewGomegaWithT(t)
	g.Expect(Supported()).To(Equal([]string{"4.12", "4.11", "4.10"}))
}
