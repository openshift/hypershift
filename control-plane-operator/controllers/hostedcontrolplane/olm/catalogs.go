package olm

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/blang/semver"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/assets"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	imagev1 "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/util"
)

var (
	certifiedCatalogService         = assets.MustService(content.ReadFile, "assets/catalog-certified.service.yaml")
	communityCatalogService         = assets.MustService(content.ReadFile, "assets/catalog-community.service.yaml")
	redHatMarketplaceCatalogService = assets.MustService(content.ReadFile, "assets/catalog-redhat-marketplace.service.yaml")
	redHatOperatorsCatalogService   = assets.MustService(content.ReadFile, "assets/catalog-redhat-operators.service.yaml")
)

func catalogLabels() map[string]string {
	return map[string]string{"app": "catalog-operator", hyperv1.ControlPlaneComponentLabel: "catalog-operator"}
}

func ReconcileCertifiedOperatorsService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	return reconcileCatalogService(svc, ownerRef, certifiedCatalogService)
}

func ReconcileCommunityOperatorsService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	return reconcileCatalogService(svc, ownerRef, communityCatalogService)
}

func ReconcileRedHatMarketplaceOperatorsService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	return reconcileCatalogService(svc, ownerRef, redHatMarketplaceCatalogService)
}

func ReconcileRedHatOperatorsService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	return reconcileCatalogService(svc, ownerRef, redHatOperatorsCatalogService)
}

func reconcileCatalogService(svc *corev1.Service, ownerRef config.OwnerRef, sourceService *corev1.Service) error {
	ownerRef.ApplyTo(svc)
	// The service is assigned a cluster IP when it is created.
	// This field is immutable as shown here: https://github.com/kubernetes/api/blob/62998e98c313b2ca15b1da278aa702bdd7b84cb0/core/v1/types.go#L4114-L4130
	// As such, to avoid an error when updating the object, only update the fields OLM specifies.
	sourceServiceDeepCopy := sourceService.DeepCopy()
	svc.Spec.Ports = sourceServiceDeepCopy.Spec.Ports
	svc.Spec.Type = sourceServiceDeepCopy.Spec.Type
	svc.Spec.Selector = sourceServiceDeepCopy.Spec.Selector

	return nil
}

var (
	certifiedCatalogDeployment         = assets.MustDeployment(content.ReadFile, "assets/catalog-certified.deployment.yaml")
	communityCatalogDeployment         = assets.MustDeployment(content.ReadFile, "assets/catalog-community.deployment.yaml")
	redHatMarketplaceCatalogDeployment = assets.MustDeployment(content.ReadFile, "assets/catalog-redhat-marketplace.deployment.yaml")
	redHatOperatorsCatalogDeployment   = assets.MustDeployment(content.ReadFile, "assets/catalog-redhat-operators.deployment.yaml")
)

func ReconcileCertifiedOperatorsDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, dc config.DeploymentConfig, imageOverride, olmManagerImage string) error {
	return reconcileCatalogDeployment(deployment, ownerRef, dc, certifiedCatalogDeployment, imageOverride, olmManagerImage)
}

func ReconcileCommunityOperatorsDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, dc config.DeploymentConfig, imageOverride, olmManagerImage string) error {
	return reconcileCatalogDeployment(deployment, ownerRef, dc, communityCatalogDeployment, imageOverride, olmManagerImage)
}

func ReconcileRedHatMarketplaceOperatorsDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, dc config.DeploymentConfig, imageOverride, olmManagerImage string) error {
	return reconcileCatalogDeployment(deployment, ownerRef, dc, redHatMarketplaceCatalogDeployment, imageOverride, olmManagerImage)
}

func ReconcileRedHatOperatorsDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, dc config.DeploymentConfig, imageOverride, olmManagerImage string) error {
	return reconcileCatalogDeployment(deployment, ownerRef, dc, redHatOperatorsCatalogDeployment, imageOverride, olmManagerImage)
}

func reconcileCatalogDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, dc config.DeploymentConfig, sourceDeployment *appsv1.Deployment, imageOverride, olmManagerImage string) error {
	ownerRef.ApplyTo(deployment)
	if deployment.Annotations == nil {
		deployment.Annotations = map[string]string{}
	}
	for k, v := range sourceDeployment.Annotations {
		deployment.Annotations[k] = v
	}
	image := "from:imagestream"
	if imageOverride == "" {
		// If deployment already exists, imagestream tag will already populate the container image
		if len(deployment.Spec.Template.Spec.Containers) > 0 && deployment.Spec.Template.Spec.Containers[0].Image != "" {
			image = deployment.Spec.Template.Spec.Containers[0].Image
		}
	} else {
		image = imageOverride
		delete(deployment.Annotations, "image.openshift.io/triggers")
	}
	deployment.Spec = sourceDeployment.DeepCopy().Spec
	deployment.Spec.Template.Spec.Containers[0].Image = image
	addVolumesAndInitContainers(deployment, image, olmManagerImage)
	dc.ApplyTo(deployment)
	return nil
}

func findTagReference(tags []imagev1.TagReference, name string) *imagev1.TagReference {
	for i, tag := range tags {
		if tag.Name == name {
			return &tags[i]
		}
	}
	return nil
}

func GetCatalogImages(ctx context.Context, hcp hyperv1.HostedControlPlane, pullSecret []byte, imageMetadataProvider util.ImageMetadataProvider, registryOverrides map[string][]string) (map[string]string, error) {
	imageRef := hcp.Spec.ReleaseImage
	imageConfig, _, _, err := imageMetadataProvider.GetMetadata(ctx, imageRef, pullSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get image metadata: %w", err)
	}

	version, err := semver.Parse(imageConfig.Config.Labels["io.openshift.release"])
	if err != nil {
		return nil, fmt.Errorf("invalid OpenShift release version format: %s", imageConfig.Config.Labels["io.openshift.release"])
	}

	registries := []string{
		"registry.redhat.io/redhat",
	}
	if len(registryOverrides) > 0 {
		for registrySource, registryDest := range registryOverrides {
			if registries[0] == registrySource {
				registries = registryDest
				break
			}
		}
	}

	//check catalogs of last 4 supported version in case new version is not available
	supportedVersions := 4
	imageRegistry := ""
	if hcp.Spec.OLMCatalogPlacement == hyperv1.GuestOLMCatalogPlacement {
		imageRegistry = "registry.redhat.io/redhat"
	} else {
		for i := 0; i < supportedVersions; i++ {

			for _, registry := range registries {
				testImage := fmt.Sprintf("%s/certified-operator-index:v%d.%d", registry, version.Major, version.Minor)

				_, dockerImage, err := imageMetadataProvider.GetDigest(ctx, testImage, pullSecret)
				if err == nil {
					imageRegistry = fmt.Sprintf("%s/%s", dockerImage.Registry, dockerImage.Namespace)
					break
				}

				// Manifest unknown error is expected if the image is not available.
				if !strings.Contains(err.Error(), "manifest unknown") {
					return nil, err // Return if it's an unexpected error
				}
			}
			if imageRegistry != "" {
				break
			}
			if i == supportedVersions-1 {
				return nil, fmt.Errorf("failed to get image digest for 4 previous versions of certified-operator-index: %w", err)
			}
			version.Minor--
		}
	}

	operators := map[string]string{
		"certified-operators": fmt.Sprintf("%s/certified-operator-index:v%d.%d", imageRegistry, version.Major, version.Minor),
		"community-operators": fmt.Sprintf("%s/community-operator-index:v%d.%d", imageRegistry, version.Major, version.Minor),
		"redhat-marketplace":  fmt.Sprintf("%s/redhat-marketplace-index:v%d.%d", imageRegistry, version.Major, version.Minor),
		"redhat-operators":    fmt.Sprintf("%s/redhat-operator-index:v%d.%d", imageRegistry, version.Major, version.Minor),
	}

	return operators, nil
}

func ReconcileCatalogsImageStream(imageStream *imagev1.ImageStream, ownerRef config.OwnerRef, catalogImages map[string]string) error {
	imageStream.Spec.LookupPolicy.Local = true
	if imageStream.Spec.Tags == nil {
		imageStream.Spec.Tags = []imagev1.TagReference{}
	}
	for name, image := range catalogImages {
		tagRef := findTagReference(imageStream.Spec.Tags, name)
		if tagRef == nil {
			imageStream.Spec.Tags = append(imageStream.Spec.Tags, imagev1.TagReference{
				Name: name,
				From: &corev1.ObjectReference{
					Kind: "DockerImage",
					Name: image,
				},
				ReferencePolicy: imagev1.TagReferencePolicy{
					Type: imagev1.LocalTagReferencePolicy,
				},
				ImportPolicy: imagev1.TagImportPolicy{
					Scheduled:  true,
					ImportMode: imagev1.ImportModePreserveOriginal,
				},
			})
		} else {
			tagRef.From = &corev1.ObjectReference{
				Kind: "DockerImage",
				Name: image,
			}
			tagRef.ReferencePolicy.Type = imagev1.LocalTagReferencePolicy
			tagRef.ImportPolicy.Scheduled = true
		}
	}
	ownerRef.ApplyTo(imageStream)
	return nil
}

// getCatalogToImageWithISImageRegistryOverrides returns a map of
// images to be used for the catalog registries where the image address got
// amended according to OpenShiftImageRegistryOverrides as set on the HostedControlPlaneReconciler
func getCatalogToImageWithISImageRegistryOverrides(catalogToImage map[string]string, isImageRegistryOverrides map[string][]string) map[string]string {
	catalogWithOverride := make(map[string]string)
	for name, image := range catalogToImage {
		for registrySource, registryDest := range isImageRegistryOverrides {
			if strings.Contains(image, registrySource) {
				for _, registryReplacement := range registryDest {
					image = strings.Replace(image, registrySource, registryReplacement, 1)
				}
			}
		}
		catalogWithOverride[name] = image
	}
	return catalogWithOverride
}

// generateModularDailyCronSchedule returns a daily crontab schedule
// where, given a is input's integer representation, the minute is a % 60
// and hour is a % 24.
func generateModularDailyCronSchedule(input []byte) string {
	a := big.NewInt(0).SetBytes(input)
	var hi, mi big.Int
	m := mi.Mod(a, big.NewInt(60))
	h := hi.Mod(a, big.NewInt(24))
	return fmt.Sprintf("%d %d * * *", m.Int64(), h.Int64())
}

func ReconcileCatalogServiceMonitor(sm *prometheusoperatorv1.ServiceMonitor, ownerRef config.OwnerRef, clusterID string, metricsSet metrics.MetricsSet) error {
	ownerRef.ApplyTo(sm)

	sm.Spec.Selector.MatchLabels = catalogLabels()
	sm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{sm.Namespace},
	}
	targetPort := intstr.FromString("metrics")
	sm.Spec.Endpoints = []prometheusoperatorv1.Endpoint{
		{
			TargetPort: &targetPort,
			Scheme:     "https",
			TLSConfig: &prometheusoperatorv1.TLSConfig{
				SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
					ServerName: "catalog-operator-metrics",
					Cert: prometheusoperatorv1.SecretOrConfigMap{
						Secret: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: manifests.MetricsClientCertSecret(sm.Namespace).Name,
							},
							Key: "tls.crt",
						},
					},
					KeySecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: manifests.MetricsClientCertSecret(sm.Namespace).Name,
						},
						Key: "tls.key",
					},
					CA: prometheusoperatorv1.SecretOrConfigMap{
						ConfigMap: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: manifests.RootCAConfigMap(sm.Namespace).Name,
							},
							Key: certs.CASignerCertMapKey,
						},
					},
				},
			},
			MetricRelabelConfigs: metrics.CatalogOperatorRelabelConfigs(metricsSet),
		},
	}

	util.ApplyClusterIDLabel(&sm.Spec.Endpoints[0], clusterID)

	return nil
}

func addVolumesAndInitContainers(deployment *appsv1.Deployment, image, olmManagerImage string) {
	volumes := []corev1.Volume{
		{
			Name: "utilities",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		{
			Name: "catalog-content",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}

	initContainers := []corev1.Container{
		{
			Name:            "extract-utilities",
			Image:           olmManagerImage,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command:         []string{"cp"},
			Args:            []string{"/bin/copy-content", "/utilities/copy-content"},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "utilities",
					MountPath: "/utilities",
				},
			},
			TerminationMessagePath:   "/dev/termination-log",
			TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		},
		{
			Name:            "extract-content",
			Image:           image,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command:         []string{"/utilities/copy-content"},
			Args: []string{
				"--catalog.from=/configs",
				"--catalog.to=/extracted-catalog/catalog",
				"--cache.from=/tmp/cache",
				"--cache.to=/extracted-catalog/cache",
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "utilities",
					MountPath: "/utilities",
				},
				{
					Name:      "catalog-content",
					MountPath: "/extracted-catalog",
				},
			},
			TerminationMessagePath:   "/dev/termination-log",
			TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		},
	}

	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, volumes...)
	deployment.Spec.Template.Spec.InitContainers = append(deployment.Spec.Template.Spec.InitContainers, initContainers...)

	olmCatalogSource := ""
	if deployment.Spec.Template.Labels != nil {
		olmCatalogSource = deployment.Spec.Template.Labels["olm.catalogSource"]
	}

	if deployment.Annotations == nil {
		deployment.Annotations = map[string]string{}
	}
	deployment.Annotations["image.openshift.io/triggers"] = fmt.Sprintf(`[
		{"from":{"kind":"ImageStreamTag","name":"catalogs:%s"},"fieldPath":"spec.template.spec.initContainers[?(@.name==\"extract-content\")].image"},
		{"from":{"kind":"ImageStreamTag","name":"catalogs:%s"},"fieldPath":"spec.template.spec.containers[?(@.name==\"registry\")].image"}
	]`, olmCatalogSource, olmCatalogSource)

	// Add command, args, and volume mounts to the first container
	if len(deployment.Spec.Template.Spec.Containers) > 0 {
		deployment.Spec.Template.Spec.Containers[0].Command = []string{"/bin/opm"}
		deployment.Spec.Template.Spec.Containers[0].Args = []string{
			"serve",
			"/extracted-catalog/catalog",
			"--cache-dir=/extracted-catalog/cache",
		}
		deployment.Spec.Template.Spec.Containers[0].VolumeMounts = append(deployment.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
			Name:      "catalog-content",
			MountPath: "/extracted-catalog",
		})
	}
}
