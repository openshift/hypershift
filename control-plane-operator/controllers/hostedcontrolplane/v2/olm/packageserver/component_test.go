package packageserver

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestNewComponent(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	component := NewComponent()
	g.Expect(component).ToNot(BeNil())
	g.Expect(component.Name()).To(Equal("packageserver"))
}
