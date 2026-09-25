package powervs

import (
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/cluster/core"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestNewDestroyCommandClientProvider(t *testing.T) {
	t.Run("When management client creation fails, it should return the provider error", func(t *testing.T) {
		g := NewWithT(t)
		cmd := NewDestroyCommand(&core.DestroyOptions{}, &core.ClientProvider{
			ControllerRuntimeClient: func(string) (crclient.Client, error) {
				return nil, errors.New("management client unavailable")
			},
		})

		cmd.SetArgs([]string{})
		err := cmd.Execute()
		g.Expect(err).To(MatchError("management client unavailable"))
	})
}
