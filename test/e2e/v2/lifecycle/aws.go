//go:build e2ev2

package lifecycle

import (
	"context"
	"fmt"
	"log"
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
	cfg := &AWSPlatformConfig{
		region:    opts.Region,
		sharedDir: sharedDir,
	}

	cfg.zones = strings.Split(opts.Zones, ",")

	cfg.additionalTags = []string{
		fmt.Sprintf("expirationDate=%s", time.Now().Add(4*time.Hour).UTC().Format(time.RFC3339)),
	}

	log.Printf("AWS platform config: region=%s, zones=%v", cfg.region, cfg.zones)
	return cfg
}

func (a *AWSPlatformConfig) Name() string { return "aws" }

func (a *AWSPlatformConfig) DefaultBaseDomain() string {
	return "ci.hypershift.devcluster.openshift.com"
}

func (a *AWSPlatformConfig) ClusterSpecs(releaseImage, n1Image string) []ClusterSpec {
	return []ClusterSpec{
		{
			Variant:    "public",
			OutputFile: "cluster-name-public",
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
				ClusterFile: "cluster-name-public",
				LabelFilter: "hosted-cluster-health || control-plane-workloads || hosted-cluster-metrics || hosted-cluster-image-registry",
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
