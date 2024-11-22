package olm

import (
	"fmt"
	"math/big"
	"strings"

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
	return map[string]string{"app": "catalog-operator", hyperv1.ControlPlaneComponent: "catalog-operator"}
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

func ReconcileCertifiedOperatorsDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, dc config.DeploymentConfig, imageOverride string) error {
	return reconcileCatalogDeployment(deployment, ownerRef, dc, certifiedCatalogDeployment, imageOverride)
}

func ReconcileCommunityOperatorsDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, dc config.DeploymentConfig, imageOverride string) error {
	return reconcileCatalogDeployment(deployment, ownerRef, dc, communityCatalogDeployment, imageOverride)
}

func ReconcileRedHatMarketplaceOperatorsDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, dc config.DeploymentConfig, imageOverride string) error {
	return reconcileCatalogDeployment(deployment, ownerRef, dc, redHatMarketplaceCatalogDeployment, imageOverride)
}

func ReconcileRedHatOperatorsDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, dc config.DeploymentConfig, imageOverride string) error {
	return reconcileCatalogDeployment(deployment, ownerRef, dc, redHatOperatorsCatalogDeployment, imageOverride)
}

func reconcileCatalogDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, dc config.DeploymentConfig, sourceDeployment *appsv1.Deployment, imageOverride string) error {
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

var CatalogToImage = map[string]string{
	"certified-operators": "registry.redhat.io/redhat/certified-operator-index:v4.16",
	"community-operators": "registry.redhat.io/redhat/community-operator-index:v4.16",
	"redhat-marketplace":  "registry.redhat.io/redhat/redhat-marketplace-index:v4.16",
	"redhat-operators":    "registry.redhat.io/redhat/redhat-operator-index:v4.16",
}

func ReconcileCatalogsImageStream(imageStream *imagev1.ImageStream, ownerRef config.OwnerRef, isImageRegistryOverrides map[string][]string) error {
	imageStream.Spec.LookupPolicy.Local = true
	if imageStream.Spec.Tags == nil {
		imageStream.Spec.Tags = []imagev1.TagReference{}
	}
	for name, image := range getCatalogToImageWithISImageRegistryOverrides(CatalogToImage, isImageRegistryOverrides) {
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
