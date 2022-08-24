package cvo

import (
	"fmt"
	"path"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/util"
)

var (
	volumeMounts = util.PodVolumeMounts{
		cvoContainerMain().Name: {
			cvoVolumeUpdatePayloads().Name: "/etc/cvo/updatepayloads",
			cvoVolumeKubeconfig().Name:     "/etc/openshift/kubeconfig",
			cvoVolumePayload().Name:        "/var/payload",
			cvoVolumeServerCert().Name:     "/etc/kubernetes/certs/server",
		},
		cvoContainerPrepPayload().Name: {
			cvoVolumePayload().Name: "/var/payload",
		},
		cvoContainerBootstrap().Name: {
			cvoVolumePayload().Name:    "/var/payload",
			cvoVolumeKubeconfig().Name: "/etc/kubernetes",
		},
	}

	// TODO: These manifests should eventually be removed from the CVO payload by annotating
	// them with the proper cluster profile in the OLM repository.
	manifestsToOmit = []string{
		"0000_50_olm_00-pprof-config.yaml",
		"0000_50_olm_00-pprof-rbac.yaml",
		"0000_50_olm_00-pprof-secret.yaml",
		"0000_50_olm_01-olm-operator.serviceaccount.yaml",
		"0000_50_olm_02-services.yaml",
		"0000_50_olm_06-psm-operator.deployment.yaml",
		"0000_50_olm_06-psm-operator.deployment.ibm-cloud-managed.yaml",
		"0000_50_olm_07-olm-operator.deployment.ibm-cloud-managed.yaml",
		"0000_50_olm_07-olm-operator.deployment.yaml",
		"0000_50_olm_07-collect-profiles.cronjob.yaml",
		"0000_50_olm_08-catalog-operator.deployment.ibm-cloud-managed.yaml",
		"0000_50_olm_08-catalog-operator.deployment.yaml",
		"0000_50_olm_15-packageserver.clusterserviceversion.yaml",
		"0000_50_olm_99-operatorstatus.yaml",
		"0000_90_olm_00-service-monitor.yaml",
		"0000_90_olm_01-prometheus-rule.yaml",
		"0000_50_operator-marketplace_04_service_account.yaml",
		"0000_50_operator-marketplace_05_role.yaml",
		"0000_50_operator-marketplace_06_role_binding.yaml",
		"0000_50_operator-marketplace_07_configmap.yaml",
		"0000_50_operator-marketplace_08_service.yaml",
		"0000_50_operator-marketplace_09_operator-ibm-cloud-managed.yaml",
		"0000_50_operator-marketplace_09_operator.yaml",
		"0000_50_operator-marketplace_10_clusteroperator.yaml",
		"0000_50_operator-marketplace_11_service_monitor.yaml",
		"0000_50_cluster-ingress-operator_02-deployment-ibm-cloud-managed.yaml",
		"0000_70_cluster-network-operator_02_rbac.yaml",
		"0000_70_cluster-network-operator_03_deployment-ibm-cloud-managed.yaml",
		"0000_80_machine-config-operator_01_machineconfig.crd.yaml",
		"0000_80_machine-config-operator_01_machineconfigpool.crd.yaml",
		"0000_50_cluster-node-tuning-operator_50-operator-ibm-cloud-managed.yaml",
		"0000_50_cluster-node-tuning-operator_60-clusteroperator.yaml",
		"0000_50_cluster-image-registry-operator_07-operator-ibm-cloud-managed.yaml",
		"0000_50_cluster-image-registry-operator_07-operator-service.yaml",
		"0000_90_cluster-image-registry-operator_02_operator-servicemonitor.yaml",

		// TODO: Remove these when cluster profiles annotations are fixed
		"0000_50_cloud-credential-operator_01-operator-config.yaml",
		"0000_50_cluster-authentication-operator_02_config.cr.yaml",
		"0000_90_etcd-operator_03_prometheusrule.yaml",
	}
)

func cvoLabels() map[string]string {
	return map[string]string{
		"app": "cluster-version-operator",
		// value for compatibility with roks-toolkit clusters
		"k8s-app":                     "cluster-version-operator",
		hyperv1.ControlPlaneComponent: "cluster-version-operator",
	}
}

var port int32 = 8443

func ReconcileDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, deploymentConfig config.DeploymentConfig, image, cliImage, availabilityProberImage, clusterID string, apiPort *int32) error {
	ownerRef.ApplyTo(deployment)

	// preserve existing resource requirements for main CVO container
	mainContainer := util.FindContainer(cvoContainerMain().Name, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		deploymentConfig.SetContainerResourcesIfPresent(mainContainer)
	}
	selector := deployment.Spec.Selector
	if selector == nil {
		selector = &metav1.LabelSelector{
			MatchLabels: cvoLabels(),
		}
	}
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: selector,
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: cvoLabels(),
			},
			Spec: corev1.PodSpec{
				AutomountServiceAccountToken: pointer.BoolPtr(false),
				InitContainers: []corev1.Container{
					util.BuildContainer(cvoContainerPrepPayload(), buildCVOContainerPrepPayload(image)),
					util.BuildContainer(cvoContainerBootstrap(), buildCVOContainerBootstrap(cliImage, clusterID)),
				},
				Containers: []corev1.Container{
					util.BuildContainer(cvoContainerMain(), buildCVOContainerMain(image)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(cvoVolumePayload(), buildCVOVolumePayload),
					util.BuildVolume(cvoVolumeKubeconfig(), buildCVOVolumeKubeconfig),
					util.BuildVolume(cvoVolumeUpdatePayloads(), buildCVOVolumeUpdatePayloads),
					util.BuildVolume(cvoVolumeServerCert(), buildCVOVolumeServerCert),
				},
			},
		},
	}
	deploymentConfig.ApplyTo(deployment)
	util.AvailabilityProber(
		kas.InClusterKASReadyURL(deployment.Namespace, apiPort),
		availabilityProberImage,
		&deployment.Spec.Template.Spec,
		func(o *util.AvailabilityProberOpts) {
			o.KubeconfigVolumeName = cvoVolumeKubeconfig().Name
		},
	)
	return nil
}

func cvoContainerPrepPayload() *corev1.Container {
	return &corev1.Container{
		Name: "prepare-payload",
	}
}

func cvoContainerBootstrap() *corev1.Container {
	return &corev1.Container{
		Name: "bootstrap",
	}
}

func cvoContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "cluster-version-operator",
	}
}

func buildCVOContainerPrepPayload(image string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.Command = []string{"/bin/bash"}
		c.Args = []string{
			"-c",
			preparePayloadScript(),
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func buildCVOContainerBootstrap(image, clusterID string) func(*corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.Command = []string{"/bin/bash"}
		c.Args = []string{
			"-c",
			cvoBootrapScript(clusterID),
		}
		c.Resources.Requests = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("10Mi"),
		}
		c.Env = []corev1.EnvVar{
			{
				Name:  "KUBECONFIG",
				Value: path.Join(volumeMounts.Path(c.Name, cvoVolumeKubeconfig().Name), kas.KubeconfigKey),
			},
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

type resourceDesc struct {
	name       string
	namespace  string
	apiVersion string
	kind       string
}

func resourcesToRemove() []resourceDesc {
	return []resourceDesc{
		{
			apiVersion: "apps/v1",
			kind:       "Deployment",
			name:       "network-operator",
			namespace:  "openshift-network-operator",
		},
		{
			apiVersion: "rbac.authorization.k8s.io/v1",
			kind:       "ClusterRoleBinding",
			name:       "default-account-cluster-network-operator",
		},
		/* TODO: Add these to the remove list when no longer used for POCs (IBM) */
		/*
			{
				apiVersion: "apiextensions.k8s.io/v1",
				kind:       "CustomResourceDefinition",
				name:       "machineconfigs.machineconfiguration.openshift.io",
			},
			{
				apiVersion: "apiextensions.k8s.io/v1",
				kind:       "CustomResourceDefinition",
				name:       "machineconfigpools.machineconfiguration.openshift.io",
			},
		*/
		{
			apiVersion: "apps/v1",
			kind:       "Deployment",
			name:       "cluster-node-tuning-operator",
			namespace:  "openshift-cluster-node-tuning-operator",
		},
		{
			apiVersion: "apps/v1",
			kind:       "Deployment",
			name:       "cluster-image-registry-operator",
			namespace:  "openshift-image-registry",
		},
	}
}

func preparePayloadScript() string {
	payloadDir := volumeMounts.Path(cvoContainerPrepPayload().Name, cvoVolumePayload().Name)
	var stmts []string

	stmts = append(stmts,
		fmt.Sprintf("cp -R /manifests %s/", payloadDir),
		fmt.Sprintf("rm %s/manifests/*_deployment.yaml", payloadDir),
		fmt.Sprintf("rm %s/manifests/*_servicemonitor.yaml", payloadDir),
		fmt.Sprintf("cp -R /release-manifests %s/", payloadDir),
	)
	for _, manifest := range manifestsToOmit {
		stmts = append(stmts, fmt.Sprintf("rm %s", path.Join(payloadDir, "release-manifests", manifest)))
	}
	toRemove := resourcesToRemove()
	if len(toRemove) > 0 {
		// NOTE: the name of the cleanup file indicates the CVO runlevel for the cleanup.
		// A level of 0000_01 forces the cleanup to happen first without waiting for any cluster operators to
		// become available.
		stmts = append(stmts, fmt.Sprintf("cat > %s/release-manifests/0000_01_cleanup.yaml <<EOF", payloadDir))
	}
	for _, desc := range resourcesToRemove() {
		stmts = append(stmts,
			"---",
			fmt.Sprintf("apiVersion: %s", desc.apiVersion),
			fmt.Sprintf("kind: %s", desc.kind),
			"metadata:",
			fmt.Sprintf("  name: %s", desc.name),
		)
		if desc.namespace != "" {
			stmts = append(stmts, fmt.Sprintf("  namespace: %s", desc.namespace))
		}
		stmts = append(stmts,
			"  annotations:",
			"    include.release.openshift.io/ibm-cloud-managed: \"true\"",
			"    release.openshift.io/delete: \"true\"",
		)
	}
	return strings.Join(stmts, "\n")
}

func cvoBootrapScript(clusterID string) string {
	payloadDir := volumeMounts.Path(cvoContainerBootstrap().Name, cvoVolumePayload().Name)
	var scriptTemplate = `#!/bin/bash
set -euo pipefail
cat > /tmp/clusterversion.yaml <<EOF
apiVersion: config.openshift.io/v1
kind: ClusterVersion
metadata:
  name: version
spec:
  clusterID: %s
EOF
oc get ns openshift-config &> /dev/null || oc create ns openshift-config
oc get ns openshift-config-managed &> /dev/null || oc create ns openshift-config-managed
while true; do
  echo "Applying CVO bootstrap manifests"
  if oc apply -f %s/manifests; then
    echo "Bootstrap manifests applied successfully."
    break
  fi
  sleep 1
done
oc get clusterversion/version &> /dev/null || oc create -f /tmp/clusterversion.yaml
`
	return fmt.Sprintf(scriptTemplate, clusterID, payloadDir)
}

func buildCVOContainerMain(image string) func(c *corev1.Container) {
	cpath := func(vol, file string) string {
		return path.Join(volumeMounts.Path(cvoContainerMain().Name, vol), file)
	}
	return func(c *corev1.Container) {
		c.Image = image
		c.Command = []string{"cluster-version-operator"}
		c.Args = []string{
			"start",
			"--release-image",
			image,
			"--enable-auto-update=false",
			"--kubeconfig",
			path.Join(volumeMounts.Path(c.Name, cvoVolumeKubeconfig().Name), kas.KubeconfigKey),
			fmt.Sprintf("--listen=0.0.0.0:%d", port),
			fmt.Sprintf("--serving-cert-file=%s", cpath(cvoVolumeServerCert().Name, corev1.TLSCertKey)),
			fmt.Sprintf("--serving-key-file=%s", cpath(cvoVolumeServerCert().Name, corev1.TLSPrivateKeyKey)),
			"--v=4",
		}
		c.Env = []corev1.EnvVar{
			{
				Name:  "PAYLOAD_OVERRIDE",
				Value: volumeMounts.Path(c.Name, cvoVolumePayload().Name),
			},
			{
				Name:  "CLUSTER_PROFILE",
				Value: "ibm-cloud-managed",
			},
			{
				Name: "NODE_NAME",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "spec.nodeName",
					},
				},
			},
		}
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "https",
				ContainerPort: port,
				Protocol:      corev1.ProtocolTCP,
			},
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func cvoVolumeUpdatePayloads() *corev1.Volume {
	return &corev1.Volume{
		Name: "update-payloads",
	}
}

func cvoVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func cvoVolumePayload() *corev1.Volume {
	return &corev1.Volume{
		Name: "payload",
	}
}

func buildCVOVolumeUpdatePayloads(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}

func buildCVOVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.KASServiceKubeconfigSecret("").Name
}

func buildCVOVolumePayload(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}

func cvoVolumeServerCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "server-crt",
	}
}
func buildCVOVolumeServerCert(v *corev1.Volume) {
	if v.Secret == nil {
		v.Secret = &corev1.SecretVolumeSource{}
	}
	v.Secret.DefaultMode = pointer.Int32Ptr(416)
	v.Secret.SecretName = manifests.ClusterVersionOperatorServerCertSecret("").Name
}

func ReconcileService(svc *corev1.Service, owner config.OwnerRef) error {
	owner.ApplyTo(svc)
	svc.Spec.Selector = cvoLabels()

	// Ensure labels propagate to endpoints so service monitors can select them
	if svc.Labels == nil {
		svc.Labels = map[string]string{}
	}
	for k, v := range cvoLabels() {
		svc.Labels[k] = v
	}

	svc.Spec.Type = corev1.ServiceTypeClusterIP

	if len(svc.Spec.Ports) == 0 {
		svc.Spec.Ports = []corev1.ServicePort{
			{
				Name: "https",
			},
		}
	}

	svc.Spec.Ports[0].Port = port
	svc.Spec.Ports[0].Name = "https"
	svc.Spec.Ports[0].TargetPort = intstr.FromString("https")
	svc.Spec.Ports[0].Protocol = corev1.ProtocolTCP

	return nil
}

func ReconcileServiceMonitor(sm *prometheusoperatorv1.ServiceMonitor, ownerRef config.OwnerRef, clusterID string, metricsSet metrics.MetricsSet) error {
	ownerRef.ApplyTo(sm)

	sm.Spec.Selector.MatchLabels = cvoLabels()
	sm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{sm.Namespace},
	}
	targetPort := intstr.FromString("https")
	sm.Spec.Endpoints = []prometheusoperatorv1.Endpoint{
		{
			Interval:   "15s",
			TargetPort: &targetPort,
			Scheme:     "https",
			TLSConfig: &prometheusoperatorv1.TLSConfig{
				SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
					ServerName: "cluster-version-operator",
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
						Secret: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: manifests.MetricsClientCertSecret(sm.Namespace).Name,
							},
							Key: "ca.crt",
						},
					},
				},
			},
			MetricRelabelConfigs: metrics.CVORelabelConfigs(metricsSet),
		},
	}

	util.ApplyClusterIDLabel(&sm.Spec.Endpoints[0], clusterID)

	return nil
}
