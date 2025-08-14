package catalogs

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"

	"github.com/blang/semver"
)

const cacheExpiration = 5 * time.Minute

type imagesCache struct {
	timeStamp time.Time
	hash      string
	images    map[string]string
	m         sync.Mutex
}

func (c *imagesCache) getImages(inputsHash string) map[string]string {
	c.m.Lock()
	defer c.m.Unlock()

	if inputsHash != c.hash {
		return nil
	}

	if time.Since(c.timeStamp) > cacheExpiration {
		return nil
	}

	return c.images
}

func (c *imagesCache) setImages(images map[string]string, inputsHash string) {
	c.m.Lock()
	defer c.m.Unlock()

	c.timeStamp = time.Now()
	c.hash = inputsHash
	c.images = images
}

var catalogImagesCache = &imagesCache{}

// GetCatalogImages uses a simple cache to prevent frequent registry lookups for catalog images
func GetCatalogImages(ctx context.Context, hcp hyperv1.HostedControlPlane, pullSecret []byte, imageMetadataProvider util.ImageMetadataProvider, registryOverrides map[string][]string) (map[string]string, error) {
	return getCatalogImagesWithCache(
		imageLookupCacheKeyFn(&hcp, pullSecret, registryOverrides),
		releaseVersionFn(ctx, &hcp, pullSecret, imageMetadataProvider),
		imageExistsFn(ctx, &hcp, pullSecret, imageMetadataProvider),
		registryOverrides)
}

func getCatalogImagesWithCache(cacheKey func() any, releaseVersion func() (*semver.Version, error), imageExists func(string) (bool, error), registryOverrides map[string][]string) (map[string]string, error) {
	hash := util.HashSimple(cacheKey())
	if images := catalogImagesCache.getImages(hash); images != nil {
		return images, nil
	}
	images, err := computeCatalogImages(releaseVersion, imageExists, registryOverrides)
	if err != nil {
		return nil, err
	}
	catalogImagesCache.setImages(images, hash)
	return images, nil
}

func imageLookupCacheKeyFn(hcp *hyperv1.HostedControlPlane, pullSecret []byte, registryOverrides map[string][]string) func() any {
	return func() any {
		cacheKey := struct {
			releaseImage string
			overrides    map[string][]string
			pullSecret   []byte
		}{
			releaseImage: hcp.Spec.ReleaseImage,
			overrides:    registryOverrides,
			pullSecret:   pullSecret,
		}
		return cacheKey
	}
}

func releaseVersionFn(ctx context.Context, hcp *hyperv1.HostedControlPlane, pullSecret []byte, imageMetadataProvider util.ImageMetadataProvider) func() (*semver.Version, error) {
	return func() (*semver.Version, error) {
		imageRef := hcp.Spec.ReleaseImage
		imageConfig, _, _, err := imageMetadataProvider.GetMetadata(ctx, imageRef, pullSecret)
		if err != nil {
			return nil, fmt.Errorf("failed to get image metadata: %w", err)
		}

		version, err := semver.Parse(imageConfig.Config.Labels["io.openshift.release"])
		if err != nil {
			return nil, fmt.Errorf("invalid OpenShift release version format: %s", imageConfig.Config.Labels["io.openshift.release"])
		}
		return &version, nil
	}
}

func imageExistsFn(ctx context.Context, hcp *hyperv1.HostedControlPlane, pullSecret []byte, imageMetadataProvider util.ImageMetadataProvider) func(image string) (bool, error) {
	return func(image string) (bool, error) {
		if hcp.Spec.OLMCatalogPlacement == hyperv1.GuestOLMCatalogPlacement {
			return true, nil
		}
		_, _, err := imageMetadataProvider.GetDigest(ctx, image, pullSecret)
		if err == nil {
			return true, nil
		}
		if strings.Contains(err.Error(), "manifest unknown") || strings.Contains(err.Error(), "access to the requested resource is not authorized") {
			return false, nil
		}
		return false, fmt.Errorf("failed to get image digest: %w", err)
	}
}

func computeCatalogImages(releaseVersion func() (*semver.Version, error), imageExists func(string) (bool, error), registryOverrides map[string][]string) (map[string]string, error) {
	var registries []string
	version, err := releaseVersion()
	if err != nil {
		return nil, err
	}

	defaultRegistryURL := "registry.redhat.io"
	defaultRegistryNamespace := "redhat"
	defaultRegistry := fmt.Sprintf("%s/%s", defaultRegistryURL, defaultRegistryNamespace)

	if len(registryOverrides) > 0 {
		for registrySource, registryDest := range registryOverrides {
			switch {
			case registrySource == defaultRegistry:
				registries = registryDest
			case registrySource == defaultRegistryURL:
				for _, dest := range registryDest {
					if strings.Contains(dest, "/") {
						registries = append(registries, dest)
					} else {
						registries = append(registries, fmt.Sprintf("%s/%s", dest, defaultRegistryNamespace))
					}
				}
			}
		}
	}
	if len(registries) == 0 {
		registries = []string{
			defaultRegistry,
		}
	}

	//check catalogs of last 4 supported version in case new version is not available
	supportedVersions := 4

	catalogImageNames := map[string]string{
		"certified-operators": "certified-operator-index",
		"community-operators": "community-operator-index",
		"redhat-marketplace":  "redhat-marketplace-index",
		"redhat-operators":    "redhat-operator-index",
	}

	catalogs := map[string]string{
		"certified-operators": "",
		"community-operators": "",
		"redhat-marketplace":  "",
		"redhat-operators":    "",
	}

	for catalog := range catalogs {
		catalogVersion := *version
		for i := range supportedVersions {
			for _, registry := range registries {
				testImage := fmt.Sprintf("%s/%s:v%d.%d", registry, catalogImageNames[catalog], catalogVersion.Major, catalogVersion.Minor)
				exists, err := imageExists(testImage)
				if err != nil {
					return nil, err
				}

				if exists {
					catalogs[catalog] = testImage
					break
				}
			}
			if catalogs[catalog] != "" {
				break
			}
			if i == supportedVersions-1 {
				return nil, fmt.Errorf("failed to fetch image digest for 4 previous versions of %s", catalog)
			}
			catalogVersion.Minor--
		}
	}
	return catalogs, nil
}
