package registry

import (
	"crypto/rand"
	"encoding/hex"

	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func ReconcileRegistryConfig(cfg *imageregistryv1.Config, platform hyperv1.PlatformType) {
	cfg.Spec.DefaultRoute = false
	if cfg.Spec.HTTPSecret == "" {
		cfg.Spec.HTTPSecret = generateImageRegistrySecret()
	}
	cfg.Spec.Logging = 2
	cfg.Spec.ManagementState = imageregistryv1.StorageManagementStateManaged
	cfg.Spec.Proxy.HTTP = ""
	cfg.Spec.Proxy.HTTPS = ""
	cfg.Spec.Proxy.NoProxy = ""
	cfg.Spec.ReadOnly = false
	cfg.Spec.Replicas = 1
	cfg.Spec.Requests.Read.MaxInQueue = 0
	cfg.Spec.Requests.Read.MaxRunning = 0
	cfg.Spec.Requests.Read.MaxWaitInQueue.Reset()
	cfg.Spec.Requests.Write.MaxInQueue = 0
	cfg.Spec.Requests.Write.MaxRunning = 0
	cfg.Spec.Requests.Write.MaxWaitInQueue.Reset()

	if platform == hyperv1.KubevirtPlatform || platform == hyperv1.NonePlatform {
		cfg.Spec.Storage = imageregistryv1.ImageRegistryConfigStorage{EmptyDir: &imageregistryv1.ImageRegistryConfigStorageEmptyDir{}}
	} else {
		cfg.Spec.Storage.EmptyDir = nil
	}
}

func generateImageRegistrySecret() string {
	num := make([]byte, 64)
	rand.Read(num)
	return hex.EncodeToString(num)
}
