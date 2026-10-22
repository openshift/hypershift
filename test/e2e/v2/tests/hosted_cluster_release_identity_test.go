//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/blang/semver"
	"github.com/onsi/gomega"
	"github.com/opencontainers/go-digest"
)

type releaseDigestProviderFunc func(context.Context, string, []byte) (digest.Digest, *reference.DockerImageReference, error)

func (provider releaseDigestProviderFunc) GetDigest(ctx context.Context, image string, secret []byte) (digest.Digest, *reference.DockerImageReference, error) {
	return provider(ctx, image, secret)
}

func TestResolveReleaseImageIdentity(t *testing.T) {
	const image = "quay.io/release/payload:4.22.1"
	const manifestDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const canonical = "quay.io/release/payload@" + manifestDigest
	for _, testCase := range []struct {
		name, requested, observed string
		providerDigest            digest.Digest
		providerError             error
		nilReference              bool
		wantMatch, wantError      bool
	}{
		{name: "When CP status still reports the mutable tag, it should reject it", requested: image, observed: image, providerDigest: manifestDigest},
		{name: "When the tag resolves to the observed digest, it should match", requested: image, observed: canonical, providerDigest: manifestDigest, wantMatch: true},
		{name: "When DockerHub canonicalization defaults the library namespace, it should match producer String", requested: "docker.io/release:4.22.1", observed: "docker.io/library/release@" + manifestDigest, providerDigest: manifestDigest, wantMatch: true},
		{name: "When another digest is observed, it should reject it", requested: image, observed: "quay.io/release/payload@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", providerDigest: manifestDigest},
		{name: "When the digest appears in another repository, it should reject it", requested: image, observed: "quay.io/other/payload@" + manifestDigest, providerDigest: manifestDigest},
		{name: "When the digest appears in another registry, it should reject it", requested: image, observed: "mirror.example/release/payload@" + manifestDigest, providerDigest: manifestDigest},
		{name: "When the observed image is empty, it should reject it", requested: image, providerDigest: manifestDigest},
		{name: "When the requested image is pinned, it should not need metadata", requested: canonical, observed: canonical, wantMatch: true},
		{name: "When the registry fails, it should propagate failure", requested: image, providerError: errors.New("registry unavailable"), wantError: true},
		{name: "When metadata has no reference, it should reject it", requested: image, providerDigest: manifestDigest, nilReference: true, wantError: true},
		{name: "When metadata has no digest, it should reject it", requested: image, wantError: true},
		{name: "When the digest is malformed, it should reject it", requested: "quay.io/release/payload@sha256:bad", wantError: true},
		{name: "When the requested image is empty, it should reject it", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			called := false
			provider := releaseDigestProviderFunc(func(_ context.Context, requested string, secret []byte) (digest.Digest, *reference.DockerImageReference, error) {
				called = true
				if requested != testCase.requested || string(secret) != "fixture" {
					t.Fatal("release metadata lookup did not receive requested image and pull secret")
				}
				parsed, err := reference.Parse(requested)
				if err != nil {
					t.Fatal(err)
				}
				if testCase.nilReference {
					return testCase.providerDigest, nil, testCase.providerError
				}
				return testCase.providerDigest, &parsed, testCase.providerError
			})
			identity, err := resolveReleaseImageIdentity(t.Context(), provider, testCase.requested, []byte("fixture"))
			if (err != nil) != testCase.wantError {
				t.Fatalf("unexpected resolution error: %v", err)
			}
			if testCase.requested == canonical && called {
				t.Fatal("pinned image should not perform a registry lookup")
			}
			if err == nil && identity.Matches(testCase.observed) != testCase.wantMatch {
				t.Fatalf("unexpected image match for %q: %#v", testCase.observed, identity)
			}
			if err == nil && testCase.observed == image && !identity.MatchesDataPlane(image) {
				t.Fatal("legacy CVO status may preserve the requested image tag")
			}
		})
	}
}

func TestHostedReleaseImageIdentityPrerequisites(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := hyperv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	management := fake.NewClientBuilder().WithScheme(scheme).Build()
	if _, err := hostedReleaseImageIdentity(t.Context(), management, nil, "quay.io/release/payload:4.22.1"); err == nil {
		t.Fatal("tag lookup must require its HostedCluster")
	}
	cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}}
	cluster.Spec.PullSecret.Name = "missing"
	if _, err := hostedReleaseImageIdentity(t.Context(), management, cluster, "quay.io/release/payload:4.22.1"); err == nil {
		t.Fatal("tag lookup must reject an unavailable pull secret")
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: cluster.Namespace, Name: cluster.Spec.PullSecret.Name}, Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("fixture")}}
	if err := management.Create(t.Context(), secret); err != nil {
		t.Fatal(err)
	}
	called := false
	provider := releaseDigestProviderFunc(func(_ context.Context, image string, secret []byte) (digest.Digest, *reference.DockerImageReference, error) {
		called = true
		if string(secret) != "fixture" {
			t.Fatal("metadata lookup must receive the HostedCluster pull secret")
		}
		parsed, err := reference.Parse(image)
		return "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", &parsed, err
	})
	if _, err := hostedReleaseImageIdentityWithProvider(t.Context(), management, cluster, "quay.io/release/payload:4.22.1", provider); err != nil || !called {
		t.Fatalf("expected native metadata lookup with the HostedCluster pull secret: %v", err)
	}
}

func TestHostedClusterCanonicalUpgradeComplete(t *testing.T) {
	const image = "quay.io/release/payload:4.22.1"
	const manifestDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const canonical = "quay.io/release/payload@" + manifestDigest
	const wrong = "quay.io/release/payload@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	provider := releaseDigestProviderFunc(func(context.Context, string, []byte) (digest.Digest, *reference.DockerImageReference, error) {
		parsed, err := reference.Parse(image)
		return manifestDigest, &parsed, err
	})
	identity, err := resolveReleaseImageIdentity(t.Context(), provider, image, nil)
	if err != nil {
		t.Fatal(err)
	}
	targetVersion := semver.MustParse("4.22.1")
	identity.targetVersion = &targetVersion
	for _, testCase := range []struct {
		name   string
		mutate func(*hyperv1.HostedCluster)
	}{
		{name: "When both histories report the canonical target, it should pass"},
		{name: "When CP desired remains a raw tag after resolution, it should fail", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.ControlPlaneVersion.Desired.Image = image }},
		{name: "When CP completed history remains a raw tag after resolution, it should fail", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.ControlPlaneVersion.History[0].Image = image }},
		{name: "When CVO desired digest is wrong, it should fail", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version.Desired.Image = wrong }},
		{name: "When CVO history is stale despite target desired, it should fail", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.Version.History[0].Image = wrong }},
		{name: "When CP desired digest is wrong, it should fail", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.ControlPlaneVersion.Desired.Image = wrong }},
		{name: "When CP history is stale despite target desired, it should fail", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Status.ControlPlaneVersion.History[0].Image = wrong }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "clusters"}, Status: hyperv1.HostedClusterStatus{
				Conditions:          []metav1.Condition{{Type: string(hyperv1.HostedClusterAvailable), Status: metav1.ConditionTrue}, {Type: string(hyperv1.HostedClusterProgressing), Status: metav1.ConditionFalse}},
				Version:             &hyperv1.ClusterVersionStatus{Desired: configv1.Release{Image: canonical}, History: []configv1.UpdateHistory{{Image: canonical, Version: "4.22.1", State: configv1.CompletedUpdate}}},
				ControlPlaneVersion: hyperv1.ControlPlaneVersionStatus{Desired: configv1.Release{Image: canonical}, History: []hyperv1.ControlPlaneUpdateHistory{{Image: canonical, State: configv1.CompletedUpdate}}},
			}}
			if testCase.mutate != nil {
				testCase.mutate(cluster)
			}
			if testCase.mutate == nil {
				legacy := cluster.DeepCopy()
				legacy.Status.Version.Desired.Image = image
				legacy.Status.Version.History[0].Image = image
				if err := hostedClusterUpgradeCompleteForIdentity(legacy, nil, identity); err != nil {
					t.Fatalf("legacy CVO tag status must not weaken canonical CP enforcement: %v", err)
				}
			}
			failed := false
			assertions := gomega.NewGomega(func(string, ...int) { failed = true })
			assertions.Expect(hostedClusterUpgradeCompleteForIdentity(cluster, nil, identity)).To(gomega.Succeed())
			if failed != (testCase.mutate != nil) {
				t.Fatalf("unexpected canonical upgrade predicate result: failed=%t", failed)
			}
		})
	}
}
