package fake

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"

	imagev1 "github.com/openshift/api/image/v1"
	"github.com/openshift/hypershift/support/releaseinfo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ releaseinfo.ProviderWithRegistryOverrides = &FakeReleaseProvider{}

type FakeReleaseProvider struct {
	// Version of the returned release iamge. Defaults to 4.12.0 if unset.
	Version string
	// Allows image-based versioning
	ImageVersion map[string]string
}

func (f *FakeReleaseProvider) Lookup(ctx context.Context, image string, pullSecret []byte) (*releaseinfo.ReleaseImage, error) {
	releaseImage := &releaseinfo.ReleaseImage{
		ImageStream: &imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: "4.12.0"},
			Spec: imagev1.ImageStreamSpec{
				Tags: []imagev1.TagReference{
					{
						Name: "cluster-autoscaler",
						From: &corev1.ObjectReference{Name: ""},
					},
					{
						Name: "cluster-machine-approver",
						From: &corev1.ObjectReference{Name: ""},
					},
					{
						Name: "aws-cluster-api-controllers",
						From: &corev1.ObjectReference{Name: ""},
					},
					{
						Name: "cluster-capi-controllers",
						From: &corev1.ObjectReference{Name: ""},
					},
					{
						Name: util.AvailabilityProberImageName,
						From: &corev1.ObjectReference{Name: ""},
					},
				},
			},
		},
	}
	if len(f.ImageVersion) == 0 {
		if f.Version != "" {
			releaseImage.ImageStream.Name = f.Version
		}
		return releaseImage, nil
	}
	version, ok := f.ImageVersion[image]
	if !ok {
		return nil, fmt.Errorf("unable to lookup release image")
	}
	releaseImage.ImageStream.Name = version
	return releaseImage, nil
}

func (*FakeReleaseProvider) GetRegistryOverrides() map[string]string {
	return nil
}
