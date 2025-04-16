package oapi

import (
	"fmt"
	"path"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	certsTrustPath = "/etc/pki/tls/certs"

	auditWebhookConfigFileVolumeName = "oas-audit-webhook"

	additionalTrustBundleProjectedVolumeName = "additional-trust-bundle"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	additionalCAs, err := getAdditionalCAs(cpContext)
	if err != nil {
		return err
	}

	if len(additionalCAs) > 0 {
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, buildAdditionalTrustBundleProjectedVolume(additionalCAs))
	}

	etcdHostname := "etcd-client"
	if cpContext.HCP.Spec.Etcd.ManagementType == hyperv1.Unmanaged {
		etcdHostname, err = util.HostFromURL(cpContext.HCP.Spec.Etcd.Unmanaged.Endpoint)
		if err != nil {
			return err
		}
	}
	noProxy := []string{
		manifests.KubeAPIServerService("").Name,
		etcdHostname,
		config.AuditWebhookService,
	}

	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		if !util.HCPOAuthEnabled(cpContext.HCP) {
			c.Args = append(c.Args, "--internal-oauth-disabled=true")
		}

		util.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "NO_PROXY",
			Value: strings.Join(noProxy, ","),
		})

		for _, additionalCA := range additionalCAs {
			for _, item := range additionalCA.ConfigMap.Items {
				c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
					Name:      additionalTrustBundleProjectedVolumeName,
					MountPath: path.Join(certsTrustPath, item.Path),
					SubPath:   item.Path,
				})
			}
		}
	})

	if cpContext.HCP.Spec.Configuration.GetAuditPolicyConfig().Profile == configv1.NoneAuditProfileType {
		util.RemoveContainer("audit-logs", &deployment.Spec.Template.Spec)
	}

	serviceServingCA, err := getServiceServingCA(cpContext)
	if err != nil {
		return err
	}
	if serviceServingCA != nil {
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: "kube-controller-manager",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: serviceServingCA.Name,
					},
				},
			},
		})

		util.UpdateContainer("oas-trust-anchor-generator", deployment.Spec.Template.Spec.InitContainers, func(c *corev1.Container) {
			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
				Name:      "kube-controller-manager",
				MountPath: "/run/service-ca-signer",
			})
		})
	}

	if cpContext.HCP.Spec.AuditWebhook != nil && len(cpContext.HCP.Spec.AuditWebhook.Name) > 0 {
		applyAuditWebhookConfigFileVolume(&deployment.Spec.Template.Spec, cpContext.HCP.Spec.AuditWebhook)
	}

	return nil
}

func applyAuditWebhookConfigFileVolume(podSpec *corev1.PodSpec, auditWebhookRef *corev1.LocalObjectReference) {
	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name: auditWebhookConfigFileVolumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: auditWebhookRef.Name},
		},
	})

	util.UpdateContainer(ComponentName, podSpec.Containers, func(c *corev1.Container) {
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      auditWebhookConfigFileVolumeName,
			MountPath: "/etc/kubernetes/auditwebhook",
		})
	})
}

func buildAdditionalTrustBundleProjectedVolume(additionalCAs []corev1.VolumeProjection) corev1.Volume {
	v := corev1.Volume{
		Name: additionalTrustBundleProjectedVolumeName,
	}
	v.Projected = &corev1.ProjectedVolumeSource{
		Sources:     additionalCAs,
		DefaultMode: ptr.To[int32](420),
	}
	return v
}

func getServiceServingCA(cpContext component.WorkloadContext) (*corev1.ConfigMap, error) {
	serviceServingCA := manifests.ServiceServingCA(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(serviceServingCA), serviceServingCA); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get service serving CA")
		}
		return nil, nil
	}
	return serviceServingCA, nil
}

func getAdditionalCAs(cpContext component.WorkloadContext) ([]corev1.VolumeProjection, error) {
	var additionalCAs []corev1.VolumeProjection
	// if hostedCluster additionalTrustBundle is set, add it to the volumeProjection
	if cpContext.HCP.Spec.AdditionalTrustBundle != nil {
		additionalCAs = append(additionalCAs, corev1.VolumeProjection{
			ConfigMap: &corev1.ConfigMapProjection{
				LocalObjectReference: *cpContext.HCP.Spec.AdditionalTrustBundle,
				Items:                []corev1.KeyToPath{{Key: "ca-bundle.crt", Path: "additional-ca-bundle.pem"}},
			},
		})
	}

	configuration := cpContext.HCP.Spec.Configuration
	if configuration == nil || configuration.Image == nil || configuration.Image.AdditionalTrustedCA.Name == "" {
		return additionalCAs, nil
	}

	imageRegistryAdditionalTrustedCAs := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configuration.Image.AdditionalTrustedCA.Name,
			Namespace: cpContext.HCP.Namespace,
		}}
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(imageRegistryAdditionalTrustedCAs), imageRegistryAdditionalTrustedCAs); err != nil {
		return nil, fmt.Errorf("failed to get image registry additional trusted CA configmap: %w", err)
	}

	// If additional trusted CAs exist for image registries, add them to the volumeProjection
	// The configmap for image registry additional trusted CA can have a separate key per registry.
	// Each entry in the configmap will get its own key to path mapping so that we mount it separately.
	if len(imageRegistryAdditionalTrustedCAs.Data) > 0 {
		vol := corev1.VolumeProjection{
			ConfigMap: &corev1.ConfigMapProjection{
				LocalObjectReference: corev1.LocalObjectReference{Name: imageRegistryAdditionalTrustedCAs.Name},
			},
		}
		// use a set to get a sorted key list for consistency across reconciles
		keys := sets.New[string]()
		for key := range imageRegistryAdditionalTrustedCAs.Data {
			keys.Insert(key)
		}
		for i, key := range sets.List(keys) {
			vol.ConfigMap.Items = append(vol.ConfigMap.Items, corev1.KeyToPath{
				Key:  key,
				Path: fmt.Sprintf("image-registry-%d.pem", i+1),
			})
		}
		additionalCAs = append(additionalCAs, vol)
	}

	return additionalCAs, nil
}
