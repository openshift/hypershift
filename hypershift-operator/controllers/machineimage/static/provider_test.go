package static

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func TestImage(t *testing.T) {
	hc := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				AWS: &hyperv1.AWSPlatformSpec{
					Region: "us-east-1",
				},
			},
		},
	}
	p := &StaticImageProvider{}
	image, err := p.Image(hc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if image != "ami-0d5f9982f029fbc14" {
		t.Fatalf("unexpected image: %s", image)
	}
}
