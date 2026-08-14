//go:build integration

package integration

import (
	"context"
	"flag"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/openshift/hypershift/test/integration/framework"
	"go.uber.org/zap/zapcore"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	// opts are global options for the test suite bound in TestMain.
	globalOpts *framework.Options

	// testContext should be used as the parent context for any test code, and will
	// be cancelled if a SIGINT or SIGTERM is received. It's set up in TestMain.
	testContext context.Context

	log = zap.New(zap.UseDevMode(true), zap.ConsoleEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	}))
)

func init() {
	// something in controller-runtime pollutes the global flag namespace with an implicit side-effect
	// registry of --kubeconfig on import of their packages, so we reset the flag.CommandLine in order
	// to clear their value ... if we do this in *our* init(), we still get the `go test` flags
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	ctrl.SetLogger(log)

	rand.Seed(time.Now().UnixNano())
}

func TestMain(m *testing.M) {
	globalOpts = framework.DefaultOptions()
	globalOpts.Bind(flag.CommandLine)
	flag.Parse()

	if err := globalOpts.Validate(); err != nil {
		log.Error(err, "invalid options")
		os.Exit(1)
	}

	// Set up a root context for all tests and set up signal handling
	testContext = framework.InterruptableContext(context.Background())

	os.Exit(run(m, testContext, log, globalOpts))
}

func run(m *testing.M, ctx context.Context, logger logr.Logger, opts *framework.Options) int {
	switch opts.Mode {
	case framework.AllInOneMode, framework.SetupMode:
		var cleanups []func(context.Context)
		defer func() {
			cleanupCtx := framework.InterruptableContext(context.Background())
			log.Info("skipping the following cleanup steps", "steps", framework.SkippedCleanupSteps().UnsortedList())
			for _, cleanup := range cleanups {
				cleanup(cleanupCtx)
			}
		}()

		for _, item := range []struct {
			name    string
			builder framework.Builder
		}{
			{
				name:    "assets",
				builder: framework.InstallAssets,
			},
			{
				name:    "crds",
				builder: framework.InstallHyperShiftCRDs,
			},
			{
				name:    "crd establishment",
				builder: framework.WaitForHyperShiftCRDs,
			},
			{
				name:    "hypershift operator assets",
				builder: framework.InstallHyperShiftOperator,
			},
			{
				name:    "hypershift operator readiness",
				builder: framework.WaitForHyperShiftOperator,
			},
		} {
			item := item
			cleanup, err := item.builder(ctx, logger, opts)
			cleanups = append(cleanups, func(ctx context.Context) {
				if err := cleanup(ctx); err != nil {
					logger.Error(err, "cleaning up "+item.name)
				}
			})
			if err != nil {
				logger.Error(err, "setting up "+item.name)
				return 1
			}
		}
	case framework.TestMode:
		break
	}

	logger.Info("running tests")
	return m.Run()
}
