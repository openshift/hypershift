//go:build e2ev2

package lifecycle

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	supportawsutil "github.com/openshift/hypershift/support/awsutil"
	"github.com/openshift/hypershift/test/e2e/v2/util/mirrorregistry"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type AWSPlatformConfig struct {
	region         string
	zones          []string
	additionalTags []string
	sharedDir      string
	externalOIDC   externalOIDCSetup

	newMirrorRegistry func(crclient.Client, mirrorregistry.Options) (mirrorRegistry, error)
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
		externalOIDC:   externalOIDCSetup{sharedDir: sharedDir},
		newMirrorRegistry: func(cl crclient.Client, opts mirrorregistry.Options) (mirrorRegistry, error) {
			return mirrorregistry.New(cl, opts)
		},
	}

	log.Printf("AWS platform config: region=%s, zones=%v, additionalTags=%v", cfg.region, cfg.zones, cfg.additionalTags)
	return cfg
}

func (a *AWSPlatformConfig) Name() string { return "aws" }

func (a *AWSPlatformConfig) DefaultBaseDomain() string {
	return "ci.hypershift.devcluster.openshift.com"
}

// ClusterSpecs returns the AWS cluster variants to create for the e2e-v2 run,
// one per test group.
//
// Arguments:
//   - ctx:          used for cancellable setup I/O (e.g. resolving a KMS key alias to an ARN).
//   - releaseImage: pullspec of the current release under test (a tag or digest
//     reference such as quay.io/openshift-release-dev/ocp-release:4.20.0-x86_64,
//     not an OCP version string).
//   - n1Image:      pullspec of the N-1 release used as the upgrade starting point.
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
func (a *AWSPlatformConfig) ClusterSpecs(ctx context.Context, releaseImage, n1Image string) []ClusterSpec {
	baseDomain := envOrDefault("HYPERSHIFT_BASE_DOMAIN", a.DefaultBaseDomain())
	destroyArgs := []string{
		"--region=" + a.region,
		"--base-domain=" + baseDomain,
	}

	commonArgs := []string{
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
		commonArgs = append(commonArgs, "--additional-tags="+tag)
	}

	// Parse EXTRA_ARGS from environment if provided
	var extraArgs []string
	if envArgs := os.Getenv("EXTRA_ARGS"); envArgs != "" {
		extraArgs = strings.Fields(envArgs)
	}

	clusterArgs := func(args ...string) []string {
		return slices.Concat(commonArgs, extraArgs, args)
	}
	oneInitialReplica := 1

	// The storage-kms test runs on the public cluster and requires the cluster
	// to be created with a KMS key. HYPERSHIFT_STORAGE_KMS_KEY_ALIAS carries a
	// pre-provisioned CI KMS key alias (e.g. alias/hypershift-ci), resolved here
	// to its ARN and passed to the create command. The variable is only wired on
	// jobs whose release supports the --initial-storage-volumes-kms-key flag; when
	// unset the flag is omitted and the storage-kms test skips itself (it gates on
	// the running HostedCluster version via SkipIfVersionBelow).
	publicArgs := []string{
		"--public-only",
		"--feature-set=TechPreviewNoUpgrade",
	}
	if alias := os.Getenv("HYPERSHIFT_STORAGE_KMS_KEY_ALIAS"); alias != "" {
		arn, err := a.resolveKMSKeyARN(ctx, alias)
		if err != nil {
			// The variable is set, so this job expects a KMS-encrypted cluster.
			// Failing to resolve it would silently drop coverage (the cluster
			// would be created without a key and the storage-kms test would
			// skip), so die early. This runs before create-guests provisions
			// anything, so nothing is left behind.
			panic(fmt.Sprintf("HYPERSHIFT_STORAGE_KMS_KEY_ALIAS is set but alias %q could not be resolved to an ARN: %v", alias, err))
		}
		log.Printf("Resolved storage KMS key alias %q to ARN %q", alias, arn)
		publicArgs = append(publicArgs, "--initial-storage-volumes-kms-key="+arn)
	}

	return []ClusterSpec{
		{
			Variant:     "public",
			DestroyArgs: destroyArgs,
			CreateArgs:  clusterArgs(publicArgs...),
		},
		{
			Variant:      "upgrade",
			DestroyArgs:  destroyArgs,
			ReleaseImage: n1Image,
			CreateArgs: clusterArgs(
				"--control-plane-availability-policy=HighlyAvailable",
			),
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
			// Dedicated cluster for the managed ingress DNS test. managedDNS is gated
			// by the AWSManagedDNS feature gate, which only exists in TechPreviewNoUpgrade.
			Variant:     "managed-dns",
			DestroyArgs: destroyArgs,
			CreateArgs: clusterArgs(
				"--public-only",
				"--feature-set=TechPreviewNoUpgrade",
				"--managed-dns",
			),
		},
		{
			Variant:     "karpenter",
			DestroyArgs: destroyArgs,
			CreateArgs: clusterArgs(
				// Enables Karpenter-based node provisioning (AutoNode)
				"--auto-node",
				// Required for karpenter to reach the hosted cluster API server from the mgmt cluster
				"--endpoint-access=PublicAndPrivate",
				"--feature-set=TechPreviewNoUpgrade",
			),
		},
		{
			Variant:      "karpenter-upgrade",
			DestroyArgs:  destroyArgs,
			ReleaseImage: n1Image,
			CreateArgs: clusterArgs(
				// Enables Karpenter-based node provisioning (AutoNode)
				"--auto-node",
				// Required for karpenter to reach the hosted cluster API server from the mgmt cluster
				"--endpoint-access=PublicAndPrivate",
				"--control-plane-availability-policy=HighlyAvailable",
			),
		},
		{
			Variant:                 "autoscaling",
			DestroyArgs:             destroyArgs,
			InitialNodePoolReplicas: &oneInitialReplica,
			CreateArgs:              clusterArgs(),
		},
		{
			Variant:                 "external-oidc",
			DestroyArgs:             destroyArgs,
			InitialNodePoolReplicas: &oneInitialReplica,
			CreateArgs:              clusterArgs(),
		},
		{
			// Consumes the trust bundle and imageContentSources written to
			// SHARED_DIR by the mirror registry fixture in PreCreate.
			Variant:                 "image-mirroring",
			DestroyArgs:             destroyArgs,
			InitialNodePoolReplicas: &oneInitialReplica,
			CreateArgs: clusterArgs(
				"--additional-trust-bundle="+filepath.Join(a.sharedDir, "mirror-registry-ca.pem"),
				"--image-content-sources="+filepath.Join(a.sharedDir, "mirror-registry-ics.yaml"),
			),
		},
	}
}

// PreCreate deploys the external OIDC provider and the mirror registry fixture
// before any cluster is created. The image-mirroring variant reads the fixture
// artifacts from SHARED_DIR at creation time.
func (a *AWSPlatformConfig) PreCreate(ctx context.Context, cl crclient.WithWatch, namespace string) error {
	if err := a.externalOIDC.preCreate(ctx, cl); err != nil {
		return err
	}
	return a.setupImageMirroring(ctx, cl)
}

func (a *AWSPlatformConfig) setupImageMirroring(ctx context.Context, cl crclient.Client) error {
	if a.sharedDir == "" {
		return fmt.Errorf("image mirroring requires a shared directory")
	}
	// This lane copies only two small public-image mappings, not full release
	// payloads. Twenty minutes bounds the entire setup, including readiness
	// waits, even though the generic copier allows thirty minutes per Job.
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	copierImage, err := cliImage(ctx, cl)
	if err != nil {
		return err
	}
	registry, err := a.newMirrorRegistry(cl, mirrorregistry.Options{CopierImage: copierImage})
	if err != nil {
		return fmt.Errorf("initialize mirror registry: %w", err)
	}
	// The public image needs no source pull secret. Mirror both its real
	// repository and a virtual source that cannot fall back to upstream.
	result, err := registry.Setup(ctx, mirrorregistry.MirrorRequest{Images: []mirrorregistry.ImageMapping{
		{From: mirrorregistry.DefaultRegistryImage},
		{From: mirrorregistry.DefaultRegistryImage, SourceRepository: "mirror-source.hypershift-e2e.invalid/libpod/registry"},
	}})
	if err != nil {
		return fmt.Errorf("set up mirror registry: %w", err)
	}
	return writeMirrorRegistryResult(a.sharedDir, result)
}

func (a *AWSPlatformConfig) PostCreate(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error {
	return nil
}

func (a *AWSPlatformConfig) PostAvailable(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error {
	return nil
}

func (a *AWSPlatformConfig) PostVersionRollout(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error {
	return a.externalOIDC.postVersionRollout(ctx, cl, namespace, clusterNames)
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
				Name:        "managed-dns",
				Variant:     "managed-dns",
				LabelFilter: "hosted-cluster-managed-dns",
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
			{
				Name:        "image-mirroring",
				Variant:     "image-mirroring",
				LabelFilter: "image-mirroring",
			},
		},
		Sequential: []SequentialGroup{
			{
				Name: "public",
				Steps: []TestGroup{
					{
						Name:    "public",
						Variant: "public",
						LabelFilter: "hosted-cluster-aws || hosted-cluster-compliance || hosted-cluster-node-communication || " +
							"hosted-cluster-cpo || nodepool-arm64 || secret-encryption || control-plane-workloads || " +
							"hosted-cluster-security || nodepool-osimagestream || hosted-cluster-ingress || hosted-cluster-dns || " +
							"hosted-cluster-health || hosted-cluster-metrics || hosted-cluster-image-registry || storage-kms",
					},
					{
						Name:    "public-nodepool-rollouts",
						Variant: "public",
						LabelFilter: "nodepool-vm-size-rollout || nodepool-replace-version-upgrade || nodepool-inplace-version-upgrade || " +
							"nodepool-n1-release || nodepool-n2-release || nodepool-auto-repair || nodepool-osimagestream-upgrade || " +
							"nodepool-root-volume-kms || nodepool-nto-replace-rollout || nodepool-nto-inplace-rollout || " +
							"nodepool-performance-profile || nodepool-mirror-config",
					},
				},
			},
			{
				Name: "autoscaling",
				Steps: []TestGroup{
					{
						Name:        "autoscaling-nodepool-machineconfig",
						Variant:     "autoscaling",
						LabelFilter: "nodepool-machineconfig-rollout",
					},
					{
						Name:        "autoscaling-balancing",
						Variant:     "autoscaling",
						LabelFilter: "nodepool-autoscaling-balancing",
					},
				},
			},
			{
				Name: "external-oidc",
				Steps: []TestGroup{
					{
						Name:        "external-oidc",
						Variant:     "external-oidc",
						LabelFilter: "external-oidc || global-pull-secret",
					},
					{
						Name:        "external-oidc-autoscaling",
						Variant:     "external-oidc",
						LabelFilter: "nodepool-autoscaling-scale-up-down",
					},
					{
						Name:        "external-oidc-trust-bundle",
						Variant:     "external-oidc",
						LabelFilter: "nodepool-trust-bundle",
					},
				},
			},
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

func (a *AWSPlatformConfig) SetupTestEnv(sharedDir string) {
	setupExternalOIDCTestEnv(sharedDir)
}

// resolveKMSKeyARN resolves a KMS key alias (e.g. "alias/hypershift-ci") to its
// full key ARN using DescribeKey. AWS credentials are sourced from the SDK default
// credential chain (shared config/credentials files, environment variables, and
// instance/role providers).
func (a *AWSPlatformConfig) resolveKMSKeyARN(ctx context.Context, alias string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	awsConfig := awsutil.NewSession(ctx, "e2e-storage-kms", "", "", "", a.region)
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
