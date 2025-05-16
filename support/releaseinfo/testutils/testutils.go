package testutils

import (
	haproxy "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/apiserver-haproxy"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/util"

	imagev1 "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InitReleaseImageOrDie returns a ptr to releaseinfo.ReleaseImage,
// it panics in case no version, to initialize Name, is not supplied
// Only for testing purposes
func InitReleaseImageOrDie(version string) *releaseinfo.ReleaseImage {
	if version == "" {
		panic("undefined version for releaseinfo.ReleaseImage")
	}
	return &releaseinfo.ReleaseImage{
		ImageStream: &imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: version},
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
						From: &corev1.ObjectReference{Name: "capi-aws"},
					},
					{
						Name: "cluster-capi-controllers",
						From: &corev1.ObjectReference{Name: ""},
					},
					{
						Name: "azure-cluster-api-controllers",
						From: &corev1.ObjectReference{Name: "capi-azure"},
					},
					{
						Name: "openstack-cluster-api-controllers",
						From: &corev1.ObjectReference{Name: "capi-openstack"},
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
}
