//go:build e2e
// +build e2e

package ginkgo

import (
	"context"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
)

var (
	// Global test context and options initialized in BeforeSuite
	testContext context.Context
	globalOpts  *e2eutil.Options
)

func TestCreateClusterGinkgo(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CreateCluster Ginkgo Suite")
}

var _ = BeforeSuite(func() {
	// Initialize options
	// For pilot migration, we rely on e2e package's TestMain having run first
	// which initializes globalOpts via flags. We just create a basic options
	// struct here. In a real scenario, this would need proper flag integration.
	globalOpts = &e2eutil.Options{
		Platform: hyperv1.AWSPlatform, // Default, can be overridden
	}
	// Parse basic options from environment for pilot
	if platform := os.Getenv("E2E_PLATFORM"); platform != "" {
		globalOpts.Platform = hyperv1.PlatformType(platform)
	}
	if artifactDir := os.Getenv("E2E_ARTIFACT_DIR"); artifactDir != "" {
		globalOpts.ArtifactDir = artifactDir
	}

	testContext = context.Background()
})
