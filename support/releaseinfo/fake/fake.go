package fake

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	haproxy "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/apiserver-haproxy"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/util"

	imagev1 "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var _ releaseinfo.ProviderWithRegistryOverrides = &FakeReleaseProvider{}

type FakeReleaseProvider struct {
	// Version of the returned release image. Defaults to 4.14.0 if unset.
	Version string
	// Allows image-based versioning
	ImageVersion map[string]string
	Components   map[string]string
}

func (f *FakeReleaseProvider) Lookup(_ context.Context, image string, _ []byte) (*releaseinfo.ReleaseImage, error) {
	releaseImage := &releaseinfo.ReleaseImage{
		ImageStream: &imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: "4.18.0"},
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
						Name: "azure-cluster-api-controllers",
						From: &corev1.ObjectReference{Name: ""},
					},
					{
						Name: "openstack-cluster-api-controllers",
						From: &corev1.ObjectReference{Name: ""},
					},
					{
						Name: util.AvailabilityProberImageName,
						From: &corev1.ObjectReference{Name: ""},
					},
					{
						Name: haproxy.HAProxyRouterImageName,
						From: &corev1.ObjectReference{Name: ""},
					},
				},
			},
		},
		StreamMetadata: &releaseinfo.CoreOSStreamMetadata{
			Architectures: map[string]releaseinfo.CoreOSArchitecture{
				"x86_64": {
					Images: releaseinfo.CoreOSImages{
						AWS: releaseinfo.CoreOSAWSImages{
							Regions: map[string]releaseinfo.CoreOSAWSImage{
								"us-east-1": {
									Release: "us-east-1-x86_64-release",
									Image:   "us-east-1-x86_64-image",
								},
							},
						},
					},
				},
				"aarch64": {
					Images: releaseinfo.CoreOSImages{
						AWS: releaseinfo.CoreOSAWSImages{
							Regions: map[string]releaseinfo.CoreOSAWSImage{
								"us-east-1": {
									Release: "us-east-1-aarch64-release",
									Image:   "us-east-1-aarch64-image",
								},
								"us-west-1": {
									Release: "us-west-1-aarch64-release",
									Image:   "",
								},
							},
						},
					},
				},
			},
		},
	}

	for name, image := range f.Components {
		releaseImage.Spec.Tags = append(releaseImage.Spec.Tags, imagev1.TagReference{
			Name: name,
			From: &corev1.ObjectReference{Name: image},
		})
	}

	if len(f.ImageVersion) == 0 {
		if f.Version != "" {
			releaseImage.Name = f.Version
		}
		return releaseImage, nil
	}
	version, ok := f.ImageVersion[image]
	if !ok {
		return nil, fmt.Errorf("unable to lookup release image")
	}
	releaseImage.Name = version
	return releaseImage, nil
}

func (*FakeReleaseProvider) GetRegistryOverrides() map[string]string {
	return nil
}

func (*FakeReleaseProvider) GetOpenShiftImageRegistryOverrides() map[string][]string {
	return nil
}

func (*FakeReleaseProvider) GetMirroredReleaseImage() string {
	return ""
}

func GetReleaseImage(ctx context.Context, hc *hyperv1.HostedCluster, client crclient.WithWatch, releaseProvider *FakeReleaseProvider) *releaseinfo.ReleaseImage {
	var pullSecret corev1.Secret
	if err := client.Get(ctx, types.NamespacedName{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name}, &pullSecret); err != nil {
		return nil
	}
	pullSecretBytes, ok := pullSecret.Data[corev1.DockerConfigJsonKey]
	if !ok {
		return nil
	}
	releaseInfo, err := releaseProvider.Lookup(ctx, hc.Spec.Release.Image, pullSecretBytes)
	if err != nil {
		return nil
	}

	return releaseInfo
}
