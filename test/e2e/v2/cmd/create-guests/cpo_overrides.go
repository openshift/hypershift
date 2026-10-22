//go:build e2ev2

package main

import (
	"context"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

func validateCPOOverrideSelection(plan *lifecycle.TestPlan, platform string, specs []lifecycle.ClusterSpec, legacyMode bool) (bool, error) {
	selected := false
	for _, spec := range specs {
		selected = selected || spec.Variant == "cpo-override-latest" || spec.Variant == "cpo-override-previous"
	}
	if !selected {
		if legacyMode && platform == "aws" {
			return false, fmt.Errorf("TEST_CPO_OVERRIDE=1 requires TEST_PLAN=contrib/aws-e2e-v2-cpo-overrides.yaml and HYPERSHIFT_PLATFORM=aws; default suites are not CPO override mode")
		}
		return false, nil
	}
	if platform != "aws" || len(specs) != 2 {
		return false, fmt.Errorf("CPO override mode requires exactly the two dedicated AWS override fixtures")
	}
	latest, previous := controlplaneoperatoroverrides.LatestOverrideTestReleases(platform)
	if latest == "" || previous == "" || latest == previous {
		return false, fmt.Errorf("the production CPO override map must provide distinct latest and previous AWS test releases")
	}
	expected := map[string]string{"cpo-override-latest": latest, "cpo-override-previous": previous}
	for _, spec := range specs {
		image, found := expected[spec.Variant]
		if !found || spec.ReleaseImage != image {
			return false, fmt.Errorf("CPO override fixture %s must use its production-mapped test release", spec.Variant)
		}
		delete(expected, spec.Variant)
	}
	if err := validateCPOOverrideGroups(plan); err != nil {
		return false, err
	}
	return true, nil
}

func validateCPOOverrideGroups(plan *lifecycle.TestPlan) error {
	if plan == nil || len(plan.TestMatrix.Parallel) != 0 || len(plan.TestMatrix.Sequential) != 2 {
		return fmt.Errorf("CPO override mode requires exactly two independent sequential validation lanes")
	}
	before, after := "", ""
	for _, lane := range (&lifecycle.AWSPlatformConfig{}).TestMatrix().Sequential {
		for _, step := range lane.Steps {
			switch step.Name {
			case "public-common-before":
				before = step.LabelFilter
			case "public-common-after":
				after = step.LabelFilter
			}
		}
	}
	if before == "" || after == "" {
		return fmt.Errorf("CPO override mode requires the existing public common-hook selections")
	}
	remaining := sets.New("cpo-override-latest", "cpo-override-previous")
	for _, lane := range plan.TestMatrix.Sequential {
		if !remaining.Has(lane.Name) || len(lane.Steps) != 4 {
			return fmt.Errorf("CPO override mode requires one four-child lane per mapped fixture")
		}
		remaining.Delete(lane.Name)
		names := []string{lane.Name + "-configured-workers", lane.Name + "-common-before", lane.Name, lane.Name + "-common-after"}
		filters := []string{"hosted-cluster-health-configured-workers", before, "hosted-cluster-cpo-required", after}
		for index, step := range lane.Steps {
			if step.Name != names[index] || step.Variant != lane.Name || step.LabelFilter != filters[index] || step.Skip != "" {
				return fmt.Errorf("CPO override lane %s must retain configured initial, public before, original required assertion and public after children without skips", lane.Name)
			}
		}
	}
	return nil
}

func preflightSelectedCPOOverrides(selected bool, preflight func() error) error {
	if !selected {
		return nil
	}
	return preflight()
}

func preflightCPOOverrides(ctx context.Context, client crclient.Client, specs []lifecycle.ClusterSpec, pullSecretFile, namespace string) error {
	if err := validateCPOOverrideOperator(ctx, client, namespace); err != nil {
		return err
	}
	pullSecret, err := os.ReadFile(pullSecretFile)
	if err != nil {
		return fmt.Errorf("reading CPO release lookup pull secret: %w", err)
	}
	return validateCPOOverrideReleases(ctx, &releaseinfo.RegistryClientProvider{}, specs, pullSecret)
}

func validateCPOOverrideReleases(ctx context.Context, provider releaseinfo.Provider, specs []lifecycle.ClusterSpec, pullSecret []byte) error {
	versions := map[string]semver.Version{}
	for _, spec := range specs {
		image, err := provider.Lookup(ctx, spec.ReleaseImage, pullSecret)
		if err != nil {
			return fmt.Errorf("resolving CPO override fixture %s: %w", spec.Variant, err)
		}
		if image == nil || image.ImageStream == nil || image.Version() == "" || controlplaneoperatoroverrides.CPOImage("aws", image.Version()) == "" {
			return fmt.Errorf("CPO override fixture %s must resolve to a release version mapped by the production AWS override map", spec.Variant)
		}
		version, err := semver.Parse(image.Version())
		if err != nil {
			return fmt.Errorf("parsing CPO override fixture %s version: %w", spec.Variant, err)
		}
		versions[spec.Variant] = version
	}
	if !versions["cpo-override-latest"].GT(versions["cpo-override-previous"]) {
		return fmt.Errorf("the mapped CPO latest payload version must be newer than the previous payload version")
	}
	return nil
}

func validateCPOOverrideOperator(ctx context.Context, client crclient.Client, namespace string) error {
	deployments := &appsv1.DeploymentList{}
	var options []crclient.ListOption
	if namespace != "" {
		options = append(options, crclient.InNamespace(namespace))
	}
	if err := client.List(ctx, deployments, options...); err != nil {
		return fmt.Errorf("discovering the installed HyperShift operator: %w", err)
	}
	var candidates []*appsv1.Deployment
	for index := range deployments.Items {
		deployment := &deployments.Items[index]
		if deployment.Name == "operator" && deployment.Spec.Template.Labels[hyperv1.OperatorComponent] == "operator" {
			candidates = append(candidates, deployment)
		}
	}
	if len(candidates) != 1 {
		return fmt.Errorf("CPO overrides require exactly one installed HyperShift operator, found %d; set E2E_HYPERSHIFT_OPERATOR_NAMESPACE to select its namespace", len(candidates))
	}
	deployment := candidates[0]
	if err := cpoOverrideOperatorReady(deployment); err != nil {
		return fmt.Errorf("HyperShift operator %s/%s: %w", deployment.Namespace, deployment.Name, err)
	}
	return nil
}

func cpoOverrideOperatorReady(deployment *appsv1.Deployment) error {
	if deployment.DeletionTimestamp != nil || deployment.Spec.Replicas == nil || *deployment.Spec.Replicas <= 0 ||
		deployment.Status.ObservedGeneration < deployment.Generation || deployment.Status.Replicas != *deployment.Spec.Replicas ||
		deployment.Status.UpdatedReplicas != *deployment.Spec.Replicas || deployment.Status.ReadyReplicas != *deployment.Spec.Replicas ||
		deployment.Status.AvailableReplicas != *deployment.Spec.Replicas || deployment.Status.UnavailableReplicas != 0 {
		return fmt.Errorf("CPO override prerequisite requires a fully observed, available operator deployment")
	}
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name != "operator" || len(container.Command) != 1 || container.Command[0] != "/usr/bin/hypershift-operator" {
			continue
		}
		for _, variable := range container.Env {
			if variable.Name == controlplaneoperatoroverrides.CPOOverridesEnvVar && variable.Value == "1" && variable.ValueFrom == nil {
				return nil
			}
		}
	}
	return fmt.Errorf("CPO override prerequisite requires an operator installed with --enable-cpo-overrides (literal ENABLE_CPO_OVERRIDES=1); this command does not mutate the operator")
}
