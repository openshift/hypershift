package nodepool

import (
	"github.com/coreos/stream-metadata-go/stream"
)

// testAWSStream returns a minimal stream.Stream with a single AWS region/arch/AMI entry.
// Use this to reduce boilerplate in test cases that only need a simple AWS image lookup.
func testAWSStream(arch, region, ami string) *stream.Stream {
	return &stream.Stream{
		Architectures: map[string]stream.Arch{
			arch: {
				Images: stream.Images{
					Aws: &stream.AwsImage{
						Regions: map[string]stream.SingleImage{
							region: {Image: ami},
						},
					},
				},
			},
		},
	}
}

// testAWSStreamWithRelease returns a minimal stream.Stream with a single AWS
// region/arch/AMI entry plus the Release field set.
func testAWSStreamWithRelease(arch, region, ami, release string) *stream.Stream {
	return &stream.Stream{
		Architectures: map[string]stream.Arch{
			arch: {
				Images: stream.Images{
					Aws: &stream.AwsImage{
						Regions: map[string]stream.SingleImage{
							region: {Release: release, Image: ami},
						},
					},
				},
			},
		},
	}
}

func testOpenStackStream(release string) *stream.Stream {
	return &stream.Stream{
		Architectures: map[string]stream.Arch{
			"x86_64": {
				Artifacts: map[string]stream.PlatformArtifacts{
					"openstack": {Release: release},
				},
			},
		},
	}
}
