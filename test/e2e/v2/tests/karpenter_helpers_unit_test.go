//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/awslabs/operatorpkg/status"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/blang/semver"
)

func TestAutoNodeUpgradeTarget(t *testing.T) {
	previousImage := "quay.io/openshift-release-dev/ocp-release@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	targetImage := "quay.io/openshift-release-dev/ocp-release:4.23.0-x86_64"
	targetCanonical := "quay.io/openshift-release-dev/ocp-release@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	starting := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{Release: hyperv1.Release{Image: previousImage}},
		Status: hyperv1.HostedClusterStatus{
			Version: &hyperv1.ClusterVersionStatus{
				History: []configv1.UpdateHistory{{Image: previousImage, Version: "4.22.0", State: configv1.CompletedUpdate}},
			},
			ControlPlaneVersion: hyperv1.ControlPlaneVersionStatus{
				Desired: configv1.Release{Image: previousImage},
				History: []hyperv1.ControlPlaneUpdateHistory{{Image: previousImage, Version: "4.22.0", State: configv1.CompletedUpdate}},
			},
		},
	}
	for _, testCase := range []struct {
		name            string
		mutate          func(*hyperv1.HostedCluster)
		targetImage     string
		targetCanonical string
		startCanonical  string
		targetVersion   string
		nilCluster      bool
		unresolved      bool
		valid           bool
	}{
		{name: "When resolved target content is a newer minor, it should accept the upgrade", targetImage: targetImage, targetVersion: "4.23.0", valid: true},
		{name: "When resolved target content is a newer patch, it should accept the upgrade", targetImage: targetImage, targetVersion: "4.22.1", valid: true},
		{name: "When a different tag resolves to the same digest, it should reject the alias no-op", targetImage: "quay.io/example/release:alias", targetCanonical: previousImage, targetVersion: "4.22.0"},
		{name: "When a mirror resolves to the same digest, it should reject the repository no-op", targetImage: "mirror.example.com/release:latest", targetCanonical: "mirror.example.com/release@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", targetVersion: "4.22.0"},
		{name: "When equal versions identify different manifest digests, it should preserve the image-change scenario", targetImage: targetImage, targetVersion: "4.22.0", valid: true},
		{name: "When a target alias resolves to the observed image despite a stale version, it should reject the identity no-op", targetImage: targetImage, targetCanonical: previousImage, targetVersion: "4.23.0"},
		{name: "When build metadata differs with a different digest, it should accept the image change", targetImage: targetImage, targetVersion: "4.22.0+rebuilt", valid: true},
		{name: "When build metadata differs with the same digest, it should reject the content no-op", targetImage: targetImage, targetCanonical: previousImage, targetVersion: "4.22.0+rebuilt"},
		{name: "When target content is older, it should reject a downgrade", targetImage: targetImage, targetVersion: "4.21.9"},
		{name: "When target content is an older prerelease, it should reject it", targetImage: targetImage, targetVersion: "4.22.0-rc.1"},
		{name: "When the requested image already matches the spec, it should reject a mutable-tag no-op", targetImage: previousImage, targetVersion: "4.23.0"},
		{name: "When the requested spec tag resolves to a different digest, it should still reject a no-op spec mutation", targetImage: previousImage, targetCanonical: "quay.io/example/release@sha256:new", targetVersion: "4.23.0"},
		{name: "When history retains the requested tag with a different canonical digest, it should reject an observed no-op", targetImage: targetImage, targetCanonical: "quay.io/example/release@sha256:new", targetVersion: "4.23.0", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version.History[0].Image = targetImage }},
		{name: "When the requested target is already observed, it should reject the completed no-op", targetImage: targetImage, targetVersion: "4.23.0", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version.History[0].Image = targetImage }},
		{name: "When target input is absent, it should reject configuration", targetVersion: "4.23.0"},
		{name: "When the target has not been resolved, it should reject an unbound version comparison", targetImage: targetImage, targetVersion: "4.23.0", unresolved: true},
		{name: "When the target canonical identity is still a tag, it should reject unresolved content", targetImage: targetImage, targetCanonical: targetImage, targetVersion: "4.23.0"},
		{name: "When the target digest is malformed, it should reject unresolved content", targetImage: targetImage, targetCanonical: "quay.io/example/release@sha256:invalid", targetVersion: "4.23.0"},
		{name: "When the starting identity is unresolved, it should reject unbound starting content", targetImage: targetImage, targetVersion: "4.23.0", startCanonical: "quay.io/example/release:previous"},
		{name: "When the starting identity is malformed, it should reject unbound starting content", targetImage: targetImage, targetVersion: "4.23.0", startCanonical: "quay.io/example/release@sha256:invalid"},
		{name: "When the starting CP history is missing, it should reject unobserved content", targetImage: targetImage, targetVersion: "4.23.0", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.ControlPlaneVersion.History = nil }},
		{name: "When the starting CP rollout is incomplete, it should reject ambiguous content", targetImage: targetImage, targetVersion: "4.23.0", mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Status.ControlPlaneVersion.History[0].State = configv1.PartialUpdate
		}},
		{name: "When starting desired differs from completed history, it should reject an in-progress fixture", targetImage: targetImage, targetVersion: "4.23.0", mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Status.ControlPlaneVersion.Desired.Image = targetCanonical
		}},
		{name: "When the starting requested tag has moved, it should use the observed digest instead of resolving that tag again", targetImage: targetImage, targetVersion: "4.22.0", valid: true, mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Spec.Release.Image = "quay.io/example/release:previous"
			cluster.Status.Version.History[0].Image = cluster.Spec.Release.Image
		}},
		{name: "When the cluster is absent, it should reject configuration", targetImage: targetImage, targetVersion: "4.23.0", nilCluster: true},
		{name: "When status is absent, it should require an observed start", targetImage: targetImage, targetVersion: "4.23.0", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version = nil }},
		{name: "When history is empty, it should require an observed start", targetImage: targetImage, targetVersion: "4.23.0", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version.History = nil }},
		{name: "When the starting rollout is incomplete, it should reject ambiguous starting state", targetImage: targetImage, targetVersion: "4.23.0", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version.History[0].State = configv1.PartialUpdate }},
		{name: "When the starting version is invalid, it should reject the comparison", targetImage: targetImage, targetVersion: "4.23.0", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version.History[0].Version = "invalid" }},
		{name: "When the starting version is empty, it should reject the comparison", targetImage: targetImage, targetVersion: "4.23.0", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version.History[0].Version = "" }},
		{name: "When the starting spec image is absent, it should reject configuration", targetImage: targetImage, targetVersion: "4.23.0", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Spec.Release.Image = "" }},
		{name: "When the observed starting image is absent, it should reject configuration", targetImage: targetImage, targetVersion: "4.23.0", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version.History[0].Image = "" }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := starting.DeepCopy()
			if testCase.nilCluster {
				cluster = nil
			} else if testCase.mutate != nil {
				testCase.mutate(cluster)
			}
			version := semver.MustParse(testCase.targetVersion)
			target := releaseImageIdentity{Requested: testCase.targetImage, Canonical: targetCanonical}
			if testCase.targetCanonical != "" {
				target.Canonical = testCase.targetCanonical
			}
			if testCase.unresolved {
				target.Canonical = ""
			}
			if testCase.startCanonical != "" {
				cluster.Status.ControlPlaneVersion.Desired.Image = testCase.startCanonical
				cluster.Status.ControlPlaneVersion.History[0].Image = testCase.startCanonical
			}
			if err := autoNodeUpgradeTarget(cluster, target, version); (err == nil) != testCase.valid {
				t.Fatalf("got error %v, want valid=%t", err, testCase.valid)
			}
		})
	}
}

func TestAutoNodeControlPlaneRolloutComplete(t *testing.T) {
	const targetDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	identity := releaseImageIdentity{
		Requested: "quay.io/example/release:latest",
		Canonical: "quay.io/example/release@" + targetDigest,
	}
	ready := hyperv1.ControlPlaneVersionStatus{
		Desired: configv1.Release{Image: identity.Canonical},
		History: []hyperv1.ControlPlaneUpdateHistory{{Image: identity.Canonical, State: configv1.CompletedUpdate}},
	}
	for _, testCase := range []struct {
		name          string
		mutate        func(*hyperv1.ControlPlaneVersionStatus)
		emptyIdentity bool
		complete      bool
	}{
		{name: "When both desired and history are the resolved digest, it should permit post-upgrade drift", complete: true},
		{name: "When desired retains the requested tag, it should keep guarding against drift", mutate: func(version *hyperv1.ControlPlaneVersionStatus) { version.Desired.Image = identity.Requested }},
		{name: "When history retains the requested tag, it should keep guarding against drift", mutate: func(version *hyperv1.ControlPlaneVersionStatus) { version.History[0].Image = identity.Requested }},
		{name: "When both desired and history retain the requested reference, it should keep guarding against drift", mutate: func(version *hyperv1.ControlPlaneVersionStatus) {
			version.Desired.Image = identity.Requested
			version.History[0].Image = identity.Requested
		}},
		{name: "When desired still identifies the previous release, it should keep guarding against drift", mutate: func(version *hyperv1.ControlPlaneVersionStatus) {
			version.Desired.Image = "quay.io/example/release:previous"
		}},
		{name: "When completed history has a different digest, it should keep guarding against drift", mutate: func(version *hyperv1.ControlPlaneVersionStatus) {
			version.History[0].Image = "quay.io/example/release@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		}},
		{name: "When target history is incomplete, it should keep guarding against drift", mutate: func(version *hyperv1.ControlPlaneVersionStatus) { version.History[0].State = configv1.PartialUpdate }},
		{name: "When history is absent, it should keep guarding against drift", mutate: func(version *hyperv1.ControlPlaneVersionStatus) { version.History = nil }},
		{name: "When target identity is absent, it should not pass vacuously", emptyIdentity: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			version := ready.DeepCopy()
			if testCase.mutate != nil {
				testCase.mutate(version)
			}
			target := identity
			if testCase.emptyIdentity {
				target = releaseImageIdentity{}
			}
			if complete := autoNodeControlPlaneRolloutComplete(*version, target); complete != testCase.complete {
				t.Fatalf("got complete=%t, want complete=%t", complete, testCase.complete)
			}
		})
	}
}

func TestAutoNodeClaimReadiness(t *testing.T) {
	ready := &karpenterv1.NodeClaim{Status: karpenterv1.NodeClaimStatus{
		ProviderID: "aws:///us-east-1a/i-0123456789abcdef0",
		Conditions: []status.Condition{
			{Type: karpenterv1.ConditionTypeLaunched, Status: metav1.ConditionTrue},
			{Type: karpenterv1.ConditionTypeRegistered, Status: metav1.ConditionTrue},
			{Type: karpenterv1.ConditionTypeInitialized, Status: metav1.ConditionTrue},
		},
	}}
	testCases := []struct {
		name   string
		mutate func(*karpenterv1.NodeClaim)
		nil    bool
		valid  bool
	}{
		{name: "When all launch stages converge, it should accept the live claim", valid: true},
		{name: "When a claim is nil, it should reject missing state", nil: true},
		{name: "When a claim is deleting, it should reject a stale replacement", mutate: func(claim *karpenterv1.NodeClaim) { claim.DeletionTimestamp = &metav1.Time{Time: time.Now()} }},
		{name: "When a provider ID is missing, it should reject unprovisioned state", mutate: func(claim *karpenterv1.NodeClaim) { claim.Status.ProviderID = "" }},
		{name: "When Launched is false, it should reject incomplete launch", mutate: func(claim *karpenterv1.NodeClaim) { claim.Status.Conditions[0].Status = metav1.ConditionFalse }},
		{name: "When Registered is false, it should reject incomplete registration", mutate: func(claim *karpenterv1.NodeClaim) { claim.Status.Conditions[1].Status = metav1.ConditionFalse }},
		{name: "When Initialized is absent, it should not pass vacuously", mutate: func(claim *karpenterv1.NodeClaim) { claim.Status.Conditions = claim.Status.Conditions[:2] }},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			claim := ready.DeepCopy()
			if testCase.nil {
				claim = nil
			} else if testCase.mutate != nil {
				testCase.mutate(claim)
			}
			if err := autoNodeClaimReadiness(claim); (err == nil) != testCase.valid {
				t.Fatalf("got error %v, want valid=%t", err, testCase.valid)
			}
		})
	}
}

func TestAutoNodeProgressEvent(t *testing.T) {
	progressing := &hyperv1.HostedCluster{Status: hyperv1.HostedClusterStatus{Conditions: []metav1.Condition{
		{Type: string(hyperv1.AutoNodeEnabled), Status: metav1.ConditionFalse, Reason: hyperv1.AutoNodeProgressingReason},
	}}}
	ready := progressing.DeepCopy()
	ready.Status.Conditions[0].Status = metav1.ConditionTrue
	ready.Status.Conditions[0].Reason = hyperv1.AsExpectedReason
	testCases := []struct {
		name    string
		event   watch.Event
		matched bool
		wantErr bool
	}{
		{name: "When a modification records False Progressing, it should retain the transient evidence", event: watch.Event{Type: watch.Modified, Object: progressing}, matched: true},
		{name: "When only final Ready is observed, it should not substitute it for progress", event: watch.Event{Type: watch.Modified, Object: ready}},
		{name: "When the event is a bookmark, it should ignore it", event: watch.Event{Type: watch.Bookmark}},
		{name: "When the event is a deletion, it should not treat it as enable progress", event: watch.Event{Type: watch.Deleted, Object: progressing}},
		{name: "When an API watch error arrives, it should expose the error", event: watch.Event{Type: watch.Error, Object: &metav1.Status{Status: metav1.StatusFailure, Message: "watch expired", Reason: metav1.StatusReasonExpired, Code: 410}}, wantErr: true},
		{name: "When the event object has the wrong type, it should expose the mismatch", event: watch.Event{Type: watch.Modified, Object: &corev1.ConfigMap{}}, wantErr: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			matched, err := autoNodeProgressEvent(testCase.event)
			if matched != testCase.matched || (err != nil) != testCase.wantErr {
				t.Fatalf("got match=%t error=%v, want match=%t error=%t", matched, err, testCase.matched, testCase.wantErr)
			}
		})
	}
}

func TestAutoNodeEndpointsAvailable(t *testing.T) {
	available := hyperv1.AWSEndpointService{Status: hyperv1.AWSEndpointServiceStatus{Conditions: []metav1.Condition{
		{Type: string(hyperv1.AWSEndpointAvailable), Status: metav1.ConditionTrue},
	}}}
	unavailable := available.DeepCopy()
	unavailable.Status.Conditions[0].Status = metav1.ConditionFalse
	testCases := []struct {
		name     string
		services []hyperv1.AWSEndpointService
		wantErr  bool
	}{
		{name: "When no endpoints exist, it should reject vacuous availability", wantErr: true},
		{name: "When an endpoint lacks its condition, it should reject missing status", services: []hyperv1.AWSEndpointService{{}}, wantErr: true},
		{name: "When an endpoint is unavailable, it should reject False polarity", services: []hyperv1.AWSEndpointService{*unavailable}, wantErr: true},
		{name: "When every endpoint is available, it should accept readiness", services: []hyperv1.AWSEndpointService{available}},
		{name: "When only one endpoint is available, it should reject partial readiness", services: []hyperv1.AWSEndpointService{available, *unavailable}, wantErr: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := autoNodeEndpointsAvailable(testCase.services); (err != nil) != testCase.wantErr {
				t.Fatalf("got error %v, want error=%t", err, testCase.wantErr)
			}
		})
	}
}

func TestAutoNodeSubnetCIDR(t *testing.T) {
	testCases := []struct {
		name     string
		vpc      string
		occupied []string
		expected string
		wantErr  bool
	}{
		{name: "When the VPC has no subnets, it should return the first aligned block", vpc: "10.0.0.0/16", expected: "10.0.0.0/28"},
		{name: "When an existing subnet overlaps candidates, it should skip all overlapping blocks", vpc: "10.0.0.0/16", occupied: []string{"10.0.0.0/20", "10.0.16.0/28"}, expected: "10.0.16.16/28"},
		{name: "When an existing subnet is smaller than a candidate, it should still detect overlap", vpc: "10.0.0.0/24", occupied: []string{"10.0.0.8/29"}, expected: "10.0.0.16/28"},
		{name: "When all VPC space is occupied, it should return an error", vpc: "10.0.0.0/28", occupied: []string{"10.0.0.0/28"}, wantErr: true},
		{name: "When the VPC CIDR is malformed, it should return an error", vpc: "not-a-cidr", wantErr: true},
		{name: "When the VPC is IPv6, it should reject the unsupported family", vpc: "fd00::/64", wantErr: true},
		{name: "When the VPC is too small, it should reject the allocation", vpc: "10.0.0.0/29", wantErr: true},
		{name: "When an occupied subnet is malformed, it should not risk overlapping allocation", vpc: "10.0.0.0/24", occupied: []string{"not-a-cidr"}, wantErr: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual, err := autoNodeSubnetCIDR(testCase.vpc, testCase.occupied)
			if (err != nil) != testCase.wantErr || actual != testCase.expected {
				t.Fatalf("got CIDR %q and error %v; want CIDR %q and error=%t", actual, err, testCase.expected, testCase.wantErr)
			}
		})
	}
}

func TestAutoNodeConditionPredicate(t *testing.T) {
	expected := v2util.Condition{Type: string(hyperv1.AutoNodeEnabled), Status: metav1.ConditionTrue, Reason: hyperv1.AsExpectedReason}
	testCases := []struct {
		name       string
		conditions []metav1.Condition
		matches    bool
	}{
		{name: "When conditions are empty, it should not pass vacuously"},
		{name: "When the requested condition is absent, it should reject unrelated success", conditions: []metav1.Condition{{Type: "Available", Status: metav1.ConditionTrue}}},
		{name: "When AutoNode is disabled, it should reject the opposite polarity", conditions: []metav1.Condition{{Type: string(hyperv1.AutoNodeEnabled), Status: metav1.ConditionFalse, Reason: hyperv1.AsExpectedReason}}},
		{name: "When AutoNode is progressing, it should require the expected reason", conditions: []metav1.Condition{{Type: string(hyperv1.AutoNodeEnabled), Status: metav1.ConditionTrue, Reason: hyperv1.AutoNodeProgressingReason}}},
		{name: "When AutoNode has converged, it should accept the matching condition", conditions: []metav1.Condition{{Type: string(hyperv1.AutoNodeEnabled), Status: metav1.ConditionTrue, Reason: hyperv1.AsExpectedReason}}, matches: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			hc := &hyperv1.HostedCluster{Status: hyperv1.HostedClusterStatus{Conditions: testCase.conditions}}
			matched, reason, err := autoNodeConditionPredicate[*hyperv1.HostedCluster](expected)(hc)
			if err != nil || matched != testCase.matches || reason == "" {
				t.Fatalf("got matched=%t, reason=%q, error=%v; want matched=%t with a diagnostic", matched, reason, err, testCase.matches)
			}
		})
	}
}

func TestUpdateAutoNodeObject(t *testing.T) {
	testCases := []struct {
		name         string
		missing      bool
		conflictOnce bool
		updateError  bool
	}{
		{name: "When an object exists, it should persist the requested mutation"},
		{name: "When an object is missing, it should propagate NotFound", missing: true},
		{name: "When a concurrent update conflicts, it should fetch again and retry", conflictOnce: true},
		{name: "When an update fails, it should propagate the error", updateError: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			object := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "autoscaler-settings"}}
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if !testCase.missing {
				builder = builder.WithObjects(object.DeepCopy())
			}
			calls := 0
			client := builder.WithInterceptorFuncs(interceptor.Funcs{Update: func(ctx context.Context, underlying crclient.WithWatch, current crclient.Object, options ...crclient.UpdateOption) error {
				calls++
				if testCase.updateError {
					return errors.New("update denied")
				}
				if testCase.conflictOnce && calls == 1 {
					return apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, current.GetName(), errors.New("concurrent update"))
				}
				return underlying.Update(ctx, current, options...)
			}}).Build()
			err := updateAutoNodeObject(t.Context(), client, object, func(current *corev1.ConfigMap) { current.Data = map[string]string{"expander": "random"} })
			if testCase.missing {
				if !apierrors.IsNotFound(err) {
					t.Fatalf("expected NotFound, got %v", err)
				}
				return
			}
			if testCase.updateError {
				if err == nil || err.Error() != "update denied" {
					t.Fatalf("expected update error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			current := &corev1.ConfigMap{}
			if err := client.Get(t.Context(), crclient.ObjectKeyFromObject(object), current); err != nil || current.Data["expander"] != "random" {
				t.Fatalf("mutation not persisted: object=%v, error=%v", current.Data, err)
			}
			if testCase.conflictOnce && calls != 2 {
				t.Fatalf("expected one retry, got %d update calls", calls)
			}
		})
	}
}
