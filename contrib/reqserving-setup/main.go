package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/openshift/hypershift/test/e2e/util/reqserving"

	ctrl "sigs.k8s.io/controller-runtime"
	crzap "sigs.k8s.io/controller-runtime/pkg/log/zap"

	"go.uber.org/zap/zapcore"
)

func main() {
	var dryRunDir string
	flag.StringVar(&dryRunDir, "dry-run-dir", "", "If specified, only output YAMLs to this directory")
	flag.Parse()

	log := crzap.New(crzap.UseDevMode(true), crzap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	}))
	ctrl.SetLogger(log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Info("received shutdown signal and will be canceled")
		cancel()
	}()

	var dryRunOpts *reqserving.DryRunOptions
	if dryRunDir != "" {
		dryRunOpts = &reqserving.DryRunOptions{
			Dir: dryRunDir,
		}
		if err := os.MkdirAll(dryRunOpts.Dir, 0o755); err != nil {
			log.Error(err, "failed to create dry run directory")
			os.Exit(1)
		}
	}

	log.Info("configuring management cluster for request serving")
	if err := reqserving.ConfigureManagementCluster(ctx, dryRunOpts); err != nil {
		log.Error(err, "failed to configure management cluster")
		os.Exit(1)
	}

	log.Info("management cluster configured successfully")
}
