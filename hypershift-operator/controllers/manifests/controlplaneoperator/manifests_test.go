package controlplaneoperator

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestCombinedPullSecret(t *testing.T) {
	g := NewWithT(t)
	secret := CombinedPullSecret("test-ns")
	g.Expect(secret.Name).To(Equal("combined-pull-secret"))
	g.Expect(secret.Namespace).To(Equal("test-ns"))
}
