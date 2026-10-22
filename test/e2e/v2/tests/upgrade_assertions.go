//go:build e2ev2

package tests

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

// ExpectHostedClusterUpgradeToComplete waits for the HostedCluster's control
// plane and overall version status to report the expected release image and a
// completed update. It polls every 10 seconds for up to 30 minutes and fails
// the current Ginkgo assertion if the upgrade does not complete.
func ExpectHostedClusterUpgradeToComplete(
	ctx context.Context,
	client crclient.Client,
	hc *hyperv1.HostedCluster,
	image string,
) {
	GinkgoHelper()

	identity, err := resolveHostedClusterUpgradeTarget(ctx, client, hc, image, &releaseinfo.RegistryClientProvider{})
	Expect(err).NotTo(HaveOccurred(), "resolve the effective upgrade target before waiting for rollout")
	Eventually(func(g Gomega) {
		expectHostedClusterUpgradeCompleteForIdentity(ctx, g, client, hc, identity)
	}).
		WithContext(ctx).
		WithTimeout(30 * time.Minute).
		WithPolling(10 * time.Second).
		Should(Succeed())
}

// ExpectHostedClusterUpgradeComplete performs one check that the HostedCluster
// reports the expected release image and completed update in both its control
// plane and overall version status. The supplied Gomega instance must be used
// for assertions so this function can be called from Eventually.
func ExpectHostedClusterUpgradeComplete(ctx context.Context, g Gomega, mgmtClient crclient.Client, hc *hyperv1.HostedCluster, image string) {
	GinkgoHelper()
	identity, err := resolveHostedClusterUpgradeTarget(ctx, mgmtClient, hc, image, &releaseinfo.RegistryClientProvider{})
	if !g.Expect(err).NotTo(HaveOccurred()) {
		return
	}
	expectHostedClusterUpgradeCompleteForIdentity(ctx, g, mgmtClient, hc, identity)
}

func expectHostedClusterUpgradeCompleteForIdentity(ctx context.Context, g Gomega, mgmtClient crclient.Client, hc *hyperv1.HostedCluster, identity releaseImageIdentity) {
	GinkgoHelper()

	if !g.Expect(hc).NotTo(BeNil(), "HostedCluster input cannot be nil") {
		return
	}

	currentHC := &hyperv1.HostedCluster{}
	if err := mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), currentHC); err != nil {
		g.Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster %s/%s", hc.Namespace, hc.Name)
		return
	}

	if !g.Expect(hostedClusterUpgradeCompleteForIdentity(currentHC, hc, identity)).To(Succeed()) {
		return
	}
	applicableRelease := *identity.targetVersion
	applicableRelease.Patch = 0
	applicableRelease.Pre = nil
	applicableRelease.Build = nil
	if applicableRelease.LT(semver.MustParse("4.20.0")) {
		return
	}
	components := &hyperv1.ControlPlaneComponentList{}
	if !g.Expect(mgmtClient.List(ctx, components, crclient.InNamespace(currentHC.Namespace+"-"+currentHC.Name))).To(Succeed()) {
		return
	}
	var startingVersion string
	if hc.Status.Version != nil && len(hc.Status.Version.History) > 0 {
		startingVersion = hc.Status.Version.History[0].Version
	}
	g.Expect(controlPlaneComponentsUpgradeComplete(components.Items, startingVersion, identity.targetVersion.String())).To(Succeed())
}

func resolveHostedClusterUpgradeTarget(ctx context.Context, management crclient.Client, cluster *hyperv1.HostedCluster, image string, provider releaseinfo.Provider) (releaseImageIdentity, error) {
	if err := ctx.Err(); err != nil {
		return releaseImageIdentity{}, err
	}
	if cluster == nil {
		return releaseImageIdentity{}, fmt.Errorf("resolving an upgrade target requires its HostedCluster")
	}
	identity, err := hostedReleaseImageIdentity(ctx, management, cluster, image)
	if err != nil {
		return releaseImageIdentity{}, err
	}
	secret := &corev1.Secret{}
	if err := management.Get(ctx, crclient.ObjectKey{Namespace: cluster.Namespace, Name: cluster.Spec.PullSecret.Name}, secret); err != nil {
		return releaseImageIdentity{}, fmt.Errorf("read upgrade target pull secret: %w", err)
	}
	if len(secret.Data[corev1.DockerConfigJsonKey]) == 0 {
		return releaseImageIdentity{}, fmt.Errorf("upgrade target pull secret has no docker config")
	}
	payload, err := provider.Lookup(ctx, identity.Canonical, secret.Data[corev1.DockerConfigJsonKey])
	if err != nil {
		return releaseImageIdentity{}, fmt.Errorf("resolve immutable upgrade target metadata: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return releaseImageIdentity{}, err
	}
	if payload == nil || payload.ImageStream == nil {
		return releaseImageIdentity{}, fmt.Errorf("upgrade target metadata must contain an image stream")
	}
	version, err := semver.Parse(payload.Version())
	if err != nil {
		return releaseImageIdentity{}, fmt.Errorf("parse upgrade target version: %w", err)
	}
	identity.targetVersion = &version
	return identity, nil
}

func controlPlaneComponentsUpgradeComplete(components []hyperv1.ControlPlaneComponent, startingVersion, targetVersion string) error {
	if len(components) <= 10 {
		return fmt.Errorf("expected more than ten control plane components, found %d", len(components))
	}
	versionsDiffer := false
	if startingVersion != "" {
		startingRelease, err := semver.Parse(startingVersion)
		if err != nil {
			return fmt.Errorf("parsing starting component release %q: %w", startingVersion, err)
		}
		targetRelease, err := semver.Parse(targetVersion)
		if err != nil {
			return fmt.Errorf("parsing target component release %q: %w", targetVersion, err)
		}
		versionsDiffer = !startingRelease.EQ(targetRelease)
	}
	for _, component := range components {
		if !meta.IsStatusConditionTrue(component.Status.Conditions, string(hyperv1.ControlPlaneComponentRolloutComplete)) {
			return fmt.Errorf("component %s must complete rollout", component.Name)
		}
		if versionsDiffer && component.Status.Version == startingVersion {
			return fmt.Errorf("component %s must leave the starting version", component.Name)
		}
	}
	return nil
}

func hostedClusterUpgradeComplete(current, original *hyperv1.HostedCluster, image string, targetVersion semver.Version) error {
	return hostedClusterUpgradeCompleteForIdentity(current, original, releaseImageIdentity{Requested: image, Canonical: image, targetVersion: &targetVersion})
}

func hostedClusterUpgradeCompleteForIdentity(current, original *hyperv1.HostedCluster, identity releaseImageIdentity) error {
	if identity.Requested == "" {
		return fmt.Errorf("requested upgrade image must not be empty")
	}
	if identity.targetVersion == nil {
		return fmt.Errorf("upgrade target must have independently resolved version metadata")
	}
	if current == nil || current.Status.Version == nil || len(current.Status.Version.History) == 0 {
		return fmt.Errorf("hosted cluster must report a version history")
	}
	if !meta.IsStatusConditionTrue(current.Status.Conditions, string(hyperv1.HostedClusterAvailable)) ||
		!meta.IsStatusConditionPresentAndEqual(current.Status.Conditions, string(hyperv1.HostedClusterProgressing), metav1.ConditionFalse) {
		return fmt.Errorf("hosted cluster must be Available and not Progressing")
	}
	history := current.Status.Version.History[0]
	if !identity.MatchesDataPlane(current.Status.Version.Desired.Image) || !identity.MatchesDataPlane(history.Image) || history.State != configv1.CompletedUpdate {
		return fmt.Errorf("data plane desired image and completed history must match the requested image")
	}
	if original != nil && original.Status.Version != nil && len(original.Status.Version.History) > 0 {
		previous := original.Status.Version.History[0]
		if !identity.MatchesDataPlane(previous.Image) && previous.CompletionTime != nil && history.CompletionTime != nil && previous.CompletionTime.Equal(history.CompletionTime) {
			return fmt.Errorf("data plane completion time must advance during an upgrade")
		}
	}
	_, err := semver.Parse(history.Version)
	if err != nil {
		return fmt.Errorf("completed data plane version must be a semantic version")
	}
	applicableRelease := *identity.targetVersion
	applicableRelease.Patch = 0
	applicableRelease.Pre = nil
	applicableRelease.Build = nil
	if applicableRelease.GTE(semver.MustParse("4.22.0")) {
		controlPlane := current.Status.ControlPlaneVersion
		if !identity.Matches(controlPlane.Desired.Image) || len(controlPlane.History) == 0 || !identity.Matches(controlPlane.History[0].Image) || controlPlane.History[0].State != configv1.CompletedUpdate {
			return fmt.Errorf("control plane desired image and completed history must match the requested image")
		}
	}
	return nil
}
