package registryclient

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"

	"github.com/docker/distribution"
	"github.com/docker/distribution/manifest/manifestlist"
	"github.com/opencontainers/go-digest"
)

const (
	ReleaseImage1         = "quay.io/openshift-release-dev/ocp-release@sha256:1a101ef5215da468cea8bd2eb47114e85b2b64a6b230d5882f845701f55d057f"
	ReleaseImage2         = "quay.io/openshift-release-dev/ocp-release:4.11.0-0.nightly-multi-2022-07-12-131716"
	ManifestMediaType     = "application/vnd.docker.distribution.manifest.v2+json"
	ManifestListMediaType = "application/vnd.docker.distribution.manifest.list.v2+json"
	ImageIndexMediaType   = "application/vnd.oci.image.index.v1+json"
	LinuxOS               = "linux"
)

type fakeRegistryClientImageMetadataProvider struct {
	mediaType string
	result    *dockerv1client.DockerImageConfig
	digest    string
	ref       *reference.DockerImageReference
}
type fakeManifest struct {
	mediaType string
}

func (f *fakeRegistryClientImageMetadataProvider) ImageMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error) {
	return f.result, nil
}

func (f *fakeRegistryClientImageMetadataProvider) GetManifest(ctx context.Context, imageRef string, pullSecret []byte) (distribution.Manifest, error) {
	_, _, err := GetRepoSetup(ctx, imageRef, pullSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve manifest %s: %w", imageRef, err)
	}
	return &fakeManifest{
		f.mediaType,
	}, nil
}

func (f *fakeRegistryClientImageMetadataProvider) GetDigest(ctx context.Context, imageRef string, pullSecret []byte) (digest.Digest, *reference.DockerImageReference, error) {
	var err error
	_, f.ref, err = GetRepoSetup(ctx, imageRef, pullSecret)
	if err != nil {
		return "", nil, fmt.Errorf("failed to retrieve manifest %s: %w", imageRef, err)
	}
	f.ref.ID = f.digest
	return digest.Digest(f.digest), f.ref, nil
}

func (f *fakeRegistryClientImageMetadataProvider) GetMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, []distribution.Descriptor, distribution.BlobStore, error) {
	return f.result, []distribution.Descriptor{}, nil, nil
}

type FakeManifest struct{}

func (f *fakeManifest) References() []distribution.Descriptor { return []distribution.Descriptor{} }
func (f *fakeManifest) Payload() (string, []byte, error)      { return f.mediaType, []byte{}, nil }

func TestFindMatchingManifest(t *testing.T) {
	deserializedManifestList1 := &manifestlist.DeserializedManifestList{
		ManifestList: manifestlist.ManifestList{
			Manifests: []manifestlist.ManifestDescriptor{
				{
					Descriptor: distribution.Descriptor{
						MediaType: ManifestMediaType,
						Digest:    "sha256:70fb4524d21e1b6c08477eb5d1ca2cf282b3270b1d008f70dd7e1cf13d8ba4ce",
					},
					Platform: manifestlist.PlatformSpec{
						Architecture: ArchitectureAMD64,
						OS:           LinuxOS,
					},
				},
				{
					Descriptor: distribution.Descriptor{
						MediaType: ManifestMediaType,
						Digest:    "sha256:f8dcd1dadc68b85ccf8737067f73fc03b0f6a1d81633fbdcdde2e3b5bc804d6a",
					},
					Platform: manifestlist.PlatformSpec{
						Architecture: ArchitectureS390X,
						OS:           LinuxOS,
					},
				},
				{
					Descriptor: distribution.Descriptor{
						MediaType: ManifestMediaType,
						Digest:    "sha256:a46358bdcf31d39c23e7389e8b75d1e5efa7181cca8832e51697b6bb3470e4a5",
					},
					Platform: manifestlist.PlatformSpec{
						Architecture: ArchitecturePPC64LE,
						OS:           LinuxOS,
					},
				},
				{
					Descriptor: distribution.Descriptor{
						MediaType: ManifestMediaType,
						Digest:    "sha256:4fe15a54f144d0200a39a93e2dc97b8b0e989e95cc076acbe2dfe129d0c04831",
					},
					Platform: manifestlist.PlatformSpec{
						Architecture: ArchitectureARM64,
						OS:           LinuxOS,
					},
				},
			},
		},
	}

	deserializedManifestList2 := &manifestlist.DeserializedManifestList{
		ManifestList: manifestlist.ManifestList{
			Manifests: []manifestlist.ManifestDescriptor{
				{
					Descriptor: distribution.Descriptor{
						MediaType: ManifestMediaType,
						Digest:    "sha256:b593c6882f9c8d9d75f3d200fa3e02f7f8caa99cea595fd70bbdd495613fd23f",
					},
					Platform: manifestlist.PlatformSpec{
						Architecture: ArchitectureAMD64,
						OS:           LinuxOS,
					},
				},
				{
					Descriptor: distribution.Descriptor{
						MediaType: ManifestMediaType,
						Digest:    "sha256:be53e6c50f1c97b4b34b341fada995f1e0c6c5e8305f3f373b9356ba82cc3d22",
					},
					Platform: manifestlist.PlatformSpec{
						Architecture: ArchitectureS390X,
						OS:           LinuxOS,
					},
				},
				{
					Descriptor: distribution.Descriptor{
						MediaType: ManifestMediaType,
						Digest:    "sha256:a0f3d715a8947e45bdc9c9d2c1fcdccf8da6b216cb6efc38d75cec49a56f074b",
					},
					Platform: manifestlist.PlatformSpec{
						Architecture: ArchitecturePPC64LE,
						OS:           LinuxOS,
					},
				},
				{
					Descriptor: distribution.Descriptor{
						MediaType: ManifestMediaType,
						Digest:    "sha256:f1c97cf57c57757fcd6d4314ff4b4cc792b27b904e949b840f902c104f1acf38",
					},
					Platform: manifestlist.PlatformSpec{
						Architecture: ArchitectureARM64,
						OS:           LinuxOS,
					},
				},
			},
		},
	}

	tests := []struct {
		testName                 string
		releaseImage             string
		deserializedManifestList *manifestlist.DeserializedManifestList
		osToFind                 string
		archToFind               string
		expectedImageRef         string
	}{
		{
			testName:                 "Find linux/amd64 in multi-arch ReleaseImage1",
			releaseImage:             ReleaseImage1,
			deserializedManifestList: deserializedManifestList1,
			osToFind:                 LinuxOS,
			archToFind:               ArchitectureAMD64,
			expectedImageRef:         "quay.io/openshift-release-dev/ocp-release@sha256:70fb4524d21e1b6c08477eb5d1ca2cf282b3270b1d008f70dd7e1cf13d8ba4ce",
		},
		{
			testName:                 "Find linux/arm64 in multi-arch ReleaseImage1",
			releaseImage:             ReleaseImage1,
			deserializedManifestList: deserializedManifestList1,
			osToFind:                 LinuxOS,
			archToFind:               ArchitectureARM64,
			expectedImageRef:         "quay.io/openshift-release-dev/ocp-release@sha256:4fe15a54f144d0200a39a93e2dc97b8b0e989e95cc076acbe2dfe129d0c04831",
		},
		{
			testName:                 "Find linux/ppc64le in multi-arch ReleaseImage1",
			releaseImage:             ReleaseImage1,
			deserializedManifestList: deserializedManifestList1,
			osToFind:                 LinuxOS,
			archToFind:               ArchitecturePPC64LE,
			expectedImageRef:         "quay.io/openshift-release-dev/ocp-release@sha256:a46358bdcf31d39c23e7389e8b75d1e5efa7181cca8832e51697b6bb3470e4a5",
		},
		{
			testName:                 "Find linux/s390x in multi-arch ReleaseImage1",
			releaseImage:             ReleaseImage1,
			deserializedManifestList: deserializedManifestList1,
			osToFind:                 LinuxOS,
			archToFind:               ArchitectureS390X,
			expectedImageRef:         "quay.io/openshift-release-dev/ocp-release@sha256:f8dcd1dadc68b85ccf8737067f73fc03b0f6a1d81633fbdcdde2e3b5bc804d6a",
		},
		{
			testName:                 "Find linux/amd64 in multi-arch ReleaseImage2",
			releaseImage:             ReleaseImage2,
			deserializedManifestList: deserializedManifestList2,
			osToFind:                 LinuxOS,
			archToFind:               ArchitectureAMD64,
			expectedImageRef:         "quay.io/openshift-release-dev/ocp-release@sha256:b593c6882f9c8d9d75f3d200fa3e02f7f8caa99cea595fd70bbdd495613fd23f",
		},
		{
			testName:                 "Find linux/arm64 in multi-arch ReleaseImage2",
			releaseImage:             ReleaseImage2,
			deserializedManifestList: deserializedManifestList2,
			osToFind:                 LinuxOS,
			archToFind:               ArchitectureARM64,
			expectedImageRef:         "quay.io/openshift-release-dev/ocp-release@sha256:f1c97cf57c57757fcd6d4314ff4b4cc792b27b904e949b840f902c104f1acf38",
		},
		{
			testName:                 "Find linux/ppc64le in multi-arch ReleaseImage2",
			releaseImage:             ReleaseImage2,
			deserializedManifestList: deserializedManifestList2,
			osToFind:                 LinuxOS,
			archToFind:               ArchitecturePPC64LE,
			expectedImageRef:         "quay.io/openshift-release-dev/ocp-release@sha256:a0f3d715a8947e45bdc9c9d2c1fcdccf8da6b216cb6efc38d75cec49a56f074b",
		},
		{
			testName:                 "Find linux/s390x in multi-arch ReleaseImage2",
			releaseImage:             ReleaseImage2,
			deserializedManifestList: deserializedManifestList2,
			osToFind:                 LinuxOS,
			archToFind:               ArchitectureS390X,
			expectedImageRef:         "quay.io/openshift-release-dev/ocp-release@sha256:be53e6c50f1c97b4b34b341fada995f1e0c6c5e8305f3f373b9356ba82cc3d22",
		},
	}

	for _, tc := range tests {
		t.Run(tc.testName, func(t *testing.T) {
			g := NewWithT(t)

			imageRef, err := findMatchingManifest(t.Context(), tc.releaseImage, tc.deserializedManifestList, tc.osToFind, tc.archToFind)
			g.Expect(err).To(BeNil())
			g.Expect(imageRef).To(Equal(tc.expectedImageRef))
		})
	}
}

func TestIsMultiArchManifestList(t *testing.T) {
	pullSecretBytes := []byte("{\"auths\":{\"quay.io\":{\"auth\":\"\",\"email\":\"\"}}}")
	testCases := []struct {
		name                   string
		image                  string
		pullSecretBytes        []byte
		mediaType              string
		manifests              []manifestlist.ManifestDescriptor
		expectedMultiArchImage bool
		expectErr              bool
	}{
		{
			name:                   "Check an amd64 image; no err",
			image:                  "quay.io/openshift-release-dev/ocp-release:4.16.10-x86_64",
			mediaType:              ManifestMediaType,
			pullSecretBytes:        pullSecretBytes,
			expectedMultiArchImage: false,
			expectErr:              false,
			manifests: []manifestlist.ManifestDescriptor{
				{
					Descriptor: distribution.Descriptor{
						MediaType: ManifestMediaType,
						Digest:    "sha256:70fb4524d21e1b6c08477eb5d1ca2cf282b3270b1d008f70dd7e1cf13d8ba4ce",
					},
					Platform: manifestlist.PlatformSpec{
						Architecture: ArchitectureAMD64,
						OS:           LinuxOS,
					},
				},
			},
		},
		{
			name:                   "Check a ppc64le image; no err",
			image:                  "quay.io/openshift-release-dev/ocp-release:4.16.11-ppc64le",
			mediaType:              ManifestMediaType,
			pullSecretBytes:        pullSecretBytes,
			expectedMultiArchImage: false,
			expectErr:              false,
			manifests: []manifestlist.ManifestDescriptor{
				{
					Descriptor: distribution.Descriptor{
						MediaType: ManifestMediaType,
						Digest:    "sha256:70fb4524d21e1b6c08477eb5d1ca2cf282b3270b1d008f70dd7e1cf13d8ba4ce",
					},
					Platform: manifestlist.PlatformSpec{
						Architecture: ArchitecturePPC64LE,
						OS:           LinuxOS,
					},
				},
			},
		},
		{
			name:                   "Check a multi-arch image; no err",
			image:                  "quay.io/openshift-release-dev/ocp-release:4.16.11-multi",
			mediaType:              ManifestListMediaType,
			pullSecretBytes:        pullSecretBytes,
			expectedMultiArchImage: true,
			expectErr:              false,
			manifests: []manifestlist.ManifestDescriptor{
				{
					Descriptor: distribution.Descriptor{
						MediaType: ManifestListMediaType,
						Digest:    "sha256:70fb4524d21e1b6c08477eb5d1ca2cf282b3270b1d008f70dd7e1cf13d8ba4ce",
					},
					Platform: manifestlist.PlatformSpec{
						Architecture: ArchitectureAMD64,
						OS:           LinuxOS,
					},
				},
				{
					Descriptor: distribution.Descriptor{
						MediaType: ManifestListMediaType,
						Digest:    "sha256:70fb4524d21e1b6c08477eb5d1ca2cf282b3270b1d008f70dd7e1cf13d8ba4ce",
					},
					Platform: manifestlist.PlatformSpec{
						Architecture: ArchitecturePPC64LE,
						OS:           LinuxOS,
					},
				},
			},
		},
		{
			name:                   "Bad pull secret; err",
			image:                  "quay.io/openshift-release-dev/ocp-release:4.16.11-ppc64le",
			mediaType:              ManifestMediaType,
			pullSecretBytes:        []byte(""),
			expectedMultiArchImage: false,
			expectErr:              true,
			manifests: []manifestlist.ManifestDescriptor{
				{
					Descriptor: distribution.Descriptor{
						MediaType: ManifestMediaType,
						Digest:    "sha256:70fb4524d21e1b6c08477eb5d1ca2cf282b3270b1d008f70dd7e1cf13d8ba4ce",
					},
					Platform: manifestlist.PlatformSpec{
						Architecture: ArchitecturePPC64LE,
						OS:           LinuxOS,
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			deserializeFunc := func(payload []byte) (*manifestlist.DeserializedManifestList, error) {
				return &manifestlist.DeserializedManifestList{
					ManifestList: manifestlist.ManifestList{
						Manifests: tc.manifests,
					},
				}, nil
			}
			ctx := context.WithValue(t.Context(), DeserializeFuncName, deserializeFunc)
			imageMetadataProvider := &fakeRegistryClientImageMetadataProvider{
				mediaType: tc.mediaType,
			}

			isMultiArchImage, err := IsMultiArchManifestList(ctx, tc.image, tc.pullSecretBytes, imageMetadataProvider)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(isMultiArchImage).To(Equal(tc.expectedMultiArchImage))
			}
		})
	}
}
