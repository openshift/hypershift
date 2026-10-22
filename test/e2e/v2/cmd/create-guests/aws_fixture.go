//go:build e2ev2

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	cmdutil "github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

func createAWSFixture(ctx context.Context, cfg envConfig, spec namedSpec) error {
	region, err := awsFixtureRegion(spec)
	if err != nil {
		return err
	}
	if spec.Variant == "custom-config" && os.Getenv("E2E_AWS_KMS_KEY_ALIAS") != "" {
		arn, err := lifecycle.ResolveEtcdKMSKeyARN(ctx, os.Getenv("AWS_GUEST_INFRA_CREDENTIALS_FILE"), region, os.Getenv("E2E_AWS_KMS_KEY_ALIAS"))
		if err != nil {
			return err
		}
		spec.CreateArgs = append(append([]string{}, spec.CreateArgs...), "--kms-key-arn="+arn)
	}
	image := spec.ReleaseImage
	if image == "" {
		image = cfg.releaseImage
	}
	pullSecret, err := os.ReadFile(cfg.pullSecret)
	if err != nil {
		return fmt.Errorf("reading release lookup pull secret: %w", err)
	}
	provider := &releaseinfo.RegistryClientProvider{}
	release, err := provider.Lookup(ctx, image, pullSecret)
	if err != nil {
		return fmt.Errorf("resolving AWS fixture release: %w", err)
	}
	version, err := semver.Parse(release.Version())
	if err != nil {
		return fmt.Errorf("parsing AWS fixture release version: %w", err)
	}
	directory, err := os.MkdirTemp("", "hypershift-aws-fixture-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "resources.yaml")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	args := append(buildCreateArgs(cfg, spec), "--render", "--render-sensitive", "--render-into="+path)
	command := exec.CommandContext(ctx, cfg.hypershiftBinary, args...)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("rendering AWS fixture: %w", err)
	}
	rendered, err := os.Open(path)
	if err != nil {
		return err
	}
	defer rendered.Close()
	objects, err := readAWSFixture(rendered, spec, version)
	if err != nil {
		return err
	}
	if err := validateSharedOIDCFixture(cfg, objects); err != nil {
		return err
	}
	if err := recordAWSExpectedNodePools(cfg.namespace, spec, objects); err != nil {
		return err
	}
	client, err := newMgmtClient()
	if err != nil {
		return err
	}
	return applyAWSFixture(ctx, client, spec.name, objects)
}

func awsFixtureRegion(spec namedSpec) (string, error) {
	region, err := effectiveAWSRegion(spec.CreateArgs)
	if err != nil {
		return "", fmt.Errorf("invalid AWS fixture create region: %w", err)
	}
	destroyRegion, err := effectiveAWSRegion(spec.DestroyArgs)
	if err != nil {
		return "", fmt.Errorf("invalid AWS fixture destroy region: %w", err)
	}
	if region != destroyRegion {
		return "", fmt.Errorf("AWS fixture create region %q differs from manifest destroy region %q; set HYPERSHIFT_AWS_REGION instead of overriding the region through EXTRA_ARGS", region, destroyRegion)
	}
	return region, nil
}

func effectiveAWSRegion(arguments []string) (string, error) {
	region := ""
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			break
		}
		switch {
		case argument == "--region":
			index++
			if index == len(arguments) {
				return "", fmt.Errorf("--region requires a value")
			}
			region = arguments[index]
		case strings.HasPrefix(argument, "--region="):
			region = strings.TrimPrefix(argument, "--region=")
		default:
			continue
		}
		if region == "" || strings.HasPrefix(region, "-") || strings.ContainsAny(region, " \t\r\n") {
			return "", fmt.Errorf("--region requires a nonempty unambiguous value")
		}
	}
	if region == "" {
		return "", fmt.Errorf("--region is required")
	}
	return region, nil
}

func readAWSFixture(reader io.Reader, spec namedSpec, version semver.Version) ([]*unstructured.Unstructured, error) {
	version.Patch, version.Pre, version.Build = 0, nil, nil
	decoder := k8syaml.NewYAMLOrJSONDecoder(reader, 4096)
	var objects []*unstructured.Unstructured
	clusters := 0
	for {
		object := &unstructured.Unstructured{}
		if err := decoder.Decode(object); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decoding rendered AWS fixture: %w", err)
		}
		if len(object.Object) == 0 {
			continue
		}
		if object.GetKind() == "HostedCluster" {
			clusters++
			if object.GetName() != spec.name {
				return nil, fmt.Errorf("rendered HostedCluster identity does not match fixture")
			}
			cluster := &hyperv1.HostedCluster{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, cluster); err != nil {
				return nil, fmt.Errorf("decoding rendered HostedCluster: %w", err)
			}
			configureAWSFixture(cluster, spec.Variant, version)
			converted, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cluster)
			if err != nil {
				return nil, err
			}
			object.Object = converted
		}
		if object.GetKind() == "NodePool" && version.LT(semver.MustParse("4.17.0")) {
			unstructured.RemoveNestedField(object.Object, "spec", "nodeVolumeDetachTimeout")
		}
		objects = append(objects, object)
	}
	if clusters != 1 {
		return nil, fmt.Errorf("rendered AWS fixture must contain exactly one HostedCluster")
	}
	return objects, nil
}

func configureAWSFixture(cluster *hyperv1.HostedCluster, variant string, version semver.Version) {
	version.Patch, version.Pre, version.Build = 0, nil, nil
	if cluster.Spec.Configuration == nil {
		cluster.Spec.Configuration = &hyperv1.ClusterConfiguration{}
	}
	if cluster.Spec.Configuration.Ingress == nil {
		cluster.Spec.Configuration.Ingress = &configv1.IngressSpec{}
	}
	cluster.Spec.Configuration.Ingress.LoadBalancer.Platform.Type = configv1.AWSPlatformType
	if cluster.Spec.Configuration.Ingress.LoadBalancer.Platform.AWS == nil {
		cluster.Spec.Configuration.Ingress.LoadBalancer.Platform.AWS = &configv1.AWSIngressSpec{}
	}
	cluster.Spec.Configuration.Ingress.LoadBalancer.Platform.AWS.Type = configv1.NLB
	if version.LT(semver.MustParse("4.19.0")) {
		delete(cluster.Spec.Labels, "hypershift-e2e-test-label")
	}
	if version.LT(semver.MustParse("4.15.0")) && (variant == "private" || variant == "private-route") {
		delete(cluster.Annotations, "hypershift.openshift.io/cleanup-cloud-resources")
	}
	if variant == "public" && version.GTE(semver.MustParse("4.21.0")) {
		if cluster.Spec.OperatorConfiguration == nil {
			cluster.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{}
		}
		if cluster.Spec.OperatorConfiguration.IngressOperator == nil {
			cluster.Spec.OperatorConfiguration.IngressOperator = &hyperv1.IngressOperatorSpec{}
		}
		cluster.Spec.OperatorConfiguration.IngressOperator.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
			Type: operatorv1.LoadBalancerServiceStrategyType, LoadBalancer: &operatorv1.LoadBalancerStrategy{Scope: operatorv1.InternalLoadBalancer},
		}
	}
	if version.LT(semver.MustParse("4.18.0")) && cluster.Spec.Configuration != nil && cluster.Spec.Configuration.FeatureGate != nil && cluster.Spec.Configuration.FeatureGate.FeatureSet == configv1.TechPreviewNoUpgrade {
		cluster.Spec.Configuration.FeatureGate = nil
	}
	if variant != "custom-config" {
		return
	}
	if cluster.Spec.Configuration.Image == nil {
		cluster.Spec.Configuration.Image = &configv1.ImageSpec{}
	}
	cluster.Spec.Configuration.Image.RegistrySources.BlockedRegistries = []string{"badregistry.io"}
	if version.LT(semver.MustParse("4.18.0")) {
		cluster.Spec.Capabilities = nil
		return
	}
	disabled := []hyperv1.OptionalCapability{hyperv1.ImageRegistryCapability, hyperv1.OpenShiftSamplesCapability, hyperv1.InsightsCapability, hyperv1.NodeTuningCapability}
	if version.GTE(semver.MustParse("4.20.0")) {
		disabled = append(disabled, hyperv1.ConsoleCapability, hyperv1.IngressCapability)
	}
	cluster.Spec.Capabilities = &hyperv1.Capabilities{Disabled: disabled}
}

func applyAWSFixture(ctx context.Context, client crclient.Client, infraID string, objects []*unstructured.Unstructured) error {
	for _, object := range objects {
		labels := object.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[cmdutil.AutoInfraLabelName] = infraID
		object.SetLabels(labels)
		var err error
		if object.GetKind() == "HostedCluster" {
			err = client.Create(ctx, object)
		} else {
			err = client.Patch(ctx, object, crclient.Apply, crclient.ForceOwnership, crclient.FieldOwner("hypershift-cli"))
		}
		if err != nil {
			return fmt.Errorf("applying rendered %s %s/%s: %w", object.GetKind(), object.GetNamespace(), object.GetName(), err)
		}
	}
	return nil
}
