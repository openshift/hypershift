//go:build e2ev2

package lifecycle

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	supportawsutil "github.com/openshift/hypershift/support/awsutil"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

type AWSPlatformConfig struct {
	region         string
	zones          []string
	additionalTags []string
	sharedDir      string
}

type AWSPlatformOptions struct {
	Region    string
	Zones     string
	ProwJobId string
}

func NewAWSPlatformConfig(opts AWSPlatformOptions, sharedDir string) *AWSPlatformConfig {
	zones := strings.Split(opts.Zones, ",")

	// TODO: this is currently just to satisfy an assumption made by EnsureInfrastructureResourceTagsTest
	// and should probably be handled another way. That test assumes there is at least one pre-existing
	// non-kubernetes-namespaced tag on the infra.
	tags := []string{fmt.Sprintf("expirationDate=%s", time.Now().Add(4*time.Hour).UTC().Format(time.RFC3339))}

	if opts.ProwJobId != "" {
		tags = append(tags, supportawsutil.HypershiftProwJobIDTagKey+"="+opts.ProwJobId)
	}

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

// ClusterSpecs returns the AWS cluster variants to create for the e2e-v2 run,
// one per test group. releaseImage is the current release under test; n1Image is
// the N-1 release used as the upgrade starting point.
//
// Variants:
//   - public:           default target for non-lifecycle tests.
//   - upgrade:          starts on n1Image, upgraded to releaseImage.
//   - karpenter:        AutoNode/Karpenter node provisioning.
//   - karpenter-upgrade: karpenter, starting on n1Image.
//
// Environment inputs:
//   - EXTRA_ARGS:                       whitespace-split, appended to every variant.
//   - HYPERSHIFT_STORAGE_KMS_KEY_ALIAS: resolved to an ARN and passed as
//     --initial-storage-volumes-kms-key on the public variant (for the storage-kms test).
func (a *AWSPlatformConfig) ClusterSpecs(releaseImage, n1Image string) []ClusterSpec {
	// Parse EXTRA_ARGS from environment if provided
	var extraArgs []string
	if envArgs := os.Getenv("EXTRA_ARGS"); envArgs != "" {
		extraArgs = strings.Fields(envArgs)
	}

	// The storage-kms test runs on the public cluster and requires the cluster
	// to be created with a KMS key. HYPERSHIFT_STORAGE_KMS_KEY_ALIAS carries a
	// pre-provisioned CI KMS key alias (e.g. alias/hypershift-ci), resolved here
	// to its ARN and passed to the create command. When unset, the flag is
	// omitted and the storage-kms test is skipped.
	var publicExtraArgs []string
	publicExtraArgs = append(publicExtraArgs, extraArgs...)
	publicExtraArgs = append(publicExtraArgs,
		"--public-only",
		"--feature-set=TechPreviewNoUpgrade",
	)
	// initialKMSKeyARN and its --initial-storage-volumes-kms-key CLI flag landed
	// in 5.1; older releases neither expose the flag nor have the env var wired in
	// CI, so only inject the KMS key when the release under test is >= 5.1.
	if releaseImageAtLeast(releaseImage, e2eutil.Version51) {
		if alias := os.Getenv("HYPERSHIFT_STORAGE_KMS_KEY_ALIAS"); alias != "" {
			arn, err := a.resolveKMSKeyARN(alias)
			if err != nil {
				// The variable is set, so this job expects a KMS-encrypted cluster.
				// Failing to resolve it would silently drop coverage (the cluster
				// would be created without a key and the storage-kms test would
				// skip), so die early. This runs before create-guests provisions
				// anything, so nothing is left behind.
				panic(fmt.Sprintf("HYPERSHIFT_STORAGE_KMS_KEY_ALIAS is set but alias %q could not be resolved to an ARN: %v", alias, err))
			}
			log.Printf("Resolved storage KMS key alias %q to ARN %q", alias, arn)
			publicExtraArgs = append(publicExtraArgs, "--initial-storage-volumes-kms-key="+arn)
		}
	}

	return []ClusterSpec{
		{
			Variant:   "public",
			ExtraArgs: publicExtraArgs,
		},
		{
			Variant:      "upgrade",
			ReleaseImage: n1Image,
			ExtraArgs: append(extraArgs, []string{
				"--control-plane-availability-policy=HighlyAvailable",
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
				"--feature-set=TechPreviewNoUpgrade",
			}...),
		},
		{
			Variant:      "karpenter-upgrade",
			ReleaseImage: n1Image,
			ExtraArgs: append(extraArgs, []string{
				// Enables Karpenter-based node provisioning (AutoNode)
				"--auto-node",
				// Required for karpenter to reach the hosted cluster API server from the mgmt cluster
				"--endpoint-access=PublicAndPrivate",
				"--control-plane-availability-policy=HighlyAvailable",
			}...),
		},
	}
}

// releaseImageAtLeast reports whether the version encoded in releaseImage is at
// least minVersion, compared at y-stream (major.minor) granularity. Pre-release
// and build metadata are ignored: semver orders a pre-release BELOW its release
// (5.1.0-ec.1 < 5.1.0), so a naive comparison against a y-stream constant like
// Version51 would treat every 5.1 dev/EC build as below 5.1. An unparsable image
// is treated as "at least" so a malformed tag fails loudly downstream rather than
// silently disabling the gated behavior.
func releaseImageAtLeast(releaseImage string, minVersion semver.Version) bool {
	version := e2eutil.ExtractVersionFromReleaseImage(releaseImage)
	if version == "" {
		return true
	}
	parsed, err := semver.Parse(version)
	if err != nil {
		return true
	}
	parsed.Pre = nil
	parsed.Build = nil
	parsed.Patch = 0
	min := minVersion
	min.Pre = nil
	min.Build = nil
	min.Patch = 0
	return parsed.GTE(min)
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

func (a *AWSPlatformConfig) DefaultTestPlan() TestPlan {
	return TestPlan{
		Name:       "aws-full",
		Platform:   "aws",
		TestMatrix: a.TestMatrix(),
	}
}

func (a *AWSPlatformConfig) TestMatrix() TestMatrix {
	return TestMatrix{
		Parallel: []TestGroup{
			{
				Name:        "public",
				Variant:     "public",
				LabelFilter: "!lifecycle || hosted-cluster-aws || nodepool-osimagestream || global-pull-secret",
			},
			{
				Name:        "karpenter",
				Variant:     "karpenter",
				LabelFilter: "karpenter",
			},
			// TODO: It might be possible to decompose the karpenter upgrade test
			// into pre and post upgrade specs which communicate with a well defined
			// IPC protocol, like the pre step serializing observations which the
			// post step can use. Then we can compose the regular upgrade test here
			// instead of duplicating upgrade logic inside the karpenter test.
			{
				Name:        "karpenter-upgrade",
				Variant:     "karpenter-upgrade",
				LabelFilter: "karpenter-upgrade",
			},
		},
		Sequential: []SequentialGroup{
			{
				Name: "upgrade-and-chaos",
				Steps: []TestGroup{
					{
						Name:        "upgrade",
						Variant:     "upgrade",
						LabelFilter: "control-plane-upgrade",
					},
					{
						Name:        "post-upgrade-health",
						Variant:     "upgrade",
						LabelFilter: "hosted-cluster-health || control-plane-workloads",
					},
					{
						Name:        "control-plane-tls",
						Variant:     "upgrade",
						LabelFilter: "control-plane-pki-operator",
					},
					{
						Name:        "etcd-chaos",
						Variant:     "upgrade",
						LabelFilter: "etcd-chaos",
					},
				},
			},
		},
	}
}

func (a *AWSPlatformConfig) SetupTestEnv(sharedDir string) {}

// resolveKMSKeyARN resolves a KMS key alias (e.g. "alias/hypershift-ci") to its
// full key ARN using DescribeKey. AWS credentials are sourced from the default
// chain (AWS_SHARED_CREDENTIALS_FILE is exported by the create-guests CI step).
func (a *AWSPlatformConfig) resolveKMSKeyARN(alias string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	awsConfig := awsutil.NewSession(ctx, "e2e-storage-kms", os.Getenv("AWS_SHARED_CREDENTIALS_FILE"), "", "", a.region)
	retryer := awsutil.NewConfig()
	kmsClient := kms.NewFromConfig(*awsConfig, func(o *kms.Options) {
		o.Retryer = retryer()
	})

	out, err := kmsClient.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: aws.String(alias)})
	if err != nil {
		return "", fmt.Errorf("describing KMS key %q: %w", alias, err)
	}
	if out.KeyMetadata == nil || out.KeyMetadata.Arn == nil {
		return "", fmt.Errorf("KMS key with alias %q has no ARN", alias)
	}
	return *out.KeyMetadata.Arn, nil
}

func (a *AWSPlatformConfig) DestroyArgs() []string {
	baseDomain := envOrDefault("HYPERSHIFT_BASE_DOMAIN", a.DefaultBaseDomain())
	return []string{
		"--region=" + a.region,
		"--base-domain=" + baseDomain,
	}
}
