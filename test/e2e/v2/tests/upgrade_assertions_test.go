//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"

	configv1 "github.com/openshift/api/config/v1"
	imagev1 "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/blang/semver"
	"github.com/onsi/gomega"
)

func TestExpectHostedClusterUpgradeCompleteForIdentity(t *testing.T) {
	const target = "quay.io/openshift-release-dev/ocp-release@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	scheme := runtime.NewScheme()
	if err := hyperv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name                                          string
		dataImage, controlPlaneImage                  string
		dataState, controlPlaneState                  configv1.UpdateState
		missingHistory, missingCluster, wantFailure   bool
		observedVersion                               string
		missingComponents, oldTarget, missingMetadata bool
		targetVersion, startingVersion                string
		insufficientComponents, unreadyComponent      bool
		staleComponent, missingControlPlane           bool
	}{
		{name: "When both target histories complete, it should pass", dataImage: "target", controlPlaneImage: "target", dataState: configv1.CompletedUpdate, controlPlaneState: configv1.CompletedUpdate},
		{name: "When desired is new but CVO history is old completed, it should fail", dataImage: "old", controlPlaneImage: "target", dataState: configv1.CompletedUpdate, controlPlaneState: configv1.CompletedUpdate, wantFailure: true},
		{name: "When desired is new but CP history is old completed, it should fail", dataImage: "target", controlPlaneImage: "old", dataState: configv1.CompletedUpdate, controlPlaneState: configv1.CompletedUpdate, wantFailure: true},
		{name: "When only CP completed, it should fail", dataImage: "target", controlPlaneImage: "target", dataState: configv1.PartialUpdate, controlPlaneState: configv1.CompletedUpdate, wantFailure: true},
		{name: "When only CVO completed, it should fail", dataImage: "target", controlPlaneImage: "target", dataState: configv1.CompletedUpdate, controlPlaneState: configv1.PartialUpdate, wantFailure: true},
		{name: "When CP history is absent, it should fail", dataImage: "target", dataState: configv1.CompletedUpdate, missingHistory: true, wantFailure: true},
		{name: "When the cluster read fails, it should fail", missingCluster: true, wantFailure: true},
		{name: "When observed history is older but the target requires CPC, it should reject missing components", observedVersion: "4.19.9", missingComponents: true, dataImage: "target", controlPlaneImage: "target", dataState: configv1.CompletedUpdate, controlPlaneState: configv1.CompletedUpdate, wantFailure: true},
		{name: "When observed history is older but the target requires CPV, it should reject missing CPV", observedVersion: "4.21.9", missingHistory: true, dataImage: "target", dataState: configv1.CompletedUpdate, wantFailure: true},
		{name: "When the actual target predates CPC and CPV, it should preserve legacy applicability", observedVersion: "4.19.9", oldTarget: true, missingComponents: true, missingHistory: true, dataImage: "target", dataState: configv1.CompletedUpdate},
		{name: "When target metadata is unavailable, it should not infer eligibility from history", missingMetadata: true, dataImage: "target", controlPlaneImage: "target", dataState: configv1.CompletedUpdate, controlPlaneState: configv1.CompletedUpdate, wantFailure: true},
		{name: "When a 4.20 nightly target has no components, it should reject the missing rollout", targetVersion: "4.20.0-0.nightly+build.1", missingComponents: true, wantFailure: true},
		{name: "When a 4.20 nightly target has only ten components, it should reject incomplete observation", targetVersion: "4.20.0-0.nightly", insufficientComponents: true, wantFailure: true},
		{name: "When a 4.20 nightly target has an unready component, it should keep waiting", targetVersion: "4.20.0-0.nightly", unreadyComponent: true, wantFailure: true},
		{name: "When a 4.20 nightly target has completed components without CPV, it should complete", targetVersion: "4.20.0-0.nightly", missingControlPlane: true},
		{name: "When a stable 4.20 target has no components, it should reject the missing rollout", targetVersion: "4.20.0", missingComponents: true, wantFailure: true},
		{name: "When a stable 4.20 target has an unready component, it should keep waiting", targetVersion: "4.20.0", unreadyComponent: true, wantFailure: true},
		{name: "When a stable 4.20 target has completed components without CPV, it should complete", targetVersion: "4.20.0", missingControlPlane: true},
		{name: "When a 4.19 target lacks components and CPV, it should preserve older-family applicability", targetVersion: "4.19.9", missingComponents: true, missingControlPlane: true},
		{name: "When a 4.21 target lacks CPV, it should preserve adjacent-family applicability", targetVersion: "4.21.9", missingControlPlane: true},
		{name: "When a 4.22 nightly target lacks CPV, it should keep waiting", targetVersion: "4.22.0-0.nightly+build.1", missingControlPlane: true, wantFailure: true},
		{name: "When a 4.22 nightly target has Partial CPV, it should keep waiting", targetVersion: "4.22.0-0.nightly", controlPlaneState: configv1.PartialUpdate, wantFailure: true},
		{name: "When a 4.22 nightly target has stale canonical CPV history, it should keep waiting", targetVersion: "4.22.0-0.nightly", controlPlaneImage: "old", wantFailure: true},
		{name: "When a 4.22 nightly target completes both rollouts, it should complete", targetVersion: "4.22.0-0.nightly+build.1"},
		{name: "When a stable 4.22 target lacks CPV, it should keep waiting", targetVersion: "4.22.0", missingControlPlane: true, wantFailure: true},
		{name: "When a stable 4.22 target completes both rollouts, it should complete", targetVersion: "4.22.0"},
		{name: "When a newer nightly leaves a component at the starting nightly, it should require the full prerelease transition", targetVersion: "4.20.0-0.nightly.2", startingVersion: "4.20.0-0.nightly.1", staleComponent: true, wantFailure: true},
		{name: "When a newer nightly updates every component, it should preserve the full prerelease transition", targetVersion: "4.20.0-0.nightly.2", startingVersion: "4.20.0-0.nightly.1"},
		{name: "When a patch upgrade leaves a component at the stable starting version, it should require the full patch transition", targetVersion: "4.20.1", startingVersion: "4.20.0", staleComponent: true, wantFailure: true},
		{name: "When a patch upgrade updates every component, it should preserve the full patch transition", targetVersion: "4.20.1", startingVersion: "4.20.0"},
		{name: "When the same nightly image has completed components, it should permit unchanged component versions", targetVersion: "4.20.0-0.nightly", startingVersion: "4.20.0-0.nightly", staleComponent: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.targetVersion != "" {
				if testCase.dataImage == "" {
					testCase.dataImage = "target"
				}
				if testCase.controlPlaneImage == "" {
					testCase.controlPlaneImage = "target"
				}
				if testCase.dataState == "" {
					testCase.dataState = configv1.CompletedUpdate
				}
				if testCase.controlPlaneState == "" {
					testCase.controlPlaneState = configv1.CompletedUpdate
				}
			}
			version := "4.22.1"
			if testCase.oldTarget {
				version = "4.19.9"
			}
			if testCase.targetVersion != "" {
				version = testCase.targetVersion
			}
			if testCase.dataImage == "target" {
				testCase.dataImage = target
			}
			if testCase.controlPlaneImage == "target" {
				testCase.controlPlaneImage = target
			}
			cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}, Spec: hyperv1.HostedClusterSpec{PullSecret: corev1.LocalObjectReference{Name: "pull-secret"}}, Status: hyperv1.HostedClusterStatus{
				Conditions:          []metav1.Condition{{Type: string(hyperv1.HostedClusterAvailable), Status: metav1.ConditionTrue}, {Type: string(hyperv1.HostedClusterProgressing), Status: metav1.ConditionFalse}},
				Version:             &hyperv1.ClusterVersionStatus{Desired: configv1.Release{Image: target}, History: []configv1.UpdateHistory{{Image: testCase.dataImage, Version: version, State: testCase.dataState}}},
				ControlPlaneVersion: hyperv1.ControlPlaneVersionStatus{Desired: configv1.Release{Image: target}, History: []hyperv1.ControlPlaneUpdateHistory{{Image: testCase.controlPlaneImage, State: testCase.controlPlaneState}}},
			}}
			if testCase.missingHistory {
				cluster.Status.ControlPlaneVersion.History = nil
			}
			if testCase.missingControlPlane {
				cluster.Status.ControlPlaneVersion = hyperv1.ControlPlaneVersionStatus{}
			}
			if testCase.observedVersion != "" {
				cluster.Status.Version.History[0].Version = testCase.observedVersion
			}
			original := cluster.DeepCopy()
			if testCase.startingVersion != "" {
				original.Status.Version.History[0].Version = testCase.startingVersion
			}
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: cluster.Namespace, Name: cluster.Spec.PullSecret.Name}, Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("synthetic-offline-config")}}
			objects := []crclient.Object{secret}
			if !testCase.missingCluster {
				objects = append(objects, cluster)
			}
			componentCount := 11
			if testCase.missingComponents {
				componentCount = 0
			}
			if testCase.insufficientComponents {
				componentCount = 10
			}
			objects = append(objects, upgradeAssertionComponents(componentCount, version, testCase.startingVersion, testCase.unreadyComponent, testCase.staleComponent)...)
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			failed := false
			assertions := gomega.NewGomega(func(string, ...int) { failed = true })
			provider := failureConditionReleaseProvider(func(ctx context.Context, image string, pullSecret []byte) (*releaseinfo.ReleaseImage, error) {
				if image != target || string(pullSecret) != "synthetic-offline-config" {
					t.Fatal("target resolution did not use canonical release identity and configured pull secret")
				}
				return &releaseinfo.ReleaseImage{ImageStream: &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: version}}}, nil
			})
			identity, err := resolveHostedClusterUpgradeTarget(t.Context(), client, original, target, provider)
			if err != nil {
				t.Fatalf("resolve target before checking completion: %v", err)
			}
			if testCase.missingMetadata {
				identity.targetVersion = nil
			}
			expectHostedClusterUpgradeCompleteForIdentity(t.Context(), assertions, client, original, identity)
			if failed != testCase.wantFailure {
				t.Fatalf("unexpected upgrade assertion result: failed=%t", failed)
			}
			if !testCase.missingMetadata && identity.targetVersion.String() != version {
				t.Fatalf("completion checks mutated full target metadata: %s", identity.targetVersion)
			}
		})
	}
}

func upgradeAssertionComponents(count int, version, startingVersion string, unready, stale bool) []crclient.Object {
	components := make([]crclient.Object, 0, count)
	for index := range count {
		component := &hyperv1.ControlPlaneComponent{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("component-%d", index), Namespace: "clusters-cluster"}, Status: hyperv1.ControlPlaneComponentStatus{Version: version, Conditions: []metav1.Condition{{Type: string(hyperv1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue}}}}
		if index == 0 && unready {
			component.Status.Conditions = nil
		}
		if index == 0 && stale {
			component.Status.Version = startingVersion
		}
		components = append(components, component)
	}
	return components
}

func TestControlPlaneComponentsUpgradeComplete(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		count          int
		starting       string
		target         string
		unready, stale bool
		wantErr        bool
	}{
		{name: "When all eleven components rolled, it should complete", count: 11},
		{name: "When no components exist, it should not pass vacuously", wantErr: true},
		{name: "When only ten components exist, it should reject incomplete observation", count: 10, wantErr: true},
		{name: "When a component has not rolled, it should keep waiting", count: 11, unready: true, wantErr: true},
		{name: "When a component still reports the starting version, it should keep waiting", count: 11, stale: true, wantErr: true},
		{name: "When an image change retains the release version, it should require rollout without a version-string change", count: 11, stale: true, starting: "4.21.1", target: "4.21.1"},
		{name: "When image-change versions differ only in build metadata, it should preserve semantic-version-equivalent rollouts", count: 11, stale: true, starting: "4.21.1", target: "4.21.1+rebuilt"},
		{name: "When an equal-version image has an unready component, it should keep waiting", count: 11, unready: true, starting: "4.21.1", target: "4.21.1", wantErr: true},
		{name: "When the starting version is malformed, it should reject an unbound comparison", count: 11, starting: "invalid", target: "4.22.1", wantErr: true},
		{name: "When the target version is malformed, it should reject an unbound comparison", count: 11, starting: "4.21.1", target: "invalid", wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			components := make([]hyperv1.ControlPlaneComponent, testCase.count)
			for index := range components {
				components[index].Name = fmt.Sprintf("component-%d", index)
				components[index].Status.Version = "4.22.1"
				components[index].Status.Conditions = []metav1.Condition{{Type: string(hyperv1.ControlPlaneComponentRolloutComplete), Status: metav1.ConditionTrue}}
			}
			if testCase.unready {
				components[0].Status.Conditions = nil
			}
			if testCase.stale {
				components[0].Status.Version = "4.21.1"
			}
			startingVersion, targetVersion := testCase.starting, testCase.target
			if startingVersion == "" && targetVersion == "" {
				startingVersion, targetVersion = "4.21.1", "4.22.1"
			}
			if err := controlPlaneComponentsUpgradeComplete(components, startingVersion, targetVersion); (err != nil) != testCase.wantErr {
				t.Fatalf("unexpected component rollout result: %v", err)
			}
		})
	}
}

func TestHostedClusterUpgradeComplete(t *testing.T) {
	image := "quay.io/openshift-release-dev/ocp-release:4.22.1-x86_64"
	completed := &hyperv1.HostedCluster{Status: hyperv1.HostedClusterStatus{
		Conditions: []metav1.Condition{{Type: string(hyperv1.HostedClusterAvailable), Status: metav1.ConditionTrue}, {Type: string(hyperv1.HostedClusterProgressing), Status: metav1.ConditionFalse}},
		Version:    &hyperv1.ClusterVersionStatus{Desired: configv1.Release{Image: image}, History: []configv1.UpdateHistory{{Image: image, Version: "4.22.1", State: configv1.CompletedUpdate, CompletionTime: &metav1.Time{Time: time.Unix(100, 0)}}}},
	}}
	completed.Status.ControlPlaneVersion = hyperv1.ControlPlaneVersionStatus{Desired: configv1.Release{Image: image}, History: []hyperv1.ControlPlaneUpdateHistory{{Image: image, Version: "4.22.1", State: configv1.CompletedUpdate}}}
	for _, testCase := range []struct {
		name    string
		mutate  func(*hyperv1.HostedCluster)
		target  string
		wantErr bool
	}{
		{name: "When both histories match the requested release, it should complete"},
		{name: "When the version history is missing, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version = nil }, wantErr: true},
		{name: "When the data plane history is empty, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version.History = nil }, wantErr: true},
		{name: "When the data plane is still Partial, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version.History[0].State = configv1.PartialUpdate }, wantErr: true},
		{name: "When only the data plane is complete, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Status.ControlPlaneVersion.History[0].State = configv1.PartialUpdate
		}, wantErr: true},
		{name: "When the desired data plane image is stale, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version.Desired.Image = "previous-image" }, wantErr: true},
		{name: "When the desired control plane image is stale, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Status.ControlPlaneVersion.Desired.Image = "previous-image"
		}, wantErr: true},
		{name: "When the data plane history image is stale, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version.History[0].Image = "previous-image" }, wantErr: true},
		{name: "When the control plane history image is stale, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Status.ControlPlaneVersion.History[0].Image = "previous-image"
		}, wantErr: true},
		{name: "When the control plane history is missing, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.ControlPlaneVersion.History = nil }, wantErr: true},
		{name: "When reported history predates support but the target requires CPV, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Status.Version.History[0].Version = "4.21.9"
			cluster.Status.ControlPlaneVersion = hyperv1.ControlPlaneVersionStatus{}
		}, wantErr: true},
		{name: "When the independently resolved target predates CPV support, it should not require that field", target: "4.21.9", mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Status.Version.History[0].Version = "4.21.9"
			cluster.Status.ControlPlaneVersion = hyperv1.ControlPlaneVersionStatus{}
		}},
		{name: "When Available is false, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Conditions[0].Status = metav1.ConditionFalse }, wantErr: true},
		{name: "When Progressing is true, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Conditions[1].Status = metav1.ConditionTrue }, wantErr: true},
		{name: "When completion time has not advanced, it should keep waiting", mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Status.Version.History[0].CompletionTime = &metav1.Time{Time: time.Unix(50, 0)}
		}, wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			current := completed.DeepCopy()
			if testCase.mutate != nil {
				testCase.mutate(current)
			}
			original := completed.DeepCopy()
			original.Status.Version.History[0].Image = "previous-image"
			original.Status.Version.History[0].CompletionTime = &metav1.Time{Time: time.Unix(50, 0)}
			target := semver.MustParse("4.22.1")
			if testCase.target != "" {
				target = semver.MustParse(testCase.target)
			}
			if err := hostedClusterUpgradeComplete(current, original, image, target); (err != nil) != testCase.wantErr {
				t.Fatalf("unexpected rollout result: %v", err)
			}
		})
	}
}

func TestResolveHostedClusterUpgradeTarget(t *testing.T) {
	const image = "quay.io/openshift-release-dev/ocp-release@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, testCase := range []struct {
		name, version                                                                string
		missingSecret, emptySecret, lookupError, missingPayload, canceled, wantError bool
	}{
		{name: "When immutable target metadata resolves, it should carry independent eligibility", version: "4.22.1"},
		{name: "When 4.20 nightly metadata resolves, it should retain full prerelease and build metadata", version: "4.20.0-0.nightly+build.1"},
		{name: "When 4.22 nightly metadata resolves, it should retain full prerelease and build metadata", version: "4.22.0-0.nightly+build.1"},
		{name: "When the real target predates CPV support, it should preserve old-release applicability", version: "4.19.9"},
		{name: "When pull-secret lookup fails, it should reject the target", missingSecret: true, wantError: true},
		{name: "When the pull secret is empty, it should reject the target", emptySecret: true, wantError: true},
		{name: "When target lookup fails, it should preserve the error", lookupError: true, wantError: true},
		{name: "When target metadata is absent, it should reject the target", missingPayload: true, wantError: true},
		{name: "When target metadata is malformed, it should reject the target", version: "invalid", wantError: true},
		{name: "When the caller is canceled, it should reject the target before lookup", canceled: true, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "cluster"}, Spec: hyperv1.HostedClusterSpec{PullSecret: corev1.LocalObjectReference{Name: "pull-secret"}}}
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: cluster.Namespace, Name: cluster.Spec.PullSecret.Name}, Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("synthetic-offline-config")}}
			if testCase.emptySecret {
				secret.Data = nil
			}
			objects := []crclient.Object{}
			if !testCase.missingSecret {
				objects = append(objects, secret)
			}
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(objects...).Build()
			calls := 0
			provider := failureConditionReleaseProvider(func(ctx context.Context, requested string, pullSecret []byte) (*releaseinfo.ReleaseImage, error) {
				calls++
				if requested != image || string(pullSecret) != "synthetic-offline-config" {
					t.Fatal("target lookup did not use immutable identity and configured credentials")
				}
				if testCase.lookupError {
					return nil, errors.New("target lookup unavailable")
				}
				if testCase.missingPayload {
					return nil, nil
				}
				return &releaseinfo.ReleaseImage{ImageStream: &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: testCase.version}}}, nil
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if testCase.canceled {
				cancel()
			}
			identity, err := resolveHostedClusterUpgradeTarget(ctx, client, cluster, image, provider)
			if (err != nil) != testCase.wantError {
				t.Fatalf("unexpected target resolution: %v", err)
			}
			if err == nil && (identity.targetVersion == nil || identity.targetVersion.String() != testCase.version) {
				t.Fatalf("resolved target eligibility lost: %#v", identity)
			}
			if (testCase.canceled || testCase.missingSecret || testCase.emptySecret) && calls != 0 {
				t.Fatal("target lookup occurred before input validation")
			}
		})
	}
}

func TestValidateControlPlaneUpgradeInput(t *testing.T) {
	for _, testCase := range []struct {
		name, specImage, historyImage, targetImage string
		missingVersion, wantErr                    bool
	}{
		{name: "When the observed previous release differs from target, it should accept the fixture", specImage: "previous", historyImage: "previous", targetImage: "target"},
		{name: "When the fixture spec already uses latest, it should reject a no-op", specImage: "target", historyImage: "target", targetImage: "target", wantErr: true},
		{name: "When status already reached latest despite a stale spec, it should reject a no-op", specImage: "previous", historyImage: "target", targetImage: "target", wantErr: true},
		{name: "When latest was not supplied, it should reject the missing prerequisite", specImage: "previous", historyImage: "previous", wantErr: true},
		{name: "When the starting release was not observed, it should reject the fixture", specImage: "previous", targetImage: "target", missingVersion: true, wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{Release: hyperv1.Release{Image: testCase.specImage}}, Status: hyperv1.HostedClusterStatus{Version: &hyperv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{Image: testCase.historyImage, Version: "4.21.1"}}}}}
			if testCase.missingVersion {
				cluster.Status.Version = nil
			}
			if err := validateControlPlaneUpgradeInput(cluster, testCase.targetImage); (err != nil) != testCase.wantErr {
				t.Fatalf("unexpected upgrade fixture validation: %v", err)
			}
		})
	}
}
