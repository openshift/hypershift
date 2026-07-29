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
			Variant:    "public",
			OutputFile: "cluster-name-public",
			ExtraArgs:  extraArgs,
		},
	}
}

func (a *AWSPlatformConfig) CreateArgs() []string {
	args := []string{
		"--region=" + a.region,
		"--zones=" + strings.Join(a.zones, ","),
		"--root-volume-size=64",
		"--root-volume-type=gp3",
		"--public-only",
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
				Name:        "aws-public",
				ClusterFile: "cluster-name-public",
				LabelFilter: "!lifecycle || hosted-cluster-aws",
				JUnitFile:   "junit_aws_public.xml",
			},
		},
	}
}

func (a *AWSPlatformConfig) SetupTestEnv(sharedDir string) {}

func (a *AWSPlatformConfig) DestroyArgs() []string {
	return []string{
		"--region=" + a.region,
	}
}
