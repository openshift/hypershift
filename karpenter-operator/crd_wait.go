package main

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

// standaloneKarpenterCRDNames returns the CRD names the adapter waits for,
// based on the platform.
func standaloneKarpenterCRDNames(platform hyperv1.PlatformType) []string {
	common := []string{
		"nodepools.karpenter.sh",
		"nodeclaims.karpenter.sh",
	}
	switch platform {
	case hyperv1.AWSPlatform:
		return append(common, "ec2nodeclasses.karpenter.k8s.aws")
	case hyperv1.AzurePlatform:
		return append(common, "aksnodeclasses.karpenter.azure.com")
	default:
		return common
	}
}

// waitForKarpenterCRDs waits for CRDs owned by standalone operator before adapter registers typed watches.
func waitForKarpenterCRDs(ctx context.Context, cfg *rest.Config, platform hyperv1.PlatformType) error {
	crdNames := standaloneKarpenterCRDNames(platform)
	setupLog.Info("waiting for standalone Karpenter CRDs to become established", "crds", crdNames)

	clientset, err := apiextensionsclientset.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("failed to create API extensions client: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	err = wait.PollUntilContextCancel(waitCtx, 100*time.Millisecond, true, func(ctx context.Context) (bool, error) {
		for _, name := range crdNames {
			crd, err := clientset.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) || isTransientAPIError(err) {
				return false, nil
			}
			if err != nil {
				return false, err
			}

			established := false
			for _, condition := range crd.Status.Conditions {
				if condition.Type == apiextensionsv1.Established && condition.Status == apiextensionsv1.ConditionTrue {
					established = true
					break
				}
			}
			if !established {
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return err
	}

	setupLog.Info("standalone Karpenter CRDs established")
	return nil
}

func isTransientAPIError(err error) bool {
	return apierrors.IsTimeout(err) ||
		apierrors.IsServerTimeout(err) ||
		apierrors.IsInternalError(err) ||
		apierrors.IsTooManyRequests(err) ||
		apierrors.IsServiceUnavailable(err)
}
