//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/pkg/manifests"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/util/wait"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
	"github.com/onsi/ginkgo/v2/types"
)

func prepareFailureConditionVersions(tc *internal.TestContext, provider releaseinfo.Provider) error {
	ctx, cancel := context.WithTimeout(tc.Context, 30*time.Second)
	defer cancel()
	previous := internal.GetEnvVarValue("E2E_PREVIOUS_RELEASE_IMAGE")
	latest := internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE")
	resetFailureConditionPhase(tc)
	if err := errors.Join(validateFailureConditionReleaseOverride(), validateFailureConditionUpgradeTarget()); err != nil {
		return err
	}
	if tc.ConditionDiagnosticReleaseVersions == nil {
		tc.ConditionDiagnosticReleaseVersions = map[string]semver.Version{}
	}
	if tc.ConditionDiagnosticReleaseErrors == nil {
		tc.ConditionDiagnosticReleaseErrors = map[string]error{}
	}
	var errs []error
	for _, image := range []string{previous, latest} {
		if image == "" {
			continue
		}
		if _, cached := tc.ConditionDiagnosticReleaseVersions[image]; cached {
			continue
		}
		version, err := resolveFailureConditionVersion(ctx, tc, image, provider)
		if err != nil {
			tc.ConditionDiagnosticReleaseErrors[image] = err
			errs = append(errs, err)
			continue
		}
		tc.ConditionDiagnosticReleaseVersions[image] = version
		delete(tc.ConditionDiagnosticReleaseErrors, image)
	}
	_, err := failureConditionVersion(tc)
	return errors.Join(append(errs, err)...)
}

func failureConditionInitialReleaseImage() string {
	if override := internal.GetEnvVarValue("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE"); override != "" {
		return override
	}
	if previous := internal.GetEnvVarValue("E2E_PREVIOUS_RELEASE_IMAGE"); previous != "" {
		return previous
	}
	return internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE")
}

func validateFailureConditionReleaseOverride() error {
	override := internal.GetEnvVarValue("E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE")
	if override != "" && (internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE") == "" || override != internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE")) {
		return fmt.Errorf("unsupported E2E_CONDITION_DIAGNOSTIC_RELEASE_IMAGE: must equal configured E2E_LATEST_RELEASE_IMAGE")
	}
	return nil
}

func resetFailureConditionPhase(tc *internal.TestContext) {
	tc.ConditionDiagnosticUpgradeTarget = ""
	tc.ConditionDiagnosticReleaseImage = failureConditionInitialReleaseImage()
	if validateFailureConditionUpgradeTarget() == nil {
		tc.ConditionDiagnosticUpgradeTarget = internal.GetEnvVarValue("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET")
	}
}

func validateFailureConditionUpgradeTarget() error {
	target := internal.GetEnvVarValue("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET")
	if target != "" && (internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE") == "" || target != internal.GetEnvVarValue("E2E_LATEST_RELEASE_IMAGE")) {
		return fmt.Errorf("unsupported E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET: must equal configured E2E_LATEST_RELEASE_IMAGE")
	}
	return nil
}

func resolveFailureConditionVersion(ctx context.Context, tc *internal.TestContext, image string, provider releaseinfo.Provider) (semver.Version, error) {
	if err := ctx.Err(); err != nil {
		return semver.Version{}, err
	}
	hc := &hyperv1.HostedCluster{}
	if err := tc.MgmtClient.Get(ctx, crclient.ObjectKey{Namespace: tc.ClusterNamespace, Name: tc.ClusterName}, hc); err != nil {
		return semver.Version{}, fmt.Errorf("read cluster pull-secret reference for independent suite release: %w", err)
	}
	pullSecret := &corev1.Secret{}
	if err := tc.MgmtClient.Get(ctx, crclient.ObjectKey{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name}, pullSecret); err != nil {
		return semver.Version{}, fmt.Errorf("read pull secret for independent suite release: %w", err)
	}
	if len(pullSecret.Data[corev1.DockerConfigJsonKey]) == 0 {
		return semver.Version{}, fmt.Errorf("independent suite release pull secret has no docker config")
	}
	payload, err := provider.Lookup(ctx, image, pullSecret.Data[corev1.DockerConfigJsonKey])
	if err != nil {
		return semver.Version{}, fmt.Errorf("resolve independent suite release %s: %w", image, err)
	}
	if err := ctx.Err(); err != nil {
		return semver.Version{}, err
	}
	if payload == nil || payload.ImageStream == nil {
		return semver.Version{}, fmt.Errorf("independent suite release %s has no image stream metadata", image)
	}
	version, err := semver.Parse(payload.Version())
	if err != nil {
		return semver.Version{}, fmt.Errorf("independent suite release %s metadata version: %w", image, err)
	}
	version.Patch = 0
	version.Pre = nil
	version.Build = nil
	return version, nil
}

func reportFailureConditions(tc *internal.TestContext, report types.SpecReport) error {
	if !report.Failed() && !internal.IsInformingFailureSkip(report) {
		return nil
	}
	if tc == nil || tc.Context == nil || tc.MgmtClient == nil || tc.ClusterName == "" || tc.ClusterNamespace == "" {
		return fmt.Errorf("hosted cluster context unavailable for failure condition diagnostic")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), 2*time.Second)
	defer cancel()
	return reconcileFailureConditions(ctx, tc)
}

func reconcileFailureConditions(ctx context.Context, tc *internal.TestContext) error {
	if err := healthConfiguredNodePoolIntent(tc.ExpectedNodePools); err != nil {
		return fmt.Errorf("failure condition diagnostic requires configured worker intent: %w", err)
	}
	version, err := failureConditionVersion(tc)
	if err != nil {
		return err
	}
	hasWorkers := false
	for _, replicas := range tc.ExpectedNodePools {
		if replicas > 0 {
			hasWorkers = true
		}
	}
	var lastErr error
	controlPlaneVersionReady := false
	err = wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, func(ctx context.Context) (bool, error) {
		hc := &hyperv1.HostedCluster{}
		if err := tc.MgmtClient.Get(ctx, crclient.ObjectKey{Namespace: tc.ClusterNamespace, Name: tc.ClusterName}, hc); err != nil {
			lastErr = fmt.Errorf("fetch failed cluster %s/%s: %w", tc.ClusterNamespace, tc.ClusterName, err)
			return false, ctx.Err()
		}
		var readinessErr error
		if !version.LT(semver.MustParse("4.22.0")) && !controlPlaneVersionReady {
			controlPlaneVersionReady, readinessErr = failureControlPlaneVersionReady(ctx, tc, hc)
		}
		lastErr = errors.Join(readinessErr, failureConditionMismatch(hc, hasWorkers, version, tc.ConditionDiagnosticUpgradeTarget != "", controlPlaneVersionReady))
		return lastErr == nil, nil
	})
	if err != nil {
		return errors.Join(err, lastErr)
	}
	return nil
}

func failureConditionVersion(tc *internal.TestContext) (semver.Version, error) {
	if err := errors.Join(validateFailureConditionReleaseOverride(), validateFailureConditionUpgradeTarget()); err != nil {
		return semver.Version{}, err
	}
	image := tc.ConditionDiagnosticReleaseImage
	if image == "" {
		return semver.Version{}, fmt.Errorf("failure condition diagnostic lacks independent suite release input (E2E_PREVIOUS_RELEASE_IMAGE or E2E_LATEST_RELEASE_IMAGE)")
	}
	version, available := tc.ConditionDiagnosticReleaseVersions[image]
	if !available {
		return semver.Version{}, errors.Join(fmt.Errorf("failure condition diagnostic lacks resolved independent suite release %s", image), tc.ConditionDiagnosticReleaseErrors[image])
	}
	return version, nil
}

func failureControlPlaneVersionReady(ctx context.Context, tc *internal.TestContext, hc *hyperv1.HostedCluster) (bool, error) {
	target := tc.ConditionDiagnosticUpgradeTarget
	if target == "" {
		return true, nil
	}
	if hc.Spec.Release.Image != target {
		return false, nil
	}
	cpc := &hyperv1.ControlPlaneComponent{}
	if err := tc.MgmtClient.Get(ctx, crclient.ObjectKey{Namespace: manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name), Name: "control-plane-operator"}, cpc); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("read CPO rollout for failure condition diagnostic: %w", err)
	}
	return meta.IsStatusConditionTrue(cpc.Status.Conditions, string(hyperv1.ControlPlaneComponentRolloutComplete)), nil
}

func failureConditionMismatch(hc *hyperv1.HostedCluster, hasWorkers bool, version semver.Version, upgrade, enforceControlPlaneVersion bool) error {
	expected := healthExpectedConditions(hc, hasWorkers, version)
	if upgrade {
		delete(expected, hyperv1.ControlPlaneConnectionAvailable)
		delete(expected, hyperv1.ConfigOperatorReconciliationSucceeded)
	}
	var mismatches []string
	for conditionType, status := range expected {
		condition := meta.FindStatusCondition(hc.Status.Conditions, string(conditionType))
		if condition == nil {
			mismatches = append(mismatches, fmt.Sprintf("%s absent, expected %s", conditionType, status))
		} else if condition.Status != status {
			mismatches = append(mismatches, fmt.Sprintf("%s=%s, expected %s (%s: %s)", conditionType, condition.Status, status, condition.Reason, condition.Message))
		}
	}
	if enforceControlPlaneVersion && !version.LT(semver.MustParse("4.22.0")) {
		cpv := hc.Status.ControlPlaneVersion
		if cpv.Desired.Image == "" || len(cpv.History) == 0 || cpv.History[0].State != configv1.CompletedUpdate {
			mismatches = append(mismatches, "controlPlaneVersion must have a desired image and latest Completed history")
		}
	}
	if len(mismatches) == 0 {
		return nil
	}
	sort.Strings(mismatches)
	return fmt.Errorf("HostedCluster %s/%s unexpected conditions: %s", hc.Namespace, hc.Name, strings.Join(mismatches, "; "))
}
