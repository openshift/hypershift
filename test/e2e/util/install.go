package util

import (
	"context"
	"fmt"
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/install"
	"github.com/openshift/hypershift/support/metrics"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// InstallHyperShiftOperator generates and applies the manifests needed to install the HyperShift Operator starting
// with the all the HyperShift CRDs. It will wait for the HyperShift Operator to be ready before it returns.
func InstallHyperShiftOperator(ctx context.Context, opts HyperShiftOperatorInstallOptions) error {
	installOpts := getInstallOptions(opts)

	if opts.DryRun {
		installOpts.OutputFile = opts.DryRunDir + "/install-hypershift-operator.yaml"
		installOpts.Format = install.RenderFormatYaml
		installOpts.OutputTypes = string(install.OutputAll)
		return install.RenderHyperShiftOperator(ctx, os.Stdout, &installOpts)
	}

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

// getInstallOptions translates e2e HyperShiftOperatorInstallOptions into
// install.Options, applying only the flags appropriate for the target platform.
// This mirrors the per-platform logic in the CI install step script
// (hypershift-install-commands.sh in openshift/release):
//
//   - AWS:  S3 OIDC, private-platform=AWS, external-dns provider=aws
//   - Azure: private-platform=Azure (self-managed) or managed-service, external-dns provider=azure
//   - GCP: private-platform=GCP with project/region, external-dns provider=google
//   - Other (KubeVirt, None, …): no S3 OIDC, no private platform, no external-dns
//
// The e2e flag defaults in e2e_test.go are AWS-centric (non-empty S3 credentials
// path, external-dns provider="aws", etc.).  Without this platform switch those
// defaults leak into every platform and fail install validation (e.g. S3 bucket
// name required when S3 credentials are set).
func getInstallOptions(opts HyperShiftOperatorInstallOptions) install.Options {
	installOpts := install.NewInstallOptionsWithDefaults()

	// Platform-agnostic flags.
	installOpts.EnableCIDebugOutput = opts.EnableCIDebugOutput
	installOpts.HyperShiftImage = opts.HyperShiftOperatorLatestImage
	installOpts.PlatformMonitoring = metrics.PlatformMonitoring(opts.PlatformMonitoring)
	installOpts.WaitUntilAvailable = true
	installOpts.EnableSizeTagging = opts.EnableSizeTagging
	installOpts.EnableDedicatedRequestServingIsolation = opts.EnableDedicatedRequestServingIsolation
	installOpts.EnableCPOOverrides = opts.EnableCPOOverrides
	installOpts.EnableEtcdRecovery = opts.EnableEtcdRecovery
	installOpts.DisableCAPIMigration = opts.DisableCAPIMigration

	// Platform-specific flags: OIDC, private platform, and external-dns.
	switch opts.Platform {
	case hyperv1.AWSPlatform, "":
		installOpts.OIDCStorageProviderS3BucketName = opts.AWSOidcS3BucketName
		installOpts.OIDCStorageProviderS3Credentials = opts.AWSOidcS3Credentials
		installOpts.OIDCStorageProviderS3Region = opts.AWSOidcS3Region
		installOpts.PrivatePlatform = opts.PrivatePlatform
		installOpts.AWSPrivateCreds = opts.AWSPrivateCredentialsFile
		installOpts.AWSPrivateRegion = opts.AWSPrivateRegion
		installOpts.ExternalDNSProvider = opts.ExternalDNSProvider
		installOpts.ExternalDNSCredentials = opts.ExternalDNSCredentials
		installOpts.ExternalDNSDomainFilter = opts.ExternalDNSDomainFilter
		installOpts.ExternalDNSInterval = "3m"
	case hyperv1.AzurePlatform:
		installOpts.PrivatePlatform = opts.PrivatePlatform
		installOpts.AzurePrivateCreds = opts.AzurePrivateCredentialsFile
		installOpts.AzurePLSResourceGroup = opts.AzurePLSResourceGroup
		installOpts.ExternalDNSProvider = opts.ExternalDNSProvider
		installOpts.ExternalDNSCredentials = opts.ExternalDNSCredentials
		installOpts.ExternalDNSDomainFilter = opts.ExternalDNSDomainFilter
		installOpts.ExternalDNSInterval = "3m"
	case hyperv1.GCPPlatform:
		installOpts.PrivatePlatform = opts.PrivatePlatform
		installOpts.GCPProject = opts.GCPProject
		installOpts.GCPRegion = opts.GCPRegion
		installOpts.ExternalDNSProvider = opts.ExternalDNSProvider
		installOpts.ExternalDNSCredentials = opts.ExternalDNSCredentials
		installOpts.ExternalDNSDomainFilter = opts.ExternalDNSDomainFilter
		installOpts.ExternalDNSGoogleProject = opts.ExternalDNSGoogleProject
		installOpts.ExternalDNSInterval = "3m"
	default:
		// KubeVirt, None, and other platforms where --e2e.platform does not
		// identify the management-cluster infrastructure.
		// The CI step script must pass the appropriate HO flags explicitly
		// (e.g. AWS S3 OIDC bucket name for KubeVirt-on-AWS). Only values
		// whose intentional flag is set are forwarded, so hard-coded non-empty
		// defaults (credentials path, region) don't leak and trigger validation
		// errors.
		if opts.AWSOidcS3BucketName != "" {
			installOpts.OIDCStorageProviderS3BucketName = opts.AWSOidcS3BucketName
			installOpts.OIDCStorageProviderS3Credentials = opts.AWSOidcS3Credentials
			installOpts.OIDCStorageProviderS3Region = opts.AWSOidcS3Region
		}
	}

	return installOpts
}
