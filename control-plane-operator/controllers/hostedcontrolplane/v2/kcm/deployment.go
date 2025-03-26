package kcm

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP
	serviceServingCA, err := getServiceServingCA(cpContext)
	if err != nil {
		return err
	}

	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args,
			fmt.Sprintf("--cluster-cidr=%s", util.FirstClusterCIDR(hcp.Spec.Networking.ClusterNetwork)),
			fmt.Sprintf("--service-cluster-ip-range=%s", util.FirstServiceCIDR(hcp.Spec.Networking.ServiceNetwork)),
		)
		// This value comes from the Cloud Provider Azure documentation: https://cloud-provider-azure.sigs.k8s.io/install/azure-ccm/#kube-controller-manager
		if hcp.Spec.Platform.Type == hyperv1.AzurePlatform {
			c.Args = append(c.Args, fmt.Sprintf("--cloud-provider=%s", "external"))
		}

		if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
			c.Args = append(c.Args, "--node-monitor-grace-period=55s")
		} else {
			c.Args = append(c.Args, "--node-monitor-grace-period=50s")
		}

		if tlsMinVersion := config.MinTLSVersion(hcp.Spec.Configuration.GetTLSSecurityProfile()); tlsMinVersion != "" {
			c.Args = append(c.Args, fmt.Sprintf("--tls-min-version=%s", tlsMinVersion))
		}
		if cipherSuites := config.CipherSuites(hcp.Spec.Configuration.GetTLSSecurityProfile()); len(cipherSuites) != 0 {
			c.Args = append(c.Args, fmt.Sprintf("--tls-cipher-suites=%s", strings.Join(cipherSuites, ",")))
		}
		if util.StringListContains(hcp.Annotations[hyperv1.DisableProfilingAnnotation], ComponentName) {
			c.Args = append(c.Args, "--profiling=false")
		}

		for _, f := range config.FeatureGates(hcp.Spec.Configuration.GetFeatureGateSelection()) {
			c.Args = append(c.Args, fmt.Sprintf("--feature-gates=%s", f))
		}

		proxy.SetEnvVars(&c.Env)

		if serviceServingCA != nil {
			deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, corev1.Volume{
				Name: "service-serving-ca",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: serviceServingCA.Name,
						},
					},
				},
			})

			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
				Name:      "service-serving-ca",
				MountPath: "/etc/kubernetes/certs/service-ca",
			})
		}
	})

	return nil
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
