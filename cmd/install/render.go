package install

import (
	"fmt"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type TemplateParams struct {
	HyperShiftImage             string
	HyperShiftImageTag          string
	Namespace                   string
	HypershiftOperatorReplicas  string
	OIDCS3Name                  string
	OIDCS3Region                string
	OIDCS3CredsSecret           string
	OIDCS3CredsSecretKey        string
	AWSPrivateRegion            string
	AWSPrivateCredsSecret       string
	AWSPrivateCredsSecretKey    string
	ExternalDNSCredsSecret      string
	ExternalDNSDomainFilter     string
	ExternalDNSTxtOwnerID       string
	ExternalDNSImage            string
	RegistryOverrides           string
	AROHCPKeyVaultUsersClientID string
	TemplateNamespace           bool
	TemplateParamWrapper        func(string) string
}

func hyperShiftOperatorTemplateManifest(opts *Options, templateParamConfig TemplateParams) ([]crclient.Object, []crclient.Object, error) {
	// validate options
	if err := opts.ValidateRender(); err != nil {
		return nil, nil, err
	}

	opts.HyperShiftImage = fmt.Sprintf("%s:%s", templateParamConfig.TemplateParamWrapper(templateParamConfig.HyperShiftImage), templateParamConfig.TemplateParamWrapper(templateParamConfig.HyperShiftImageTag))

	// namespace parameter
	opts.Namespace = templateParamConfig.TemplateParamWrapper(templateParamConfig.Namespace)

	// oidc S3 parameter
	if opts.OIDCStorageProviderS3BucketName != "" {
		opts.OIDCStorageProviderS3BucketName = templateParamConfig.TemplateParamWrapper(templateParamConfig.OIDCS3Name)
		opts.OIDCStorageProviderS3Region = templateParamConfig.TemplateParamWrapper(templateParamConfig.OIDCS3Region)
		opts.OIDCStorageProviderS3CredentialsSecret = templateParamConfig.TemplateParamWrapper(templateParamConfig.OIDCS3CredsSecret)
		opts.OIDCStorageProviderS3CredentialsSecretKey = templateParamConfig.TemplateParamWrapper(templateParamConfig.OIDCS3CredsSecretKey)
	}

	// aws private credentials
	if opts.AWSPrivateCredentialsSecret != "" {
		opts.AWSPrivateRegion = templateParamConfig.TemplateParamWrapper(templateParamConfig.AWSPrivateRegion)
		opts.AWSPrivateCredentialsSecret = templateParamConfig.TemplateParamWrapper(templateParamConfig.AWSPrivateCredsSecret)
		opts.AWSPrivateCredentialsSecretKey = templateParamConfig.TemplateParamWrapper(templateParamConfig.AWSPrivateCredsSecretKey)
	}

	// external DNS
	if opts.ExternalDNSProvider != "" {
		opts.ExternalDNSImage = templateParamConfig.TemplateParamWrapper(templateParamConfig.ExternalDNSImage)
		opts.ExternalDNSDomainFilter = templateParamConfig.TemplateParamWrapper(templateParamConfig.ExternalDNSDomainFilter)
		opts.ExternalDNSCredentialsSecret = templateParamConfig.TemplateParamWrapper(templateParamConfig.ExternalDNSCredsSecret)
		opts.ExternalDNSTxtOwnerId = templateParamConfig.TemplateParamWrapper(templateParamConfig.ExternalDNSTxtOwnerID)
	}

	// registry overrides
	opts.RegistryOverrides = templateParamConfig.TemplateParamWrapper(templateParamConfig.RegistryOverrides)

	// azure key vault client id
	opts.AroHCPKeyVaultUsersClientID = templateParamConfig.TemplateParamWrapper(templateParamConfig.AROHCPKeyVaultUsersClientID)

	// create manifests
	opts.RenderNamespace = templateParamConfig.TemplateNamespace
	crds, objects, err := hyperShiftOperatorManifests(*opts)
	if err != nil {
		return nil, nil, err
	}
	return crds, objects, nil

}
