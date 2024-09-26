package registry

import (
	"crypto/rand"
	"encoding/hex"

	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func ReconcileRegistryConfig(cfg *imageregistryv1.Config, platform hyperv1.PlatformType, availabilityPolicy hyperv1.AvailabilityPolicy) {
	// Only initialize number of replicas if creating the config
	if cfg.ResourceVersion == "" {
		switch availabilityPolicy {
		case hyperv1.HighlyAvailable:
			cfg.Spec.Replicas = 2
		default:
			cfg.Spec.Replicas = 1
		}
	}
	if cfg.Spec.ManagementState == "" {
		cfg.Spec.ManagementState = operatorv1.Managed
	}
	if cfg.Spec.HTTPSecret == "" {
		cfg.Spec.HTTPSecret = generateImageRegistrySecret()
	}

	// Initially assign storage as emptyDir for KubevirtPlatform and NonePlatform
	// Allow user to change storage afterwards
	if cfg.ResourceVersion == "" && (platform == hyperv1.KubevirtPlatform || platform == hyperv1.NonePlatform) {
		cfg.Spec.Storage = imageregistryv1.ImageRegistryConfigStorage{EmptyDir: &imageregistryv1.ImageRegistryConfigStorageEmptyDir{}}
	}
	// IBM Cloud platform allows to initialize the registry config and then afterwards the client is in full control of the updates
	if platform == hyperv1.IBMCloudPlatform {
		// Only initialize on creates and bad config
		// TODO(IBM): Revisit after bug is addressed, https://github.com/openshift/cluster-image-registry-operator/issues/835
		onCreate := cfg.ResourceVersion == "" && cfg.Generation < 1
		badConfig := cfg.Spec.Storage.IBMCOS != nil && *cfg.Spec.Storage.IBMCOS == (imageregistryv1.ImageRegistryConfigStorageIBMCOS{})
		if onCreate || badConfig {
			cfg.Spec.Replicas = 1
			cfg.Spec.ManagementState = operatorv1.Removed
			cfg.Spec.Storage = imageregistryv1.ImageRegistryConfigStorage{EmptyDir: &imageregistryv1.ImageRegistryConfigStorageEmptyDir{}}
		}
	}
}

func generateImageRegistrySecret() string {
	num := make([]byte, 64)
	_, _ = rand.Read(num)
	return hex.EncodeToString(num)
}
