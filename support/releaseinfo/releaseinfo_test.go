package releaseinfo

import (
	"testing"

	"github.com/openshift/hypershift/support/releaseinfo/fixtures"
)

// TestReleaseInfoPowerVS test validates the presence of the powervs images in the 4.10 release
func TestReleaseInfoPowerVS(t *testing.T) {
	metadata, err := DeserializeImageMetadata(fixtures.CoreOSBootImagesYAML_4_10)
	if err != nil {
		t.Fatal(err)
	}
	arch, ok := metadata.Architectures["ppc64le"]
	if !ok {
		t.Fatal("metadata does not contain the ppc64le architecture")
	}
	if len(arch.Images.PowerVS.Regions) == 0 {
		t.Fatal("metadata does not contain any powervs regions")
	}
	for _, region := range arch.Images.PowerVS.Regions {
		if region.Release == "" || region.Object == "" || region.Bucket == "" || region.URL == "" {
			t.Fatalf("none of the fields in the image can be empty: %+v", region)
		}
	}
}
