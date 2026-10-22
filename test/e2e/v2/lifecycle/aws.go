//go:build e2ev2

package lifecycle

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	supportawsutil "github.com/openshift/hypershift/support/awsutil"

	"k8s.io/client-go/util/retry"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type AWSPlatformConfig struct {
	region         string
	zones          []string
	additionalTags []string
	sharedDir      string
	externalOIDC   externalOIDCSetup
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
	}

	log.Printf("AWS platform config: region=%s, zones=%v, additionalTags=%v", cfg.region, cfg.zones, cfg.additionalTags)
	return cfg
}

func (a *AWSPlatformConfig) Name() string { return "aws" }

func (a *AWSPlatformConfig) DefaultBaseDomain() string {
	return "ci.hypershift.devcluster.openshift.com"
}

func (a *AWSPlatformConfig) ClusterSpecs(releaseImage, n1Image string) []ClusterSpec {
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
	zeroInitialReplicas := 0
	publicArgs := clusterArgs("--public-only", "--feature-set=TechPreviewNoUpgrade")
	cpoLatest, cpoPrevious := controlplaneoperatoroverrides.LatestOverrideTestReleases("aws")
	var publicReplicas *int
	if len(a.zones) >= 3 {
		publicArgs = append(publicArgs, "--infra-availability-policy=HighlyAvailable")
		publicReplicas = &oneInitialReplica
	}
	return []ClusterSpec{
		{
			Variant:                 "native-etcd-recovery",
			DestroyArgs:             destroyArgs,
			InitialNodePoolReplicas: &zeroInitialReplicas,
			CreateArgs: clusterArgs("--public-only", "--shared-role=false", "--node-pool-replicas=0",
				"--control-plane-availability-policy=HighlyAvailable", "--annotations=hypershift-e2e-native-recovery=native-etcd-recovery"),
		},
		{
			Variant:                 "malformed-pull-secret-recovery",
			DestroyArgs:             destroyArgs,
			InitialNodePoolReplicas: &zeroInitialReplicas,
			CreateArgs: clusterArgs("--public-only", "--shared-role=false", "--node-pool-replicas=0",
				"--control-plane-availability-policy=HighlyAvailable", "--annotations=hypershift-e2e-native-recovery=malformed-pull-secret-recovery"),
		},
		{
			Variant: "cpo-override-latest", DestroyArgs: destroyArgs, ReleaseImage: cpoLatest,
			InitialNodePoolReplicas: &oneInitialReplica,
			CreateArgs:              clusterArgs("--public-only", "--shared-role=false"),
		},
		{
			Variant: "cpo-override-previous", DestroyArgs: destroyArgs, ReleaseImage: cpoPrevious,
			InitialNodePoolReplicas: &oneInitialReplica,
			CreateArgs:              clusterArgs("--public-only", "--shared-role=false"),
		},
		{
			Variant:                 "public",
			DestroyArgs:             destroyArgs,
			InitialNodePoolReplicas: publicReplicas,
			CreateArgs:              publicArgs,
		},
		{
			Variant:      "upgrade",
			DestroyArgs:  destroyArgs,
			ReleaseImage: n1Image,
			CreateArgs: clusterArgs(
				"--control-plane-availability-policy=HighlyAvailable",
				"--shared-role=false",
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
				"--additional-tags=red-hat-clustertype=rosa",
				"--additional-tags=red-hat-managed=true",
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
				"--additional-tags=red-hat-clustertype=rosa",
				"--additional-tags=red-hat-managed=true",
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
			Variant:                 "guest-olm",
			DestroyArgs:             destroyArgs,
			InitialNodePoolReplicas: &oneInitialReplica,
			CreateArgs:              clusterArgs("--public-only", "--olm-catalog-placement=Guest"),
		},
		{
			Variant:                 "trust-bundle",
			DestroyArgs:             destroyArgs,
			InitialNodePoolReplicas: &zeroInitialReplicas,
			CreateArgs:              clusterArgs("--public-only", "--shared-role=false"),
		},
		{
			Variant:                 "nodepool-dispatch",
			DestroyArgs:             destroyArgs,
			InitialNodePoolReplicas: &zeroInitialReplicas,
			CreateArgs:              clusterArgs("--public-only", "--shared-role=false"),
		},
		{
			Variant:     "private",
			DestroyArgs: destroyArgs,
			CreateArgs:  clusterArgs("--endpoint-access=Private", "--external-dns-domain=", "--control-plane-availability-policy=SingleReplica"),
		},
		{
			Variant:     "private-route",
			DestroyArgs: destroyArgs,
			CreateArgs:  clusterArgs("--endpoint-access=Private", "--control-plane-availability-policy=SingleReplica"),
		},
		{
			Variant:     "proxy",
			DestroyArgs: destroyArgs,
			CreateArgs:  clusterArgs("--enable-proxy", "--control-plane-availability-policy=SingleReplica"),
		},
		{
			Variant:                 "break-glass-ha",
			DestroyArgs:             destroyArgs,
			InitialNodePoolReplicas: &zeroInitialReplicas,
			CreateArgs:              clusterArgs("--public-only", "--control-plane-availability-policy=HighlyAvailable"),
		},
		{
			Variant:     "custom-config",
			DestroyArgs: destroyArgs,
			CreateArgs: clusterArgs("--public-only", "--disable-cluster-capabilities=ImageRegistry,openshift-samples,Insights,Console,NodeTuning,Ingress",
				"--kms-key-arn="+os.Getenv("E2E_AWS_KMS_KEY_ARN")),
		},
		{
			Variant:      "upgrade-pki-disabled",
			DestroyArgs:  destroyArgs,
			ReleaseImage: n1Image,
			CreateArgs:   clusterArgs("--control-plane-availability-policy=HighlyAvailable", "--shared-role=false"),
		},
		{
			Variant:                 "cilium",
			DestroyArgs:             destroyArgs,
			InitialNodePoolReplicas: &oneInitialReplica,
			CreateArgs:              clusterArgs("--public-only", "--network-type=Other", "--cluster-cidr=10.132.0.0/14"),
		},
		{
			Variant:      "karpenter-upgrade-pki-disabled",
			DestroyArgs:  destroyArgs,
			ReleaseImage: n1Image,
			CreateArgs: clusterArgs("--auto-node", "--endpoint-access=PublicAndPrivate", "--control-plane-availability-policy=HighlyAvailable",
				"--additional-tags=red-hat-clustertype=rosa", "--additional-tags=red-hat-managed=true"),
		},
	}
}

func (a *AWSPlatformConfig) PreCreate(ctx context.Context, cl crclient.WithWatch, namespace string) error {
	return a.externalOIDC.preCreate(ctx, cl)
}

func (a *AWSPlatformConfig) PostCreate(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error {
	return nil
}

func (a *AWSPlatformConfig) PostAvailable(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error {
	name, selected := clusterNames["cilium"]
	if !selected {
		return nil
	}
	cluster := &hyperv1.HostedCluster{}
	if err := cl.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: name}, cluster); err != nil {
		return err
	}
	pools := &hyperv1.NodePoolList{}
	if err := cl.List(ctx, pools, crclient.InNamespace(namespace)); err != nil {
		return err
	}
	var replicas int32
	for _, pool := range pools.Items {
		if pool.Spec.ClusterName == name && pool.Spec.Replicas != nil {
			replicas += *pool.Spec.Replicas
		}
	}
	return installCilium(ctx, cl, cluster, replicas)
}

func (a *AWSPlatformConfig) PostVersionRollout(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error {
	if err := a.externalOIDC.postVersionRollout(ctx, cl, namespace, clusterNames); err != nil {
		return err
	}
	for _, variant := range []string{"upgrade-pki-disabled", "karpenter-upgrade-pki-disabled"} {
		name, selected := clusterNames[variant]
		if !selected {
			continue
		}
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			cluster := &hyperv1.HostedCluster{}
			if err := cl.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: name}, cluster); err != nil {
				return fmt.Errorf("getting PKI-disabled upgrade HostedCluster %s/%s: %w", namespace, name, err)
			}
			original := cluster.DeepCopy()
			if cluster.Annotations == nil {
				cluster.Annotations = map[string]string{}
			}
			cluster.Annotations[hyperv1.DisablePKIReconciliationAnnotation] = "true"
			return cl.Patch(ctx, cluster, crclient.MergeFrom(original))
		}); err != nil {
			return err
		}
	}
	return nil
}

func (a *AWSPlatformConfig) DefaultTestPlan() TestPlan {
	return TestPlan{
		Name:       "aws-full",
		Platform:   "aws",
		TestMatrix: a.TestMatrix(),
	}
}

const awsRootAfterSuccessLabels = "(hosted-cluster-health || control-plane-workloads || hosted-cluster-compliance || hosted-cluster-security || hosted-cluster-ingress || hosted-cluster-node-communication || hosted-cluster-metrics || aws-control-plane-customization) && !hosted-cluster-health-configured-workers"
const awsRootBeforeLabels = awsRootAfterSuccessLabels + " && !hosted-cluster-ingress"

func (a *AWSPlatformConfig) TestMatrix() TestMatrix {
	return TestMatrix{
		Sequential: []SequentialGroup{
			{
				Name: "managed-dns",
				Steps: []TestGroup{
					{Name: "managed-dns-configured-workers", Variant: "managed-dns", LabelFilter: "hosted-cluster-health-configured-workers"},
					{Name: "managed-dns-common-before", Variant: "managed-dns", LabelFilter: awsRootBeforeLabels},
					{Name: "managed-dns", Variant: "managed-dns", LabelFilter: "hosted-cluster-managed-dns"},
					{Name: "managed-dns-common-after", Variant: "managed-dns", LabelFilter: awsRootAfterSuccessLabels},
				},
			},
			{
				Name: "karpenter",
				Steps: []TestGroup{
					{Name: "karpenter-configured-workers", Variant: "karpenter", LabelFilter: "hosted-cluster-health-configured-workers"},
					{Name: "karpenter-common-before", Variant: "karpenter", LabelFilter: awsRootBeforeLabels},
					{Name: "karpenter", Variant: "karpenter", LabelFilter: "karpenter"},
					{Name: "karpenter-common-after", Variant: "karpenter", LabelFilter: awsRootAfterSuccessLabels},
				},
			},
			// TODO: It might be possible to decompose the karpenter upgrade test
			// into pre and post upgrade specs which communicate with a well defined
			// IPC protocol, like the pre step serializing observations which the
			// post step can use. Then we can compose the regular upgrade test here
			// instead of duplicating upgrade logic inside the karpenter test.
			{
				Name: "karpenter-upgrade",
				Steps: []TestGroup{
					{Name: "karpenter-upgrade-configured-workers", Variant: "karpenter-upgrade", LabelFilter: "hosted-cluster-health-configured-workers"},
					{Name: "karpenter-upgrade-common-before", Variant: "karpenter-upgrade", LabelFilter: awsRootBeforeLabels},
					{Name: "karpenter-upgrade", Variant: "karpenter-upgrade", LabelFilter: "karpenter-upgrade"},
					{Name: "karpenter-upgrade-common-after", Variant: "karpenter-upgrade", LabelFilter: awsRootAfterSuccessLabels},
				},
			},
			{
				Name: "public",
				Steps: []TestGroup{
					{Name: "public-configured-workers", Variant: "public", LabelFilter: "hosted-cluster-health-configured-workers"},
					{Name: "public-common-before", Variant: "public", LabelFilter: awsRootBeforeLabels},
					{
						Name:    "public",
						Variant: "public",
						LabelFilter: "hosted-cluster-aws || hosted-cluster-compliance || hosted-cluster-node-communication || " +
							"hosted-cluster-cpo || nodepool-arm64 || secret-encryption || control-plane-workloads || " +
							"hosted-cluster-security || nodepool-osimagestream || hosted-cluster-ingress || hosted-cluster-dns || " +
							"hosted-cluster-health || hosted-cluster-metrics || hosted-cluster-image-registry || hosted-cluster-api-validation || aws-control-plane-customization",
					},
					{
						Name:    "public-nodepool-rollouts",
						Variant: "public",
						LabelFilter: "nodepool-vm-size-rollout || nodepool-replace-version-upgrade || nodepool-inplace-version-upgrade || " +
							"nodepool-n1-release || nodepool-n2-release || nodepool-auto-repair || nodepool-osimagestream-upgrade || " +
							"nodepool-nto-replace-rollout || nodepool-nto-inplace-rollout || nodepool-performance-profile || nodepool-mirror-config",
					},
					{
						Name:    "public-native-lifecycle",
						Variant: "public",
						LabelFilter: "hosted-cluster-break-glass || hosted-cluster-oauth || hosted-cluster-network-configuration || " +
							"nodepool-aws-root-volume || nodepool-aws-day2-tags || hosted-cluster-metrics-lifecycle",
					},
					{Name: "public-common-after", Variant: "public", LabelFilter: awsRootAfterSuccessLabels},
				},
			},
			{
				Name: "autoscaling",
				Steps: []TestGroup{
					{Name: "autoscaling-configured-workers", Variant: "autoscaling", LabelFilter: "hosted-cluster-health-configured-workers"},
					{Name: "autoscaling-common-before", Variant: "autoscaling", LabelFilter: awsRootBeforeLabels},
					{
						Name:        "autoscaling-nodepool-machineconfig",
						Variant:     "autoscaling",
						LabelFilter: "nodepool-machineconfig-rollout",
					},
					{
						Name:        "autoscaling-balancing",
						Variant:     "autoscaling",
						LabelFilter: "nodepool-autoscaling-balancing || nodepool-autoscaling-scale-from-zero || nodepool-autoscaling-pause",
					},
					{Name: "autoscaling-common-after", Variant: "autoscaling", LabelFilter: awsRootAfterSuccessLabels},
				},
			},
			{
				Name: "external-oidc",
				Steps: []TestGroup{
					{Name: "external-oidc-configured-workers", Variant: "external-oidc", LabelFilter: "hosted-cluster-health-configured-workers"},
					{Name: "external-oidc-common-before", Variant: "external-oidc", LabelFilter: awsRootBeforeLabels},
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
					{Name: "external-oidc-common-after", Variant: "external-oidc", LabelFilter: awsRootAfterSuccessLabels},
				},
			},
			{
				Name: "upgrade-and-chaos",
				Steps: []TestGroup{
					{Name: "upgrade-configured-workers", Variant: "upgrade", LabelFilter: "hosted-cluster-health-configured-workers"},
					{Name: "upgrade-common-before", Variant: "upgrade", LabelFilter: awsRootBeforeLabels},
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
					{Name: "upgrade-common-after", Variant: "upgrade", LabelFilter: awsRootAfterSuccessLabels},
				},
			},
		},
	}
}

func (a *AWSPlatformConfig) SetupTestEnv(sharedDir string) {
	setupExternalOIDCTestEnv(sharedDir)
}
