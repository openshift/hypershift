package registryclient

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/docker/distribution"
	"github.com/docker/distribution/manifest/manifestlist"
	"github.com/docker/distribution/registry/client/transport"
	"github.com/opencontainers/go-digest"
	"k8s.io/client-go/rest"

	dockerarchive "github.com/openshift/hypershift/support/thirdparty/docker/pkg/archive"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/registryclient"
	"github.com/openshift/hypershift/support/thirdparty/oc/pkg/cli/image/manifest"
	"github.com/openshift/hypershift/support/thirdparty/oc/pkg/cli/image/manifest/dockercredentials"
)

const (
	ArchitectureAMD64   = "amd64"
	ArchitectureS390X   = "s390x"
	ArchitecturePPC64LE = "ppc64le"
	ArchitectureARM64   = "arm64"
)

// ExtractImageFiles extracts a list of files from a registry image given the image reference, pull secret and the
// list of files to extract. It returns a map with file contents or an error.
func ExtractImageFiles(ctx context.Context, imageRef string, pullSecret []byte, files ...string) (map[string][]byte, error) {
	layers, fromBlobs, err := getMetadata(ctx, imageRef, pullSecret)
	if err != nil {
		return nil, err
	}

	fileContents := map[string][]byte{}
	for _, file := range files {
		fileContents[file] = nil
	}
	if len(fileContents) == 0 {
		return fileContents, nil
	}

	// Iterate over layers in reverse order to find the most recent version of files
	for i := len(layers) - 1; i >= 0; i-- {
		layer := layers[i]
		err := func() error {
			r, err := fromBlobs.Open(ctx, layer.Digest)
			if err != nil {
				return fmt.Errorf("unable to access the source layer %s: %v", layer.Digest, err)
			}
			defer r.Close()
			rc, err := dockerarchive.DecompressStream(r)
			if err != nil {
				return err
			}
			defer rc.Close()
			tr := tar.NewReader(rc)
			for {
				hdr, err := tr.Next()
				if err != nil {
					if err == io.EOF {
						break
					}
					return err
				}
				if hdr.Typeflag == tar.TypeReg {
					value, needFile := fileContents[hdr.Name]
					if !needFile {
						continue
					}
					// If value already assigned, the content was found in an earlier layer
					if value != nil {
						continue
					}
					out := &bytes.Buffer{}
					if _, err := io.Copy(out, tr); err != nil {
						return err
					}
					fileContents[hdr.Name] = out.Bytes()
				}
				if allFound(fileContents) {
					break
				}
			}
			return nil
		}()
		if err != nil {
			return nil, err
		}
		if allFound(fileContents) {
			break
		}
	}
	return fileContents, nil
}

func allFound(content map[string][]byte) bool {
	for _, v := range content {
		if v == nil {
			return false
		}
	}
	return true
}

func ExtractImageFile(ctx context.Context, imageRef string, pullSecret []byte, file string, out io.Writer) error {
	layers, fromBlobs, err := getMetadata(ctx, imageRef, pullSecret)
	if err != nil {
		return err
	}

	// Iterate over layers in reverse order to find the most recent version of files
	found := false
	for i := len(layers) - 1; i >= 0; i-- {
		layer := layers[i]
		err := func() error {
			r, err := fromBlobs.Open(ctx, layer.Digest)
			if err != nil {
				return fmt.Errorf("unable to access the source layer %s: %v", layer.Digest, err)
			}
			defer r.Close()
			rc, err := dockerarchive.DecompressStream(r)
			if err != nil {
				return err
			}
			defer rc.Close()
			tr := tar.NewReader(rc)
			for {
				hdr, err := tr.Next()
				if err != nil {
					if err == io.EOF {
						break
					}
					return err
				}
				if hdr.Typeflag == tar.TypeReg {
					if hdr.Name != file {
						continue
					}
					found = true
					if _, err := io.Copy(out, tr); err != nil {
						return err
					}
					return nil
				}
			}
			return nil
		}()
		if err != nil {
			return err
		}
		if found {
			return nil
		}
	}
	return fmt.Errorf("file not found")
}

func ExtractImageFilesToDir(ctx context.Context, imageRef string, pullSecret []byte, pattern string, outputDir string) error {
	regex, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid pattern: %w", err)
	}

	layers, fromBlobs, err := getMetadata(ctx, imageRef, pullSecret)
	if err != nil {
		return err
	}

	// Iterate over layers in reverse order to find the most recent version of files
	written := map[string]struct{}{}
	for i := len(layers) - 1; i >= 0; i-- {
		layer := layers[i]
		err := func() error {
			r, err := fromBlobs.Open(ctx, layer.Digest)
			if err != nil {
				return fmt.Errorf("unable to access the source layer %s: %v", layer.Digest, err)
			}
			defer r.Close()
			rc, err := dockerarchive.DecompressStream(r)
			if err != nil {
				return err
			}
			defer rc.Close()
			tr := tar.NewReader(rc)
			for {
				hdr, err := tr.Next()
				if err != nil {
					if err == io.EOF {
						break
					}
					return err
				}
				if hdr.Typeflag == tar.TypeReg {
					// Only copy the file once from the most recent layer
					if _, exists := written[hdr.Name]; exists {
						continue
					}
					if !regex.MatchString(hdr.Name) {
						continue
					}
					dst := filepath.Join(outputDir, hdr.Name)
					if err := os.MkdirAll(filepath.Clean(filepath.Dir(dst)), 0755); err != nil {
						return fmt.Errorf("failed to make dir: %w", err)
					}
					dstfd, err := os.Create(dst)
					if err != nil {
						return err
					}
					if _, err = io.Copy(dstfd, tr); err != nil {
						dstfd.Close()
						return err
					}
					dstfd.Close()
					written[hdr.Name] = struct{}{}
				}
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

func getMetadata(ctx context.Context, imageRef string, pullSecret []byte) ([]distribution.Descriptor, distribution.BlobStore, error) {
	repo, ref, err := GetRepoSetup(ctx, imageRef, pullSecret)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get repo setup: %w", err)
	}
	firstManifest, location, err := manifest.FirstManifest(ctx, *ref, repo)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to obtain root manifest for %s: %w", imageRef, err)
	}
	_, layers, err := manifest.ManifestToImageConfig(ctx, firstManifest, repo.Blobs(ctx), location)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to obtain image layers for %s: %w", imageRef, err)
	}
	return layers, repo.Blobs(ctx), nil
}

// GetRepoSetup connects to a repo and pulls the imageRef's docker image information from the repo. Returns the repo and the docker image.
func GetRepoSetup(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Repository, *reference.DockerImageReference, error) {
	var dockerImageRef *reference.DockerImageReference
	rt, err := rest.TransportFor(&rest.Config{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create secure transport: %w", err)
	}
	insecureRT, err := rest.TransportFor(&rest.Config{TLSClientConfig: rest.TLSClientConfig{Insecure: true}})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create insecure transport: %w", err)
	}
	credStore, err := dockercredentials.NewFromBytes(pullSecret)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse docker credentials: %w", err)
	}
	registryContext := registryclient.NewContext(rt, insecureRT).WithCredentials(credStore).
		WithRequestModifiers(transport.NewHeaderRequestModifier(http.Header{http.CanonicalHeaderKey("User-Agent"): []string{rest.DefaultKubernetesUserAgent()}}))

	ref, err := reference.Parse(imageRef)
	dockerImageRef = &ref
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse image reference %q: %w", imageRef, err)
	}
	repo, err := registryContext.Repository(ctx, ref.DockerClientDefaults().RegistryURL(), ref.RepositoryName(), false)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create repository client for %s: %w", ref.DockerClientDefaults().RegistryURL(), err)
	}
	return repo, dockerImageRef, nil
}

// GetManifest gets the manifest from an image
func GetManifest(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Manifest, error) {
	repo, ref, err := GetRepoSetup(ctx, imageRef, pullSecret)
	if err != nil {
		return nil, err
	}

	var srcDigest digest.Digest
	if len(ref.Tag) > 0 {
		desc, err := repo.Tags(ctx).Get(ctx, ref.Tag)
		if err != nil {
			return nil, err
		}
		srcDigest = desc.Digest
	}

	if len(ref.ID) > 0 {
		srcDigest = digest.Digest(ref.ID)
	}

	manifests, err := repo.Manifests(ctx)
	if err != nil {
		return nil, err
	}

	digestsManifest, err := manifests.Get(ctx, srcDigest, manifest.PreferManifestList)
	if err != nil {
		return nil, err
	}

	return digestsManifest, nil
}

// IsMultiArchManifestList determines whether an image is a manifest listed image and contains manifests the following processor architectures: amd64, arm64, s390x, ppc64le
func IsMultiArchManifestList(ctx context.Context, imageRef string, pullSecret []byte) (bool, error) {
	srcManifest, err := GetManifest(ctx, imageRef, pullSecret)
	if err != nil {
		return false, fmt.Errorf("failed to retrieve manifest %s: %w", imageRef, err)
	}

	mediaType, payload, err := srcManifest.Payload()
	if err != nil {
		return false, fmt.Errorf("failed to get payload %s: %w", imageRef, err)
	}

	// mediaType for manifest listed (aka fat manifest) images is either 'application/vnd.docker.distribution.manifest.list.v2+json' per the docker documentation - https://docs.docker.com/registry/spec/manifest-v2-2/
	// or 'application/vnd.oci.image.index.v1+json' per https://github.com/opencontainers/image-spec/blob/main/media-types.md
	if mediaType != "application/vnd.docker.distribution.manifest.list.v2+json" && mediaType != "application/vnd.oci.image.index.v1+json" {
		return false, nil
	}

	deserializedManifestList := new(manifestlist.DeserializedManifestList)
	if err = deserializedManifestList.UnmarshalJSON(payload); err != nil {
		return false, fmt.Errorf("failed to get unmarshalled manifest list: %w", err)
	}

	count := 0
	for _, arch := range deserializedManifestList.ManifestList.Manifests {
		switch arch.Platform.Architecture {
		case ArchitectureAMD64, ArchitectureS390X, ArchitecturePPC64LE, ArchitectureARM64:
			count = count + 1
		}
	}

	if count > 1 {
		return true, nil
	}
	return false, nil
}
