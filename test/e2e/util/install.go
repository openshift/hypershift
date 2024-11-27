package util

import (
	"context"
	"fmt"
	"os"

	"github.com/openshift/hypershift/cmd/install"
	"github.com/openshift/hypershift/support/metrics"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// InstallHyperShiftOperator generates and applies the manifests needed to install the HyperShift Operator starting
// with the all the HyperShift CRDs. It will wait for the HyperShift Operator to be ready before it returns.
func InstallHyperShiftOperator(ctx context.Context, opts HyperShiftOperatorInstallOptions) error {

	installOpts := getInstallOptions(opts)

	return install.InstallHyperShiftOperator(ctx, os.Stdout, installOpts)
}

// GetHyperShiftOperatorImage returns the current rolled-out image of the HyperShift operator
func GetHyperShiftOperatorImage(ctx context.Context, client crclient.Client, opts HyperShiftOperatorInstallOptions) (string, error) {
	var image string
	installOpts := getInstallOptions(opts)
	deployment, err := install.WaitUntilAvailable(ctx, installOpts)

	if err != nil {
		return image, err
	}
	containers := deployment.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		return image, fmt.Errorf("unexpected number of containers found for the HyperShift operator. Want 1, got %d", len(containers))
	}
	return containers[0].Image, nil
}

func getInstallOptions(opts HyperShiftOperatorInstallOptions) install.Options {
	installOpts := install.NewInstallOptionsWithDefaults()

	installOpts.AWSPrivateCreds = opts.AWSPrivateCredentialsFile
	installOpts.AWSPrivateRegion = opts.AWSPrivateRegion
	installOpts.EnableCIDebugOutput = opts.EnableCIDebugOutput
	installOpts.ExternalDNSCredentials = opts.ExternalDNSCredentials
	installOpts.ExternalDNSDomainFilter = opts.ExternalDNSDomainFilter
	installOpts.ExternalDNSProvider = opts.ExternalDNSProvider
	installOpts.HyperShiftImage = opts.HyperShiftOperatorLatestImage
	installOpts.OIDCStorageProviderS3BucketName = opts.AWSOidcS3BucketName
	installOpts.OIDCStorageProviderS3Credentials = opts.AWSOidcS3Credentials
	installOpts.OIDCStorageProviderS3Region = opts.AWSOidcS3Region
	installOpts.PlatformMonitoring = metrics.PlatformMonitoring(opts.PlatformMonitoring)
	installOpts.PrivatePlatform = opts.PrivatePlatform
	installOpts.WaitUntilAvailable = true

	return installOpts
}
