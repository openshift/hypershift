//go:build e2ev2

package tests

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/imageregistry"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"

	corev1 "k8s.io/api/core/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
	"github.com/opencontainers/go-digest"
)

type releaseImageDigestProvider interface {
	GetDigest(context.Context, string, []byte) (digest.Digest, *reference.DockerImageReference, error)
}

type releaseImageIdentity struct {
	Requested     string
	Canonical     string
	targetVersion *semver.Version
}

func (identity releaseImageIdentity) Matches(image string) bool {
	return image != "" && identity.Canonical != "" && image == identity.Canonical
}

func (identity releaseImageIdentity) MatchesDataPlane(image string) bool {
	return identity.Matches(image) || image != "" && image == identity.Requested
}

func hostedReleaseImageIdentity(ctx context.Context, management crclient.Client, cluster *hyperv1.HostedCluster, image string) (releaseImageIdentity, error) {
	return hostedReleaseImageIdentityWithProvider(ctx, management, cluster, image, &imageregistry.RegistryClientImageMetadataProvider{})
}

func hostedReleaseImageIdentityWithProvider(ctx context.Context, management crclient.Client, cluster *hyperv1.HostedCluster, image string, provider releaseImageDigestProvider) (releaseImageIdentity, error) {
	parsed, err := reference.Parse(image)
	if err != nil || image == "" {
		return releaseImageIdentity{}, fmt.Errorf("requested release image must be a valid pullspec")
	}
	var pullSecret []byte
	if parsed.ID == "" {
		if cluster == nil {
			return releaseImageIdentity{}, fmt.Errorf("resolving a release tag requires its HostedCluster")
		}
		secret := &corev1.Secret{}
		if err := management.Get(ctx, crclient.ObjectKey{Namespace: cluster.Namespace, Name: cluster.Spec.PullSecret.Name}, secret); err != nil {
			return releaseImageIdentity{}, fmt.Errorf("reading release lookup pull secret: %w", err)
		}
		pullSecret = secret.Data[corev1.DockerConfigJsonKey]
	}
	return resolveReleaseImageIdentity(ctx, provider, image, pullSecret)
}

func resolveReleaseImageIdentity(ctx context.Context, provider releaseImageDigestProvider, image string, pullSecret []byte) (releaseImageIdentity, error) {
	parsed, err := reference.Parse(image)
	if err != nil || image == "" {
		return releaseImageIdentity{}, fmt.Errorf("requested release image must be a valid pullspec")
	}
	imageDigest := digest.Digest(parsed.ID)
	if imageDigest == "" {
		imageDigest, parsedReference, err := provider.GetDigest(ctx, image, pullSecret)
		if err != nil {
			return releaseImageIdentity{}, fmt.Errorf("resolving release image digest: %w", err)
		}
		if parsedReference == nil || parsedReference.Name == "" || imageDigest.Validate() != nil {
			return releaseImageIdentity{}, fmt.Errorf("release metadata must identify a valid manifest digest")
		}
		parsed = *parsedReference
		parsed.ID = string(imageDigest)
		return releaseImageIdentity{Requested: image, Canonical: parsed.String()}, nil
	}
	if imageDigest.Validate() != nil {
		return releaseImageIdentity{}, fmt.Errorf("requested release image contains an invalid manifest digest")
	}
	return releaseImageIdentity{Requested: image, Canonical: parsed.String()}, nil
}
