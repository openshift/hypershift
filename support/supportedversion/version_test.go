package supportedversion

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestSupportedVersions(t *testing.T) {
	g := NewGomegaWithT(t)
	g.Expect(Supported()).To(Equal([]string{"4.12", "4.11", "4.10"}))
}
