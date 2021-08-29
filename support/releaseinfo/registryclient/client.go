package registryclient

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/docker/distribution/registry/client/transport"
	"k8s.io/client-go/rest"

	dockerarchive "github.com/openshift/hypershift/support/thirdparty/docker/pkg/archive"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/registryclient"
	"github.com/openshift/hypershift/support/thirdparty/oc/pkg/cli/image/manifest"
	"github.com/openshift/hypershift/support/thirdparty/oc/pkg/cli/image/manifest/dockercredentials"
)

// ExtractImageFiles extracts a list of files from a registry image given the image reference, pull secret and the
// list of files to extract. It returns a map with file contents or an error.
func ExtractImageFiles(ctx context.Context, imageRef string, pullSecret []byte, files ...string) (map[string][]byte, error) {
	rt, err := rest.TransportFor(&rest.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to create secure transport: %w", err)
	}
	insecureRT, err := rest.TransportFor(&rest.Config{TLSClientConfig: rest.TLSClientConfig{Insecure: true}})
	if err != nil {
		return nil, fmt.Errorf("failed to create insecure transport: %w", err)
	}
	credStore, err := dockercredentials.NewFromBytes(pullSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to parse docker credentials: %w", err)
	}
	registryContext := registryclient.NewContext(rt, insecureRT).WithCredentials(credStore).
		WithRequestModifiers(transport.NewHeaderRequestModifier(http.Header{http.CanonicalHeaderKey("User-Agent"): []string{rest.DefaultKubernetesUserAgent()}}))

	ref, err := reference.Parse(imageRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image reference %q: %w", imageRef, err)
	}
	repo, err := registryContext.Repository(ctx, ref.DockerClientDefaults().RegistryURL(), ref.RepositoryName(), false)
	if err != nil {
		return nil, fmt.Errorf("failed to create repository client for %s: %w", ref.DockerClientDefaults().RegistryURL(), err)
	}
	firstManifest, location, err := manifest.FirstManifest(ctx, ref, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain root manifest for %s: %w", imageRef, err)
	}
	_, layers, err := manifest.ManifestToImageConfig(ctx, firstManifest, repo.Blobs(ctx), location)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain image layers for %s: %w", imageRef, err)
	}

	fileContents := map[string][]byte{}
	for _, file := range files {
		fileContents[file] = nil
	}
	if len(fileContents) == 0 {
		return fileContents, nil
	}
	fromBlobs := repo.Blobs(ctx)
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
