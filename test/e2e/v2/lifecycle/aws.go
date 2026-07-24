//go:build e2ev2

package lifecycle

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
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
	if publicName, ok := clusterNames["cluster-name-public"]; ok {
		if err := a.postCreatePublic(ctx, cl, namespace, publicName); err != nil {
			return err
		}
	}
	return nil
}

func (a *AWSPlatformConfig) postCreatePublic(ctx context.Context, cl crclient.Client, namespace, name string) error {
	hc := &hyperv1.HostedCluster{}
	if err := cl.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: name}, hc); err != nil {
		return fmt.Errorf("getting HostedCluster %s/%s: %w", namespace, name, err)
	}

	patch := crclient.MergeFrom(hc.DeepCopy())
	if hc.Spec.OperatorConfiguration == nil {
		hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{}
	}
	hc.Spec.OperatorConfiguration.IngressOperator = &hyperv1.IngressOperatorSpec{
		EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
			Type: operatorv1.LoadBalancerServiceStrategyType,
			LoadBalancer: &operatorv1.LoadBalancerStrategy{
				Scope: operatorv1.InternalLoadBalancer,
			},
		},
	}
	if err := cl.Patch(ctx, hc, patch); err != nil {
		return fmt.Errorf("patching HostedCluster %s/%s OperatorConfiguration: %w", namespace, name, err)
	}
	log.Printf("Patched public cluster %s/%s with OperatorConfiguration", namespace, name)
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
				LabelFilter: "hosted-cluster-health || control-plane-workloads || hosted-cluster-metrics || hosted-cluster-image-registry || hosted-cluster-compliance || hosted-cluster-ingress || hosted-cluster-dns || hosted-cluster-security || control-plane-pki-operator || hosted-cluster-cpo || hosted-cluster-node-communication",
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
