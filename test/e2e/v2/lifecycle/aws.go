//go:build e2ev2

package lifecycle

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	supportawsutil "github.com/openshift/hypershift/support/awsutil"
	"github.com/openshift/hypershift/test/e2e/v2/util/mirrorregistry"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type AWSPlatformConfig struct {
	region         string
	zones          []string
	additionalTags []string
	sharedDir      string
	externalOIDC   externalOIDCSetup
	imageMirroring bool

	newMirrorRegistry func(crclient.Client, mirrorregistry.Options) (mirrorRegistry, error)
}

type AWSPlatformOptions struct {
	Region    string
	Zones     string
	ProwJobId string
	// ImageMirroring enables the mirror fixture and the image-mirroring variant.
	ImageMirroring bool
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
		imageMirroring: opts.ImageMirroring,
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

func (a *AWSPlatformConfig) ClusterSpecs(releaseImage, n1Image string) []ClusterSpec {
	// Parse EXTRA_ARGS from environment if provided
	var extraArgs []string
	if envArgs := os.Getenv("EXTRA_ARGS"); envArgs != "" {
		extraArgs = strings.Fields(envArgs)
	}
	oneInitialReplica := 1
	specs := []ClusterSpec{
		{
			Variant: "public",
			ExtraArgs: append(extraArgs, []string{
				"--public-only",
				"--feature-set=TechPreviewNoUpgrade",
			}...),
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
		{
			Variant:                 "autoscaling",
			InitialNodePoolReplicas: &oneInitialReplica,
			ExtraArgs:               extraArgs,
		},
		{
			Variant:                 "external-oidc",
			InitialNodePoolReplicas: &oneInitialReplica,
			ExtraArgs:               extraArgs,
		},
	}
	if a.imageMirroring {
		specs = append(specs, ClusterSpec{
			Variant: "image-mirroring",
			ExtraArgs: append(append([]string(nil), extraArgs...),
				"--additional-trust-bundle="+filepath.Join(a.sharedDir, "mirror-registry-ca.pem"),
				"--image-content-sources="+filepath.Join(a.sharedDir, "mirror-registry-ics.yaml")),
		})
	}
	return specs
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

// PreCreate provisions the mirror whenever ImageMirroring is enabled. Enable it
// only with a TEST_PLAN selecting image-mirroring: this hook does not receive the
// plan's filtered cluster specs.
func (a *AWSPlatformConfig) PreCreate(ctx context.Context, cl crclient.WithWatch, namespace string) error {
	if err := a.externalOIDC.preCreate(ctx, cl); err != nil {
		return err
	}
	return a.setupImageMirroring(ctx, cl)
}

func (a *AWSPlatformConfig) setupImageMirroring(ctx context.Context, cl crclient.Client) error {
	if !a.imageMirroring {
		return nil
	}
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
				Name: "public",
				Steps: []TestGroup{
					{
						Name:    "public",
						Variant: "public",
						LabelFilter: "hosted-cluster-aws || hosted-cluster-compliance || hosted-cluster-node-communication || " +
							"hosted-cluster-cpo || nodepool-arm64 || secret-encryption || control-plane-workloads || " +
							"hosted-cluster-security || nodepool-osimagestream || hosted-cluster-ingress || hosted-cluster-dns || " +
							"hosted-cluster-health || hosted-cluster-metrics || hosted-cluster-image-registry",
					},
					{
						Name:    "public-nodepool-rollouts",
						Variant: "public",
						LabelFilter: "nodepool-vm-size-rollout || nodepool-replace-version-upgrade || nodepool-inplace-version-upgrade || " +
							"nodepool-n1-release || nodepool-n2-release || nodepool-auto-repair || nodepool-osimagestream-upgrade || " +
							"nodepool-nto-replace-rollout || nodepool-nto-inplace-rollout || nodepool-performance-profile || nodepool-mirror-config",
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

func (a *AWSPlatformConfig) DestroyArgs() []string {
	baseDomain := envOrDefault("HYPERSHIFT_BASE_DOMAIN", a.DefaultBaseDomain())
	return []string{
		"--region=" + a.region,
		"--base-domain=" + baseDomain,
	}
}
