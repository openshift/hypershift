package framework

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	controllerruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
)

type ManagementTestContext struct {
	Opts          *Options
	HostedCluster *hypershiftv1beta1.HostedCluster
	MgmtCluster   *Clients
}

type TestContext struct {
	*ManagementTestContext
	GuestCluster *Clients
}

type Clients struct {
	Cfg              *rest.Config
	KubeClient       kubernetes.Interface
	HyperShiftClient hypershiftclient.Interface
	CRClient         controllerruntimeclient.Client
}

// RunHostedClusterTest takes a test closure and invokes it once the HostedCluster is available. Tests for HostedCluster
// functionality should use this entrypoint.
func RunHostedClusterTest(ctx context.Context, logger logr.Logger, globalOpts *Options, t *testing.T, test func(t *testing.T, ctx *TestContext)) {
	var cleanups []Cleanup
	defer func() {
		t.Log("cleaning up...")
		cleanupCtx := InterruptableContext(context.Background())
		for _, cleanup := range cleanups {
			if err := cleanup(cleanupCtx); err != nil {
				t.Errorf("failed to clean up: %v", err)
			}
		}
	}()
	cleanup, mgmtCtx := setupHostedCluster(ctx, logger, globalOpts, HostedClusterOptions{}, t)
	cleanups = append(cleanups, cleanup)
	testCtx := &TestContext{
		ManagementTestContext: mgmtCtx,
	}
	cleanup, err := WaitForHostedClusterAvailable(ctx, logger, testCtx.Opts, t)
	if err != nil {
		t.Fatalf("failed to set up hosted cluster: %v", err)
	}
	cleanups = append(cleanups, cleanup)

	guestCfg, err := WaitForGuestRestConfig(ctx, logger, testCtx.Opts, t, testCtx.HostedCluster)
	if err != nil {
		t.Fatalf("couldn't get guest cluster *rest.Config: %v", err)
	}

	testCtx.GuestCluster, err = NewClients(guestCfg)
	if err != nil {
		t.Fatalf("couldn't initialize clients for guest: %v", err)
	}

	test(t, testCtx)
}

// RunHyperShiftOperatorTest takes a test closure and invokes it once the HostedCluster resource is created, but without
// waiting for the guest to be available, nor giving the test access to the guest cluster. Tests for the HyperShift Operator
// are best suited for this entrypoint.
func RunHyperShiftOperatorTest(ctx context.Context, logger logr.Logger, globalOpts *Options, hostedClusterOpts HostedClusterOptions, t *testing.T, test func(t *testing.T, ctx *ManagementTestContext)) {
	cleanup, testCtx := setupHostedCluster(ctx, logger, globalOpts, hostedClusterOpts, t)
	defer func() {
		t.Log("cleaning up...")
		cleanupCtx := InterruptableContext(context.Background())
		if err := cleanup(cleanupCtx); err != nil {
			t.Errorf("failed to clean up: %v", err)
		}
	}()
	test(t, testCtx)
}

func setupHostedCluster(ctx context.Context, logger logr.Logger, globalOpts *Options, hostedClusterOpts HostedClusterOptions, t *testing.T) (Cleanup, *ManagementTestContext) {
	testCtx := &ManagementTestContext{}
	opts := *globalOpts
	opts.ArtifactDir = filepath.Join(opts.ArtifactDir, ArtifactDirectoryFor(t))
	testCtx.Opts = &opts

	cleanup := CleanupSentinel
	switch globalOpts.Mode {
	case SetupMode, AllInOneMode:
		var err error
		cleanup, err = InstallHostedCluster(ctx, logger, &opts, hostedClusterOpts, t)
		if err != nil {
			t.Fatalf("failed to set up hosted cluster: %v", err)
		}
	case TestMode:
		break
	}

	switch globalOpts.Mode {
	case SetupMode:
		t.Log("setup complete, waiting...")
		waitCtx := InterruptableContext(context.Background())
		<-waitCtx.Done()
		return cleanup, nil
	case TestMode, AllInOneMode:
		break
	}

	cfg, err := LoadKubeConfig(opts.Kubeconfig)
	if err != nil {
		t.Fatalf("couldn't load client configuration: %v", err)
	}
	testCtx.MgmtCluster, err = NewClients(cfg)
	if err != nil {
		t.Fatalf("couldn't initialize clients for mgmt plane: %v", err)
	}

	hostedClusterName := HostedClusterFor(t)
	testCtx.HostedCluster, err = testCtx.MgmtCluster.HyperShiftClient.HypershiftV1beta1().HostedClusters(HostedClusterNamespace).Get(ctx, hostedClusterName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("couldn't get hosted cluster %s/%s: %v", HostedClusterNamespace, hostedClusterName, err)
	}

	return cleanup, testCtx
}

func NewClients(cfg *rest.Config) (*Clients, error) {
	out := &Clients{
		Cfg: cfg,
	}
	var err error
	out.CRClient, err = controllerruntimeclient.New(cfg, controllerruntimeclient.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("couldn't create controller-runtime client: %w", err)
	}
	out.HyperShiftClient, err = hypershiftclient.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("couldn't create hypershift client: %w", err)
	}
	out.KubeClient, err = kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("could not create k8s client: %w", err)
	}
	return out, nil
}
