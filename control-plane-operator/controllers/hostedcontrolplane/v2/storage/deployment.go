package storage

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/yaml"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		envReplacer := newEnvironmentReplacer(cpContext.ReleaseImageProvider, cpContext.UserReleaseImageProvider)
		envReplacer.replaceEnvVars(c.Env)

		// For managed Azure, we need to supply a couple of environment variables for CSO to pass on to the CSI controllers for disk and file.
		// CSO passes those on to the CSI deployment here - https://github.com/openshift/cluster-storage-operator/pull/517/files.
		// CSI then mounts the Secrets Provider Class here - https://github.com/openshift/csi-operator/pull/309/files.
		if azureutil.IsAroHCPByHCP(cpContext.HCP) {
			c.Env = append(c.Env,
				corev1.EnvVar{
					Name:  "ARO_HCP_SECRET_PROVIDER_CLASS_FOR_DISK",
					Value: config.ManagedAzureDiskCSISecretStoreProviderClassName,
				},
				corev1.EnvVar{
					Name:  "ARO_HCP_SECRET_PROVIDER_CLASS_FOR_FILE",
					Value: config.ManagedAzureFileCSISecretStoreProviderClassName,
				})
		}

		// We set this so cluster-storage-operator knows which User ID to run the CSI controller pods as.
		// This is needed when these pods are run on a management cluster that is non-OpenShift such as AKS.
		if cpContext.SetDefaultSecurityContext {
			c.Env = append(c.Env, corev1.EnvVar{Name: "RUN_AS_USER", Value: strconv.Itoa(int(cpContext.DefaultSecurityContextUID))})
		}
	})

	return nil
}

func adaptControllerConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	profile := cpContext.HCP.Spec.Configuration.GetTLSSecurityProfile()
	controllerConfig := configv1.GenericControllerConfig{
		ServingInfo: configv1.HTTPServingInfo{
			ServingInfo: configv1.ServingInfo{
				BindAddress:   ":8443",
				CipherSuites:  config.CipherSuites(profile),
				MinTLSVersion: config.MinTLSVersion(profile),
			},
		},
	}

	asJSON, err := json.Marshal(controllerConfig)
	if err != nil {
		return fmt.Errorf("failed to json marshal config: %w", err)
	}

	asMap := map[string]any{}
	if err := json.Unmarshal(asJSON, &asMap); err != nil {
		return fmt.Errorf("failed to json unmarshal config: %w", err)
	}

	asMap["apiVersion"] = configv1.GroupVersion.String()
	asMap["kind"] = "GenericControllerConfig"

	data, err := yaml.Marshal(asMap)
	if err != nil {
		return fmt.Errorf("failed to yaml marshal config: %w", err)
	}

	if cm.Data == nil {
		cm.Data = map[string]string{}
	}

	cm.Data["config.yaml"] = string(data)
	return nil
}
