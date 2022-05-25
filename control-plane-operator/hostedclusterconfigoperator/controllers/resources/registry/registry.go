package registry

import (
	"crypto/rand"
	"encoding/hex"

	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
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
	if platform == hyperv1.KubevirtPlatform || platform == hyperv1.NonePlatform {
		cfg.Spec.Storage = imageregistryv1.ImageRegistryConfigStorage{EmptyDir: &imageregistryv1.ImageRegistryConfigStorageEmptyDir{}}
	}
}

func generateImageRegistrySecret() string {
	num := make([]byte, 64)
	rand.Read(num)
	return hex.EncodeToString(num)
}
