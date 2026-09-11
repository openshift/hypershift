package aws

import (
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/cluster/core"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestNewCreateCommandClientProvider(t *testing.T) {
	t.Run("When credential validation needs a client and the provider fails, it should return the provider error", func(t *testing.T) {
		g := NewWithT(t)
		opts := &core.RawCreateOptions{
			Name:           "test-cluster",
			Namespace:      "clusters",
			PullSecretFile: "/dev/null",
		}
		cmd := NewCreateCommand(opts, &core.ClientProvider{
			ControllerRuntimeClient: func(string) (crclient.Client, error) {
				return nil, errors.New("management client unavailable")
			},
		})
		cmd.SetArgs([]string{"--secret-creds", "cloud-credentials"})

		err := cmd.Execute()
		g.Expect(err).To(MatchError("management client unavailable"))
	})
}
