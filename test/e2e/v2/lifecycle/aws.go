//go:build e2ev2

package lifecycle

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type AWSPlatformConfig struct {
	region         string
	zones          []string
	additionalTags []string
	sharedDir      string
}

type AWSPlatformOptions struct {
	Region string
	Zones  string
}

func NewAWSPlatformConfig(opts AWSPlatformOptions, sharedDir string) *AWSPlatformConfig {
	zones := strings.Split(opts.Zones, ",")

	// TODO: this is currently just to satisfy an assumption made by EnsureInfrastructureResourceTagsTest
	// and should probably be handled another way. That test assumes there is at least one pre-existing
	// non-kubernetes-namespaced tag on the infra.
	tags := []string{fmt.Sprintf("expirationDate=%s", time.Now().Add(4*time.Hour).UTC().Format(time.RFC3339))}

	cfg := &AWSPlatformConfig{
		region:         opts.Region,
		sharedDir:      sharedDir,
		additionalTags: tags,
		zones:          zones,
	}

	log.Printf("AWS platform config: region=%s, zones=%v, additionalTags=%v", cfg.region, cfg.zones, cfg.additionalTags)
	return cfg
}

func (a *AWSPlatformConfig) Name() string { return "aws" }

func (a *AWSPlatformConfig) DefaultBaseDomain() string {
	return "ci.hypershift.devcluster.openshift.com"
}

func (a *AWSPlatformConfig) ClusterSpecs(releaseImage, n1Image string) []ClusterSpec {
	// Parse EXTRA_ARGS from environment if provided
	var extraArgs []string
	if envArgs := os.Getenv("EXTRA_ARGS"); envArgs != "" {
		extraArgs = strings.Fields(envArgs)
	}
	return []ClusterSpec{
		{
			Variant: "public",
			ExtraArgs: append(extraArgs, []string{
				"--public-only",
			}...),
		},
		// The KarpenterBillingConsolidationTest actually tests hostedcluster teardown
		// behavior and so needs its own dedicated cluster. This seems somewhat leaky
		// in terms of test isolation because a downstream teardown of a cluster the
		// test doesn't actually own is only indirectly related to the test and the
		// test can't actually make any assertions. This is a fundamental difference
		// from v1 where tests own the lifecycle of the hostedcluster. In v2 tests are
		// explicitly decoupled from the hostedcluster lifecycle and so have no inherent
		// ability to make any such lifecycle assertions. This could mean that either
		// the assertion itself needs to change to decouple it from lifecycle somehow,
		// or there's a gap in the v2 framework for this sort of use case...
		{
			Variant: "karpenter",
			ExtraArgs: append(extraArgs, []string{
				// Enables Karpenter-based node provisioning (AutoNode)
				"--auto-node",
				// Required for karpenter to reach the hosted cluster API server from the mgmt cluster
				"--endpoint-access=PublicAndPrivate",
			}...),
		},
	}
}

func (a *AWSPlatformConfig) CreateArgs() []string {
	args := []string{
		"--region=" + a.region,
		"--zones=" + strings.Join(a.zones, ","),
		"--root-volume-size=64",
		"--root-volume-type=gp3",
		"--pods-labels=hypershift-e2e-test-label=test",
		"--toleration=key=hypershift-e2e-test-toleration,operator=Equal,value=true,effect=NoSchedule",
		"--annotations=hypershift.openshift.io/cleanup-cloud-resources=true",
		"--annotations=hypershift.openshift.io/skip-release-image-validation=true",
		"--feature-set=TechPreviewNoUpgrade",
	}
	for _, tag := range a.additionalTags {
		args = append(args, "--additional-tags="+tag)
	}
	return args
}

func (a *AWSPlatformConfig) PreCreate(ctx context.Context, cl crclient.WithWatch, namespace string) error {
	return nil
}

func (a *AWSPlatformConfig) PostCreate(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error {
	return nil
}

func (a *AWSPlatformConfig) PostAvailable(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error {
	return nil
}

func (a *AWSPlatformConfig) PostVersionRollout(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error {
	return nil
}

func (a *AWSPlatformConfig) TestMatrix(releaseImage string) TestMatrix {
	return TestMatrix{
		Parallel: []TestGroup{
			{
				Name:        "public",
				Variant:     "public",
				LabelFilter: "!lifecycle || hosted-cluster-aws",
				JUnitFile:   "junit_public.xml",
			},
			{
				Name:        "karpenter",
				Variant:     "karpenter",
				LabelFilter: "karpenter",
				JUnitFile:   "junit_karpenter.xml",
			},
		},
	}
}

func (a *AWSPlatformConfig) SetupTestEnv(sharedDir string) {}

func (a *AWSPlatformConfig) DestroyArgs() []string {
	baseDomain := envOrDefault("HYPERSHIFT_BASE_DOMAIN", a.DefaultBaseDomain())
	return []string{
		"--region=" + a.region,
		"--base-domain=" + baseDomain,
	}
}
