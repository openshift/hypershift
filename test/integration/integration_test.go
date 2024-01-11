package integration

import (
	"context"
	"flag"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/openshift/hypershift/test/integration/framework"
	"go.uber.org/zap/zapcore"
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
	var cancel context.CancelFunc
	testContext, cancel = context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Info("tests received shutdown signal and will be cancelled")
		cancel()
	}()

	log.Info("skipping the following cleanup steps", "steps", framework.SkippedCleanupSteps().UnsortedList())

	var cleanups []func()
	for _, item := range []struct {
		name    string
		builder framework.Builder
	}{
		{
			name:    "assets",
			builder: framework.InstallAssets,
		},
		{
			name:    "hypershift operator assets",
			builder: framework.InstallHyperShiftOperator,
		},
		{
			name:    "hypershift operator",
			builder: framework.EmulateHyperShiftOperator,
		},
	} {
		item := item
		cleanup, err := item.builder(testContext, log, globalOpts)
		cleanups = append(cleanups, func() {
			if err := cleanup(); err != nil {
				log.Error(err, "cleaning up "+item.name)
			}
		})
		if err != nil {
			log.Error(err, "setting up "+item.name)
			os.Exit(1)
		}
	}

	out := m.Run()

	for _, cleanup := range cleanups {
		cleanup()
	}

	os.Exit(out)
}
