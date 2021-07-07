// +build e2e

package e2e

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"testing"

	e2elog "github.com/openshift/hypershift/test/e2e/log"
	"github.com/openshift/hypershift/test/e2e/scenarios"
	"github.com/openshift/hypershift/version"
	"k8s.io/apimachinery/pkg/util/errors"
)

var (
	// opts are global options for the test suite bound in TestMain.
	opts = &options{}

	// ctx should be used as the parent context for any test code, and will
	// be cancelled if a SIGINT or SIGTERM is received. It's set up in TestMain.
	ctx context.Context

	log = e2elog.Logger
)

// TestScenarios runs all the e2e tests. Any new tests need to be added to this
// list in order for them to run.
func TestScenarios(t *testing.T) {
	tests := map[string]func(t *testing.T){
		"CreateCluster": scenarios.TestCreateCluster(ctx,
			scenarios.TestCreateClusterOptions{
				AWSCredentialsFile: opts.AWSCredentialsFile,
				AWSRegion:          opts.Region,
				PullSecretFile:     opts.PullSecretFile,
				ReleaseImage:       opts.LatestReleaseImage,
				ArtifactDir:        opts.ArtifactDir,
				BaseDomain:         opts.BaseDomain,
			}),
		"UpgradeControlPlane": scenarios.TestUpgradeControlPlane(ctx,
			scenarios.TestUpgradeControlPlaneOptions{
				AWSCredentialsFile: opts.AWSCredentialsFile,
				AWSRegion:          opts.Region,
				BaseDomain:         opts.BaseDomain,
				PullSecretFile:     opts.PullSecretFile,
				FromReleaseImage:   opts.PreviousReleaseImage,
				ToReleaseImage:     opts.LatestReleaseImage,
				ArtifactDir:        opts.ArtifactDir,
				Enabled:            opts.UpgradeTestsEnabled,
			}),
	}

	for name := range tests {
		fn := tests[name]
		t.Run(name, fn)
	}
}

// TestMain deals with global options and setting up a signal-bound context
// for all tests to use.
func TestMain(m *testing.M) {
	flag.StringVar(&opts.AWSCredentialsFile, "e2e.aws-credentials-file", "", "path to AWS credentials")
	flag.StringVar(&opts.Region, "e2e.aws-region", "us-east-1", "AWS region for clusters")
	flag.StringVar(&opts.PullSecretFile, "e2e.pull-secret-file", "", "path to pull secret")
	flag.StringVar(&opts.LatestReleaseImage, "e2e.latest-release-image", "", "The latest OCP release image for use by tests")
	flag.StringVar(&opts.PreviousReleaseImage, "e2e.previous-release-image", "", "The previous OCP release image relative to the latest")
	flag.StringVar(&opts.ArtifactDir, "e2e.artifact-dir", "", "The directory where cluster resources and logs should be dumped. If empty, nothing is dumped")
	flag.StringVar(&opts.BaseDomain, "e2e.base-domain", "", "The ingress base domain for the cluster")
	flag.BoolVar(&opts.UpgradeTestsEnabled, "e2e.upgrade-tests-enabled", false, "Enables upgrade tests")
	flag.Parse()

	// Set defaults for the test options
	if err := opts.Complete(); err != nil {
		log.Error(err, "failed to set up global test options")
		os.Exit(1)
	}

	// Validate the test options
	if err := opts.Validate(); err != nil {
		log.Error(err, "invalid global test options")
		os.Exit(1)
	}

	// Set up a root context for all tests and set up signal handling
	rootCtx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Info("tests received shutdown signal and will be cancelled")
		cancel()
	}()
	ctx = rootCtx

	// Everything's okay to run tests
	log.Info("executing e2e tests", "options", opts)
	os.Exit(m.Run())
}

// options are global test options applicable to all scenarios.
type options struct {
	AWSCredentialsFile   string
	Region               string
	PullSecretFile       string
	LatestReleaseImage   string
	PreviousReleaseImage string
	IsRunningInCI        bool
	UpgradeTestsEnabled  bool
	ArtifactDir          string
	BaseDomain           string
}

// Complete is intended to be called after flags have been bound and sets
// up additional contextual defaulting.
func (o *options) Complete() error {
	if len(o.LatestReleaseImage) == 0 {
		defaultVersion, err := version.LookupDefaultOCPVersion()
		if err != nil {
			return fmt.Errorf("couldn't look up default OCP version: %w", err)
		}
		o.LatestReleaseImage = defaultVersion.PullSpec
	}
	// TODO: This is actually basically a required field right now. Maybe the input
	// to tests should be a small API spec that describes the tests and their
	// inputs to avoid having to make every test input required. Or extract
	// e2e test suites into subcommands with their own distinct flags to make
	// selectively running them easier?
	if len(o.PreviousReleaseImage) == 0 {
		o.PreviousReleaseImage = o.LatestReleaseImage
	}

	o.IsRunningInCI = os.Getenv("OPENSHIFT_CI") == "true"

	if o.IsRunningInCI {
		if len(o.ArtifactDir) == 0 {
			o.ArtifactDir = os.Getenv("ARTIFACT_DIR")
		}
		if len(o.BaseDomain) == 0 {
			// TODO: make this an envvar with change to openshift/release, then change here
			o.BaseDomain = "origin-ci-int-aws.dev.rhcloud.com"
		}
	}

	return nil
}

// Validate is intended to be called after Complete and validates the options
// are usable by tests.
func (o *options) Validate() error {
	var errs []error

	if len(o.LatestReleaseImage) == 0 {
		errs = append(errs, fmt.Errorf("latest release image is required"))
	}

	if len(o.BaseDomain) == 0 {
		errs = append(errs, fmt.Errorf("base domain is required"))
	}

	return errors.NewAggregate(errs)
}
