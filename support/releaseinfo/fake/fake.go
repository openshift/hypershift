package fake

import (
	"context"

	corev1 "k8s.io/api/core/v1"

	imagev1 "github.com/openshift/api/image/v1"
	"github.com/openshift/hypershift/support/releaseinfo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ releaseinfo.ProviderWithRegistryOverrides = &FakeReleaseProvider{}

type FakeReleaseProvider struct{}

func (*FakeReleaseProvider) Lookup(ctx context.Context, image string, pullSecret []byte) (*releaseinfo.ReleaseImage, error) {
	return &releaseinfo.ReleaseImage{
		ImageStream: &imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: "4.10.0"},
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
				},
			},
		},
	}, nil
}

func (*FakeReleaseProvider) GetRegistryOverrides() map[string]string {
	return nil
}
