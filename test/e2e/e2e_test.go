//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/version"
	"github.com/openshift/hypershift/test/e2e/podtimingcontroller"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var (
	// opts are global options for the test suite bound in TestMain.
	globalOpts = &options{}

	// testContext should be used as the parent context for any test code, and will
	// be cancelled if a SIGINT or SIGTERM is received. It's set up in TestMain.
	testContext context.Context

	log = zap.New(zap.UseDevMode(true), zap.JSONEncoder(), func(o *zap.Options) {
		o.TimeEncoder = zapcore.RFC3339TimeEncoder
	})
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// TestMain deals with global options and setting up a signal-bound context
// for all tests to use.
func TestMain(m *testing.M) {
	flag.StringVar(&globalOpts.configurableClusterOptions.AWSCredentialsFile, "e2e.aws-credentials-file", "", "path to AWS credentials")
	flag.StringVar(&globalOpts.configurableClusterOptions.Region, "e2e.aws-region", "us-east-1", "AWS region for clusters")
	flag.StringVar(&globalOpts.configurableClusterOptions.PullSecretFile, "e2e.pull-secret-file", "", "path to pull secret")
	flag.StringVar(&globalOpts.configurableClusterOptions.AWSEndpointAccess, "e2e.aws-endpoint-access", "", "endpoint access profile for the cluster")
	flag.StringVar(&globalOpts.LatestReleaseImage, "e2e.latest-release-image", "", "The latest OCP release image for use by tests")
	flag.StringVar(&globalOpts.PreviousReleaseImage, "e2e.previous-release-image", "", "The previous OCP release image relative to the latest")
	flag.StringVar(&globalOpts.ArtifactDir, "e2e.artifact-dir", "", "The directory where cluster resources and logs should be dumped. If empty, nothing is dumped")
	flag.StringVar(&globalOpts.configurableClusterOptions.BaseDomain, "e2e.base-domain", "", "The ingress base domain for the cluster")
	flag.StringVar(&globalOpts.configurableClusterOptions.ControlPlaneOperatorImage, "e2e.control-plane-operator-image", "", "The image to use for the control plane operator. If none specified, the default is used.")
	flag.Var(&globalOpts.additionalTags, "e2e.additional-tags", "Additional tags to set on AWS resources")

	flag.Parse()

	// Set defaults for the test options
	if err := globalOpts.Complete(); err != nil {
		log.Error(err, "failed to set up global test options")
		os.Exit(1)
	}

	// Validate the test options
	if err := globalOpts.Validate(); err != nil {
		log.Error(err, "invalid global test options")
		os.Exit(1)
	}

	os.Exit(main(m))
}

// main is used to allow us to use `defer` to defer cleanup task
// to after the tests are run. We can't do this in `TestMain` because
// it does an os.Exit (We could avoid that but then the deferred
// calls do not get executed after the tests, just at the end of
// TestMain()).
func main(m *testing.M) int {
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

	if globalOpts.ArtifactDir != "" {
		go setupMetricsEndpoint(testContext, log)
		go e2eObserverControllers(testContext, log, globalOpts.ArtifactDir)
		defer dumpTestMetrics(log, globalOpts.ArtifactDir)
	}

	// Everything's okay to run tests
	log.Info("executing e2e tests", "options", globalOpts)
	return m.Run()
}

func e2eObserverControllers(ctx context.Context, log logr.Logger, artifactDir string) {
	mgr, err := ctrl.NewManager(e2eutil.GetConfigOrDie(), manager.Options{MetricsBindAddress: "0"})
	if err != nil {
		log.Error(err, "failed to construct manager for observers")
		return
	}
	if err := podtimingcontroller.SetupWithManager(mgr, log, artifactDir); err != nil {
		log.Error(err, "failed to set up podtimingcontroller")
		return
	}

	if err := mgr.Start(ctx); err != nil {
		log.Error(err, "Mgr ended")
	}
}

const metricsServerAddr = "127.0.0.1:8080"

func setupMetricsEndpoint(ctx context.Context, log logr.Logger) {
	log.Info("Setting up metrics endpoint")
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	server := &http.Server{Addr: metricsServerAddr, Handler: mux}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error(err, "metrics server ended unexpectedly")
	}
}

func dumpTestMetrics(log logr.Logger, artifactDir string) {
	log.Info("Fetching test metrics")
	response, err := http.Get("http://" + metricsServerAddr + "/metrics")
	if err != nil {
		log.Error(err, "error fetching test metrics")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		log.Error(fmt.Errorf("status code %d", response.StatusCode), "Got unexpected status code from metrics endpoint")
		return
	}

	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Error(err, "failed to read response body from metrics endpoint")
		return
	}

	path := filepath.Join(artifactDir, "e2e-metrics-raw.prometheus")
	log = log.WithValues("path", path)
	if err := os.WriteFile(path, body, 0644); err != nil {
		log.Error(err, "failed to write e2e metrics to artifacts")
	}
	log.Info("Successfully wrote metrics to artifacts")
}

// options are global test options applicable to all scenarios.
type options struct {
	LatestReleaseImage   string
	PreviousReleaseImage string
	IsRunningInCI        bool
	ArtifactDir          string

	configurableClusterOptions configurableClusterOptions
	additionalTags             stringSliceVar
}

type configurableClusterOptions struct {
	AWSCredentialsFile        string
	Region                    string
	PullSecretFile            string
	BaseDomain                string
	ControlPlaneOperatorImage string
	AWSEndpointAccess         string
}

func (o *options) DefaultClusterOptions() core.CreateOptions {
	createOption := core.CreateOptions{
		ReleaseImage:              o.PreviousReleaseImage,
		GenerateSSH:               true,
		SSHKeyFile:                "",
		NodePoolReplicas:          2,
		NetworkType:               string(hyperv1.OpenShiftSDN),
		PullSecretFile:            o.configurableClusterOptions.PullSecretFile,
		ControlPlaneOperatorImage: o.configurableClusterOptions.ControlPlaneOperatorImage,
		AWSPlatform: core.AWSPlatformOptions{
			InstanceType:       "m4.large",
			RootVolumeSize:     64,
			RootVolumeType:     "gp2",
			BaseDomain:         o.configurableClusterOptions.BaseDomain,
			AWSCredentialsFile: o.configurableClusterOptions.AWSCredentialsFile,
			Region:             o.configurableClusterOptions.Region,
			EndpointAccess:     o.configurableClusterOptions.AWSEndpointAccess,
		},
	}
	createOption.AWSPlatform.AdditionalTags = append(createOption.AWSPlatform.AdditionalTags, o.additionalTags...)

	return createOption
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
		if len(o.configurableClusterOptions.BaseDomain) == 0 {
			// TODO: make this an envvar with change to openshift/release, then change here
			o.configurableClusterOptions.BaseDomain = "origin-ci-int-aws.dev.rhcloud.com"
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

	if len(o.configurableClusterOptions.BaseDomain) == 0 {
		errs = append(errs, fmt.Errorf("base domain is required"))
	}

	return errors.NewAggregate(errs)
}

var _ flag.Value = &stringSliceVar{}

// stringSliceVar mimicks github.com/spf13/pflag.StringSliceVar in a stdlib-compatible way
type stringSliceVar []string

func (s *stringSliceVar) String() string     { return strings.Join(*s, ",") }
func (s *stringSliceVar) Set(v string) error { *s = append(*s, strings.Split(v, ",")...); return nil }
