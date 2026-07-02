package oapi

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestNewComponent(t *testing.T) {
	t.Run("When creating component it should build successfully", func(t *testing.T) {
		g := NewWithT(t)
		component := NewComponent()
		g.Expect(component).ToNot(BeNil())
		g.Expect(component.Name()).To(Equal(ComponentName))
	})
}
