// +build e2e

package e2e

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"testing"

	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/hypershift/version"
)

// GlobalTestContext should be used as the parent context for any test code, and will
// be cancelled if a SIGINT or SIGTERM is received.
var GlobalTestContext context.Context

type GlobalTestOptions struct {
	AWSCredentialsFile   string
	PullSecretFile       string
	LatestReleaseImage   string
	PreviousReleaseImage string
	IsRunningInCI        bool
	UpgradeTestsEnabled  bool
}

var GlobalOptions = &GlobalTestOptions{}

func init() {
	flag.StringVar(&GlobalOptions.AWSCredentialsFile, "e2e.aws-credentials-file", "", "path to AWS credentials")
	flag.StringVar(&GlobalOptions.PullSecretFile, "e2e.pull-secret-file", "", "path to pull secret")
	flag.StringVar(&GlobalOptions.LatestReleaseImage, "e2e.latest-release-image", "", "The latest OCP release image for use by tests")
	flag.StringVar(&GlobalOptions.PreviousReleaseImage, "e2e.previous-release-image", "", "The previous OCP release image relative to the latest")
}

func (o *GlobalTestOptions) SetDefaults() error {
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

	return nil
}

func (o *GlobalTestOptions) Validate() error {
	var errs []error

	if len(o.LatestReleaseImage) == 0 {
		errs = append(errs, fmt.Errorf("latest release image is required"))
	}

	return errors.NewAggregate(errs)
}

func TestMain(m *testing.M) {
	ctx, cancel := context.WithCancel(context.Background())
	GlobalTestContext = ctx

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Printf("tests received shutdown signal and will be cancelled")
		cancel()
	}()

	flag.Parse()

	if err := GlobalOptions.SetDefaults(); err != nil {
		log.Fatalf("failed to set up global test options: %s", err)
	}

	if err := GlobalOptions.Validate(); err != nil {
		log.Fatalf("invalid global test options: %s", err)
	}

	log.Printf("Running e2e tests with global options: %#v", GlobalOptions)

	os.Exit(m.Run())
}
