package openstack

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestNewDestroyCommand(t *testing.T) {
	t.Parallel()

	t.Run("When command is created, it should be invoked as 'openstack' and wire a RunE handler", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		opts := &core.DestroyOptions{}
		cmd := NewDestroyCommand(opts)
		g.Expect(cmd.Use).To(Equal("openstack"))
		g.Expect(cmd.RunE).ToNot(BeNil())
	})
}

func TestDestroyCluster(t *testing.T) {
	t.Parallel()

	// notCalledCoreDestroy fails the test if the destroy path reaches the core
	// destroy when it is expected to bail out earlier.
	notCalledCoreDestroy := func(t *testing.T) coreDestroyFunc {
		return func(_ context.Context, _ *hyperv1.HostedCluster, _ *core.DestroyOptions, _ core.DestroyPlatformSpecifics) error {
			t.Helper()
			t.Error("core destroy should not be called")
			return nil
		}
	}

	t.Run("When getting the hosted cluster fails, it should return the error without destroying", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)

		getErr := errors.New("unable to get kubernetes config")
		getCluster := func(_ context.Context, _ *core.DestroyOptions) (*hyperv1.HostedCluster, error) {
			return nil, getErr
		}

		opts := &core.DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			InfraID:   "test-infra",
			Log:       log.Log,
		}

		err := destroyCluster(context.Background(), opts, getCluster, notCalledCoreDestroy(t))
		g.Expect(err).To(MatchError(getErr))
	})

	t.Run("When the hosted cluster is not found and no infra ID is given, it should return a missing inputs error", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)

		getCluster := func(_ context.Context, _ *core.DestroyOptions) (*hyperv1.HostedCluster, error) {
			return nil, nil
		}

		opts := &core.DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			Log:       log.Log,
		}

		err := destroyCluster(context.Background(), opts, getCluster, notCalledCoreDestroy(t))
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("required inputs are missing"))
		g.Expect(err.Error()).To(ContainSubstring("infrastructure ID is required"))
	})

	t.Run("When the hosted cluster exists, it should take the infra ID from its spec", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)

		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "test-cluster"},
			Spec:       hyperv1.HostedClusterSpec{InfraID: "test-cluster-l4bfq"},
		}
		getCluster := func(_ context.Context, _ *core.DestroyOptions) (*hyperv1.HostedCluster, error) {
			return hc, nil
		}

		var gotInfraID string
		var gotHC *hyperv1.HostedCluster
		coreDestroy := func(_ context.Context, hostedCluster *hyperv1.HostedCluster, o *core.DestroyOptions, _ core.DestroyPlatformSpecifics) error {
			gotHC = hostedCluster
			gotInfraID = o.InfraID
			return nil
		}

		opts := &core.DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			// Stale value from the CLI flag; the HC spec must win.
			InfraID: "stale-infra",
			Log:     log.Log,
		}

		err := destroyCluster(context.Background(), opts, getCluster, coreDestroy)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(gotHC).To(BeIdenticalTo(hc))
		g.Expect(gotInfraID).To(Equal("test-cluster-l4bfq"))
		g.Expect(opts.InfraID).To(Equal("test-cluster-l4bfq"))
	})

	t.Run("When the hosted cluster is not found but an infra ID is given, it should destroy using the given infra ID", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)

		getCluster := func(_ context.Context, _ *core.DestroyOptions) (*hyperv1.HostedCluster, error) {
			return nil, nil
		}

		var coreDestroyCalled bool
		var gotHC *hyperv1.HostedCluster
		var gotInfraID string
		coreDestroy := func(_ context.Context, hostedCluster *hyperv1.HostedCluster, o *core.DestroyOptions, _ core.DestroyPlatformSpecifics) error {
			coreDestroyCalled = true
			gotHC = hostedCluster
			gotInfraID = o.InfraID
			return nil
		}

		opts := &core.DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			InfraID:   "test-cluster-l4bfq",
			Log:       log.Log,
		}

		err := destroyCluster(context.Background(), opts, getCluster, coreDestroy)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(coreDestroyCalled).To(BeTrue())
		g.Expect(gotHC).To(BeNil())
		g.Expect(gotInfraID).To(Equal("test-cluster-l4bfq"))
	})

	t.Run("When the hosted cluster exists with an empty infra ID in its spec, it should return a missing inputs error", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)

		// An HC whose spec has no InfraID overwrites the flag value with "",
		// which must still be caught by the input validation.
		getCluster := func(_ context.Context, _ *core.DestroyOptions) (*hyperv1.HostedCluster, error) {
			return &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "test-cluster"},
			}, nil
		}

		opts := &core.DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			InfraID:   "test-cluster-l4bfq",
			Log:       log.Log,
		}

		err := destroyCluster(context.Background(), opts, getCluster, notCalledCoreDestroy(t))
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("infrastructure ID is required"))
		g.Expect(opts.InfraID).To(BeEmpty())
	})

	t.Run("When the core destroy fails, it should propagate the error", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)

		getCluster := func(_ context.Context, _ *core.DestroyOptions) (*hyperv1.HostedCluster, error) {
			return nil, nil
		}

		destroyErr := errors.New("failed to delete hostedcluster")
		coreDestroy := func(_ context.Context, _ *hyperv1.HostedCluster, _ *core.DestroyOptions, _ core.DestroyPlatformSpecifics) error {
			return destroyErr
		}

		opts := &core.DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			InfraID:   "test-cluster-l4bfq",
			Log:       log.Log,
		}

		err := destroyCluster(context.Background(), opts, getCluster, coreDestroy)
		g.Expect(err).To(MatchError(destroyErr))
	})

	t.Run("When destroying, it should pass the OpenStack platform specifics to the core destroy", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)

		getCluster := func(_ context.Context, _ *core.DestroyOptions) (*hyperv1.HostedCluster, error) {
			return nil, nil
		}

		var gotPlatformSpecifics core.DestroyPlatformSpecifics
		coreDestroy := func(_ context.Context, _ *hyperv1.HostedCluster, _ *core.DestroyOptions, destroyPlatformSpecifics core.DestroyPlatformSpecifics) error {
			gotPlatformSpecifics = destroyPlatformSpecifics
			return nil
		}

		opts := &core.DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			InfraID:   "test-cluster-l4bfq",
			Log:       log.Log,
		}

		err := destroyCluster(context.Background(), opts, getCluster, coreDestroy)
		g.Expect(err).ToNot(HaveOccurred())
		// A non-nil hook is what makes core.DestroyCluster take the finalizer
		// path rather than waiting for the HostedCluster to disappear.
		g.Expect(gotPlatformSpecifics).ToNot(BeNil())
		g.Expect(gotPlatformSpecifics(context.Background(), opts, nil)).To(Succeed())
	})
}

func TestDestroyPlatformSpecifics(t *testing.T) {
	t.Parallel()

	// Contract test: OpenStack infrastructure is destroyed by the cloud provider
	// via the HostedCluster deletion, so the CLI has nothing extra to clean up.
	// This must stay a no-op unless OpenStack starts creating CLI-owned infra.
	t.Run("When called it should be a no-op and return nil", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		opts := &core.DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			InfraID:   "test-cluster-l4bfq",
			Log:       log.Log,
		}
		before := *opts
		err := destroyPlatformSpecifics(context.Background(), opts, nil)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(*opts).To(Equal(before), "destroyPlatformSpecifics should not mutate the options")
	})
}

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
