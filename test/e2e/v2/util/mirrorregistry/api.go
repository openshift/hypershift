package mirrorregistry

import (
	"context"
	"errors"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/apimachinery/pkg/api/resource"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// DefaultNamespace is the namespace the registry is deployed to when
	// Options.Namespace is empty. The namespace is owned by the fixture and is
	// deleted by Teardown.
	DefaultNamespace = "test-mirror-registry"

	// DefaultName is the name used for the registry Deployment, Service,
	// Route, Secrets and PersistentVolumeClaim when Options.Name is empty.
	DefaultName = "mirror-registry"

	// DefaultRegistryImage is the registry server image used when
	// Options.Image is empty: the multi-arch manifest list of
	// quay.io/libpod/registry:2.8.2 (distribution registry 2.8.2), pinned by
	// digest. It is hosted outside Docker Hub to avoid rate limits and runs
	// under the restricted-v2 SCC with a random UID.
	DefaultRegistryImage = "quay.io/libpod/registry@sha256:e642e8604d305a3b82c8c1807b5df7a1a84cc650d57a60f9c5c2b78efec54b3f"

	// DefaultStorageSize is the PersistentVolumeClaim size used when
	// Options.Storage.Size is zero.
	DefaultStorageSize = "20Gi"

	// RegistryPort is the TLS port the registry container listens on and the
	// port exposed by the Service.
	RegistryPort = 5000
)

// errNotImplemented is returned by API skeleton methods until they are
// implemented.
var errNotImplemented = errors.New("mirrorregistry: not implemented")

// KeyPair is a PEM-encoded certificate and its PEM-encoded private key.
type KeyPair struct {
	// CertPEM is the PEM-encoded certificate.
	CertPEM []byte
	// KeyPEM is the PEM-encoded private key for CertPEM.
	KeyPEM []byte
}

// StorageOptions configures the PersistentVolumeClaim that stores registry
// content. Persistent storage is required: switching the registry to
// read-only mode restarts its pod, and content must survive the restart.
type StorageOptions struct {
	// Size is the PersistentVolumeClaim request. Zero means
	// DefaultStorageSize.
	Size resource.Quantity

	// StorageClassName is the StorageClass of the PersistentVolumeClaim.
	// Empty means the cluster default StorageClass.
	StorageClassName string
}

// Options configures a mirror registry. The zero value is valid except for
// CopierImage, which is required when the default Job-based copier is used.
type Options struct {
	// Namespace is the namespace the fixture creates and owns. Empty means
	// DefaultNamespace.
	Namespace string

	// Name is the name of all namespaced resources created by the fixture.
	// Empty means DefaultName.
	Name string

	// Image is the registry server image. Empty means DefaultRegistryImage.
	Image string

	// IngressDomain is the management cluster ingress domain used to build
	// the Route host "<Name>-<Namespace>.<IngressDomain>". Empty means it is
	// read from the cluster-scoped ingresses.config.openshift.io "cluster"
	// object.
	IngressDomain string

	// ExtraDNSNames are additional subject alternative names for the serving
	// certificate, for example a wildcard for the ingress domain.
	ExtraDNSNames []string

	// CA is the certificate authority used to sign the serving certificate.
	// Nil means the fixture generates a new CA. Supplying a CA allows the CA
	// to be distributed before the registry exists.
	CA *KeyPair

	// Storage configures registry content storage.
	Storage StorageOptions

	// CopierImage is an image containing the "oc" binary, used by the default
	// Job-based ImageCopier. Required when Copier is nil.
	CopierImage string

	// Copier copies images into the registry. Nil means the default
	// Job-based copier that runs CopierImage in Namespace.
	Copier ImageCopier
}

// ImageMapping requests that a single image be mirrored.
type ImageMapping struct {
	// From is the image to copy. It must be referenced by digest
	// (repository@sha256:...), because image digest mirror sets only redirect
	// by-digest pulls.
	From string

	// SourceRepository is the repository consumers reference and that the
	// returned image content sources map to the mirror. Empty means the
	// repository of From. Setting it to a repository that does not exist
	// upstream (for example under a ".invalid" host) proves that a successful
	// pull was served by the mirror.
	SourceRepository string
}

// MirrorRequest describes the content to mirror.
type MirrorRequest struct {
	// Images are individual images to mirror.
	Images []ImageMapping

	// ReleaseImages are OpenShift release payloads to mirror completely,
	// each referenced by digest.
	ReleaseImages []string

	// SourcePullSecret is a .dockerconfigjson document used to read source
	// images. Nil means sources are read anonymously.
	SourcePullSecret []byte
}

// MirroredImage reports where a requested image can be pulled from.
type MirroredImage struct {
	// From is the requested source image, as given in ImageMapping.From.
	From string
	// SourceRef is the by-digest reference consumers should use; it is
	// redirected to MirrorRef by the returned image content sources.
	SourceRef string
	// MirrorRef is the by-digest reference of the image on the public
	// registry endpoint.
	MirrorRef string
}

// Result describes a ready, read-only, publicly exposed mirror registry.
type Result struct {
	// Host is the public registry host (the Route host), without scheme.
	Host string

	// InternalHost is the in-cluster "<service>.<namespace>.svc:<port>"
	// address of the registry.
	InternalHost string

	// CABundlePEM is the PEM-encoded CA that signed the registry serving
	// certificate, suitable for HostedCluster.spec.additionalTrustBundle.
	CABundlePEM []byte

	// ImageContentSources maps every mirrored source repository to its mirror
	// repository on Host, suitable for HostedCluster.spec.imageContentSources.
	ImageContentSources []hyperv1.ImageContentSource

	// Images reports the mirrored individual images, in request order.
	Images []MirroredImage
}

// ImageCopy is a single image copy operation between full references.
type ImageCopy struct {
	// From is the by-digest source reference.
	From string
	// To is the destination repository on the internal registry address.
	// The digest is preserved.
	To string
}

// ReleaseCopy is a full release payload copy operation.
type ReleaseCopy struct {
	// From is the by-digest release image reference.
	From string
	// ToRepository is the destination repository on the internal registry
	// address that receives the release image and all of its components.
	ToRepository string
}

// CopyRequest is the input of an ImageCopier.
type CopyRequest struct {
	// Namespace is the namespace the copier may create workloads in.
	Namespace string
	// CABundlePEM is the CA that signed the destination registry certificate.
	CABundlePEM []byte
	// SourcePullSecret is a .dockerconfigjson document for reading sources,
	// or nil for anonymous access.
	SourcePullSecret []byte
	// Images are the individual image copies to perform.
	Images []ImageCopy
	// Releases are the release payload copies to perform.
	Releases []ReleaseCopy
}

// CopyResult is the output of an ImageCopier.
type CopyResult struct {
	// ReleaseSourceRepositories lists, for each ReleaseCopy.From, every
	// source repository the payload was read from (the release repository and
	// the repositories of its component images). The registry uses it to
	// build image content sources for release payloads.
	ReleaseSourceRepositories map[string][]string
}

// ImageCopier copies images into the registry while it is writable.
// Implementations must preserve image digests, including manifest-list
// digests.
type ImageCopier interface {
	// Copy performs all copies in req and returns when they have completed or
	// failed.
	Copy(ctx context.Context, req CopyRequest) (*CopyResult, error)
}

// Registry is a mirror registry fixture bound to a management cluster.
type Registry struct {
	client crclient.Client
	opts   Options
}

// New returns a Registry for the management cluster reachable through
// client. It applies defaults to opts and validates them; it does not contact
// the cluster.
func New(client crclient.Client, opts Options) (*Registry, error) {
	opts = opts.withDefaults()
	if err := opts.validate(); err != nil {
		return nil, err
	}
	return &Registry{client: client, opts: opts}, nil
}

// Setup deploys the registry, mirrors the content in req, switches the
// registry to read-only mode, exposes it through a passthrough Route and
// verifies the public endpoint over TLS using the registry CA. Setup is
// idempotent: calling it again reconciles all resources and mirrors req,
// temporarily re-enabling writes on the internal endpoint only.
func (r *Registry) Setup(ctx context.Context, req MirrorRequest) (*Result, error) {
	return nil, errNotImplemented
}

// Teardown deletes the namespace owned by the fixture and waits until it is
// gone. It returns nil if the namespace does not exist.
func (r *Registry) Teardown(ctx context.Context) error {
	return errNotImplemented
}
