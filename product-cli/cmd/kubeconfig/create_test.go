package kubeconfig

import (
	"testing"

	. "github.com/onsi/gomega"

	hypershiftkubeconfig "github.com/openshift/hypershift/cmd/kubeconfig"
)

func TestNewCreateCommand(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "When kubeconfig create command is created, it should have 'kubeconfig' as use",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()
				g.Expect(cmd.Use).To(Equal("kubeconfig"))
			},
		},
		{
			name: "When kubeconfig create command is created, it should set long description from shared package",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()
				g.Expect(cmd.Long).To(Equal(hypershiftkubeconfig.Description))
			},
		},
		{
			name: "When kubeconfig create command is created, it should default namespace to clusters",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()

				flag := cmd.Flag("namespace")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal("clusters"))
			},
		},
		{
			name: "When kubeconfig create command is created, it should register name flag with empty default",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()

				flag := cmd.Flag("name")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(BeEmpty())
			},
		},
		{
			name: "When kubeconfig create command is created, it should default port-forward to false",
			test: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand()

				flag := cmd.Flag("port-forward")
				g.Expect(flag).ToNot(BeNil())
				g.Expect(flag.DefValue).To(Equal("false"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.test(t)
		})
	}
}
