package util

import (
	"context"
	"fmt"
	"github.com/blang/semver/v4"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/releaseinfo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

// GetHypershiftComponentImage resolves the appropriate control plane operator
// image based on the following order of precedence (from most to least
// preferred):
//
// 1. The image specified by the ControlPlaneOperatorImageAnnotation on the
//    HostedControlPlane resource itself
// 2. The hypershift image specified in the release payload indicated by the
//    HostedControlPlane's release field
// 3. The hypershift-operator's own image for release versions 4.9 and 4.10
//
// If no image can be found according to these rules, an error is returned.
func GetHypershiftComponentImage(ctx context.Context, objectAnnotations map[string]string, releaseImage string, releaseProvider releaseinfo.Provider, hypershiftOperatorImage string, pullSecret []byte) (string, error) {
	if val, ok := objectAnnotations[hyperv1.ControlPlaneOperatorImageAnnotation]; ok {
		return val, nil
	}
	releaseInfo, err := releaseProvider.Lookup(ctx, releaseImage, pullSecret)
	if err != nil {
		return "", err
	}
	version, err := semver.Parse(releaseInfo.Version())
	if err != nil {
		return "", err
	}

	if hypershiftImage, exists := releaseInfo.ComponentImages()["hypershift"]; exists {
		return hypershiftImage, nil
	}

	versionMajMin := fmt.Sprintf("%d.%d", version.Major, version.Minor)
	switch versionMajMin {
	case "4.9", "4.10":
		return hypershiftOperatorImage, nil
	default:
		return "", fmt.Errorf("unsupported release image with version %s", versionMajMin)
	}
}

// LookupActiveContainerImage returns the image for a specified container in a specified pod
func LookupActiveContainerImage(ctx context.Context, pods v1.PodInterface, podName string, containerName string) (string, error) {
	podInfo, err := pods.Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get pod: %w", err)
	}
	for _, container := range podInfo.Spec.Containers {
		// can't use downward API to pass an image id so need to look it up
		if container.Name == containerName {
			return container.Image, nil
		}
	}
	return "", fmt.Errorf("couldn't locate image id in pod")
}
