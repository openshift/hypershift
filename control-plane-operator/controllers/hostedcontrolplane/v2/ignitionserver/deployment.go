package ignitionserver

import (
	"bytes"
	"fmt"
	"maps"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (ign *ignitionServer) adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.FeatureGate != nil {
		featureGate := &configv1.FeatureGate{
			TypeMeta: metav1.TypeMeta{
				Kind:       "FeatureGate",
				APIVersion: configv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "cluster",
			},
			Spec: *hcp.Spec.Configuration.FeatureGate,
		}

		featureGateBuffer := &bytes.Buffer{}
		if err := api.YamlSerializer.Encode(featureGate, featureGateBuffer); err != nil {
			return fmt.Errorf("failed to encode feature gates: %w", err)
		}
		featureGateYAML := featureGateBuffer.String()

		util.UpdateContainer("fetch-feature-gate", deployment.Spec.Template.Spec.InitContainers, func(c *corev1.Container) {
			util.UpsertEnvVar(c, corev1.EnvVar{
				Name:  "FEAUTRE_GATE_YAML",
				Value: featureGateYAML,
			})
		})
	}

	registryOverrides, err := ign.getRegistryOverrides(cpContext.ReleaseImageProvider)
	if err != nil {
		return err
	}
	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args,
			"--registry-overrides", util.ConvertRegistryOverridesToCommandLineFlag(registryOverrides),
			"--platform", string(hcp.Spec.Platform.Type),
		)

		util.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "OPENSHIFT_IMG_OVERRIDES",
			Value: util.ConvertOpenShiftImageRegistryOverridesToCommandLineFlag(ign.releaseProvider.GetOpenShiftImageRegistryOverrides()),
		})

		if mirroredReleaseImage := ign.releaseProvider.GetMirroredReleaseImage(); mirroredReleaseImage != "" {
			c.Env = append(c.Env, corev1.EnvVar{
				Name:  "MIRRORED_RELEASE_IMAGE",
				Value: mirroredReleaseImage,
			})
		}

		proxy.SetEnvVars(&c.Env)
	})

	if hcp.Spec.AdditionalTrustBundle != nil {
		// Add trusted-ca mount with optional configmap
		util.DeploymentAddTrustBundleVolume(hcp.Spec.AdditionalTrustBundle, deployment)
	}

	if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		util.UpdateVolume("serving-cert", deployment.Spec.Template.Spec.Volumes, func(v *corev1.Volume) {
			v.Secret.SecretName = ignitionserver.IgnitionServingCertSecret("").Name
		})
	}

	return nil
}

func (ign *ignitionServer) getRegistryOverrides(imageProvider imageprovider.ReleaseImageProvider) (map[string]string, error) {
	configAPIImage := imageProvider.GetImage("cluster-config-api")
	machineConfigOperatorImage := imageProvider.GetImage("machine-config-operator")

	openShiftRegistryOverrides := util.ConvertOpenShiftImageRegistryOverridesToCommandLineFlag(ign.releaseProvider.GetOpenShiftImageRegistryOverrides())
	ocpRegistryMapping := util.ConvertImageRegistryOverrideStringToMap(openShiftRegistryOverrides)

	// Determine if we need to override the machine config operator and cluster config operator
	// images based on image mappings present in management cluster.
	overrideConfigAPIImage, err := lookupMappedImage(ocpRegistryMapping, configAPIImage)
	if err != nil {
		return nil, err
	}
	overrideMachineConfigOperatorImage, err := lookupMappedImage(ocpRegistryMapping, machineConfigOperatorImage)
	if err != nil {
		return nil, err
	}

	registryOverrides := maps.Clone(ign.releaseProvider.GetRegistryOverrides())
	if overrideConfigAPIImage != configAPIImage {
		registryOverrides[configAPIImage] = overrideConfigAPIImage
	}
	if overrideMachineConfigOperatorImage != machineConfigOperatorImage {
		registryOverrides[machineConfigOperatorImage] = overrideMachineConfigOperatorImage
	}

	return registryOverrides, nil
}

func lookupMappedImage(ocpOverrides map[string][]string, image string) (string, error) {
	ref, err := reference.Parse(image)
	if err != nil {
		return "", fmt.Errorf("failed to parse image (%s): %w", image, err)
	}
	for source, replacements := range ocpOverrides {
		if ref.AsRepository().String() == source {
			newRef := fmt.Sprintf("%s@%s", replacements[0], ref.ID)
			return newRef, nil
		}
	}
	return image, nil
}
