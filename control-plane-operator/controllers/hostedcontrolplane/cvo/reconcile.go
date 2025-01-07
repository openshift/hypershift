package cvo

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
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
		"0000_50_olm_06-psm-operator.service.yaml",
		"0000_50_olm_06-psm-operator.servicemonitor.yaml",
		"0000_50_olm_07-olm-operator.deployment.ibm-cloud-managed.yaml",
		"0000_50_olm_07-olm-operator.deployment.yaml",
		"0000_50_olm_07-collect-profiles.cronjob.yaml",
		"0000_50_olm_08-catalog-operator.deployment.ibm-cloud-managed.yaml",
		"0000_50_olm_08-catalog-operator.deployment.yaml",
		"0000_50_olm_15-packageserver.clusterserviceversion.yaml",
		"0000_50_olm_99-operatorstatus.yaml",
		"0000_90_olm_00-service-monitor.yaml",
		"0000_50_operator-marketplace_04_service_account.yaml",
		"0000_50_operator-marketplace_05_role.yaml",
		"0000_50_operator-marketplace_06_role_binding.yaml",
		"0000_50_operator-marketplace_07_configmap.yaml",
		"0000_50_operator-marketplace_08_service.yaml",
		"0000_50_operator-marketplace_09_operator-ibm-cloud-managed.yaml",
		"0000_50_operator-marketplace_09_operator.yaml",
		"0000_50_operator-marketplace_10_clusteroperator.yaml",
		"0000_50_operator-marketplace_11_service_monitor.yaml",
		"0000_70_dns-operator_02-deployment-ibm-cloud-managed.yaml",
		"0000_50_cluster-ingress-operator_02-deployment-ibm-cloud-managed.yaml",
		"0000_70_cluster-network-operator_03_deployment-ibm-cloud-managed.yaml",
		"0000_80_machine-config_01_containerruntimeconfigs.crd.yaml",
		"0000_80_machine-config_01_kubeletconfigs.crd.yaml",
		"0000_80_machine-config_01_machineconfigs.crd.yaml",
		"0000_80_machine-config_01_machineconfigpools-Default.crd.yaml",
		"0000_50_cluster-node-tuning-operator_20-performance-profile.crd.yaml",
		"0000_50_cluster-node-tuning-operator_50-operator-ibm-cloud-managed.yaml",
		"0000_50_cluster-image-registry-operator_07-operator-ibm-cloud-managed.yaml",
		"0000_50_cluster-image-registry-operator_07-operator-service.yaml",
		"0000_90_cluster-image-registry-operator_02_operator-servicemonitor.yaml",
		"0000_50_cluster-storage-operator_10_deployment-ibm-cloud-managed.yaml",

		// TODO: Remove these when cluster profiles annotations are fixed
		"0000_50_cloud-credential-operator_01-operator-config.yaml",
		"0000_50_cluster-authentication-operator_02_config.cr.yaml",
		"0000_90_etcd-operator_03_prometheusrule.yaml",

		// TODO: Remove when cluster-csi-snapshot-controller-operator stops shipping
		// its ibm-cloud-managed deployment.
		"0000_50_cluster-csi-snapshot-controller-operator_07_deployment-ibm-cloud-managed.yaml",
		// Omitted this file in order to allow the HCCO to create the resource. This allow us to reconcile and sync
		// the HCP.Configuration.operatorhub with OperatorHub object in the HostedCluster. This will only occur once.
		// From that point the HCCO will use the OperatorHub object in the HostedCluster as a source of truth.
		"0000_03_marketplace-operator_02_operatorhub.cr.yaml",
	}
)

func cvoLabels() map[string]string {
	return map[string]string{
		"app": "cluster-version-operator",
		// value for compatibility with roks-toolkit clusters
		"k8s-app":                          "cluster-version-operator",
		hyperv1.ControlPlaneComponentLabel: "cluster-version-operator",
	}
}

var port int32 = 8443

func ReconcileDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, deploymentConfig config.DeploymentConfig, controlPlaneReleaseImage, dataPlaneReleaseImage, cliImage, availabilityProberImage, clusterID string, updateService configv1.URL, platformType hyperv1.PlatformType, oauthEnabled, enableCVOManagementClusterMetricsAccess bool, featureSet configv1.FeatureSet) error {
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
				AutomountServiceAccountToken: ptr.To(false),
				InitContainers: []corev1.Container{
					util.BuildContainer(cvoContainerPrepPayload(), buildCVOContainerPrepPayload(dataPlaneReleaseImage, platformType, oauthEnabled, featureSet)),
					util.BuildContainer(cvoContainerBootstrap(), buildCVOContainerBootstrap(cliImage, clusterID)),
				},
				Containers: []corev1.Container{
					util.BuildContainer(cvoContainerMain(), buildCVOContainerMain(controlPlaneReleaseImage, dataPlaneReleaseImage, deployment.Namespace, updateService, enableCVOManagementClusterMetricsAccess)),
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
	deployment.Spec.Template.Spec.AutomountServiceAccountToken = ptr.To(false)
	if enableCVOManagementClusterMetricsAccess {
		deployment.Spec.Template.Spec.ServiceAccountName = manifests.ClusterVersionOperatorServiceAccount("").Name
		deployment.Spec.Template.Spec.AutomountServiceAccountToken = ptr.To(true)
	}
	deploymentConfig.ApplyTo(deployment)
	util.AvailabilityProber(
		kas.InClusterKASReadyURL(platformType),
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

func buildCVOContainerPrepPayload(image string, platformType hyperv1.PlatformType, oauthEnabled bool, featureSet configv1.FeatureSet) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.Command = []string{"/bin/bash"}
		c.Args = []string{
			"-c",
			preparePayloadScript(platformType, oauthEnabled, featureSet),
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
			cvoBootstrapScript(clusterID),
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

func ResourcesToRemove(platformType hyperv1.PlatformType) []client.Object {
	switch platformType {
	case hyperv1.IBMCloudPlatform, hyperv1.PowerVSPlatform:
		return []client.Object{
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "network-operator", Namespace: "openshift-network-operator"}},
			&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "default-account-cluster-network-operator"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "cluster-node-tuning-operator", Namespace: "openshift-cluster-node-tuning-operator"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "cluster-image-registry-operator", Namespace: "openshift-image-registry"}},
		}
	default:
		return []client.Object{
			&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "machineconfigs.machineconfiguration.openshift.io"}},
			&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "machineconfigpools.machineconfiguration.openshift.io"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "network-operator", Namespace: "openshift-network-operator"}},
			&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "default-account-cluster-network-operator"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "cluster-node-tuning-operator", Namespace: "openshift-cluster-node-tuning-operator"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "cluster-image-registry-operator", Namespace: "openshift-image-registry"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "cluster-storage-operator", Namespace: "openshift-cluster-storage-operator"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "csi-snapshot-controller-operator", Namespace: "openshift-cluster-storage-operator"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "aws-ebs-csi-driver-operator", Namespace: "openshift-cluster-csi-drivers"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "aws-ebs-csi-driver-controller", Namespace: "openshift-cluster-csi-drivers"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "csi-snapshot-webhook", Namespace: "openshift-cluster-storage-operator"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "csi-snapshot-controller", Namespace: "openshift-cluster-storage-operator"}},
		}
	}
}

func preparePayloadScript(platformType hyperv1.PlatformType, oauthEnabled bool, featureSet configv1.FeatureSet) string {
	payloadDir := volumeMounts.Path(cvoContainerPrepPayload().Name, cvoVolumePayload().Name)
	var stmts []string

	stmts = append(stmts,
		fmt.Sprintf("cp -R /manifests %s/", payloadDir),
		fmt.Sprintf("rm -f %s/manifests/*_deployment.yaml", payloadDir),
		fmt.Sprintf("rm -f %s/manifests/*_servicemonitor.yaml", payloadDir),
		fmt.Sprintf("cp -R /release-manifests %s/", payloadDir),
	)

	// NOTE: We would need part of the manifest.Include logic (https://github.com/openshift/library-go/blob/0064ad7bd060b9fd52f7840972c1d3e72186d0f0/pkg/manifest/manifest.go#L190-L196)
	// to properly evaluate which CVO manifests to select based on featureset. In the absence of that logic, use simple filename filtering, which is not ideal
	// but better than nothing.  Ideally, we filter based on the feature-set annotation in the manifests.
	switch featureSet {
	case configv1.Default, "":
		stmts = append(stmts,
			fmt.Sprintf("rm -f %s/manifests/*-CustomNoUpgrade*.yaml", payloadDir),
			fmt.Sprintf("rm -f %s/manifests/*-DevPreviewNoUpgrade*.yaml", payloadDir),
			fmt.Sprintf("rm -f %s/manifests/*-TechPreviewNoUpgrade*.yaml", payloadDir),
		)
	case configv1.CustomNoUpgrade:
		stmts = append(stmts,
			fmt.Sprintf("rm -f %s/manifests/*-Default*.yaml", payloadDir),
			fmt.Sprintf("rm -f %s/manifests/*-DevPreviewNoUpgrade*.yaml", payloadDir),
			fmt.Sprintf("rm -f %s/manifests/*-TechPreviewNoUpgrade*.yaml", payloadDir),
		)
	case configv1.DevPreviewNoUpgrade:
		stmts = append(stmts,
			fmt.Sprintf("rm -f %s/manifests/*-Default*.yaml", payloadDir),
			fmt.Sprintf("rm -f %s/manifests/*-CustomNoUpgrade*.yaml", payloadDir),
			fmt.Sprintf("rm -f %s/manifests/*-TechPreviewNoUpgrade*.yaml", payloadDir),
		)
	case configv1.TechPreviewNoUpgrade:
		stmts = append(stmts,
			fmt.Sprintf("rm -f %s/manifests/*-Default*.yaml", payloadDir),
			fmt.Sprintf("rm -f %s/manifests/*-CustomNoUpgrade*.yaml", payloadDir),
			fmt.Sprintf("rm -f %s/manifests/*-DevPreviewNoUpgrade*.yaml", payloadDir),
		)
	}

	for _, manifest := range manifestsToOmit {
		if platformType == hyperv1.IBMCloudPlatform || platformType == hyperv1.PowerVSPlatform {
			if manifest == "0000_50_cluster-storage-operator_10_deployment-ibm-cloud-managed.yaml" || manifest == "0000_50_cluster-csi-snapshot-controller-operator_07_deployment-ibm-cloud-managed.yaml" {
				continue
			}
		}
		stmts = append(stmts, fmt.Sprintf("rm -f %s", path.Join(payloadDir, "release-manifests", manifest)))
	}
	if !oauthEnabled {
		stmts = append(stmts, fmt.Sprintf("rm -f %s", path.Join(payloadDir, "release-manifests", "0000_50_console-operator_01-oauth.yaml")))
	}
	toRemove := ResourcesToRemove(platformType)
	if len(toRemove) > 0 {
		// NOTE: the name of the cleanup file indicates the CVO runlevel for the cleanup.
		// A level of 0000_01 forces the cleanup to happen first without waiting for any cluster operators to
		// become available.
		stmts = append(stmts, fmt.Sprintf("cat > %s/release-manifests/0000_01_cleanup.yaml <<EOF", payloadDir))
	}
	for _, obj := range toRemove {
		name := obj.GetName()
		namespace := obj.GetNamespace()
		gvk, err := apiutil.GVKForObject(obj, api.Scheme)
		if err != nil {
			continue
		}
		stmts = append(stmts,
			"---",
			fmt.Sprintf("apiVersion: %s", gvk.GroupVersion().String()),
			fmt.Sprintf("kind: %s", gvk.Kind),
			"metadata:",
			fmt.Sprintf("  name: %s", name),
		)
		if namespace != "" {
			stmts = append(stmts, fmt.Sprintf("  namespace: %s", namespace))
		}
		stmts = append(stmts,
			"  annotations:",
			"    include.release.openshift.io/ibm-cloud-managed: \"true\"",
			"    release.openshift.io/delete: \"true\"",
		)
	}
	stmts = append(stmts, "EOF")
	return strings.Join(stmts, "\n")
}

func cvoBootstrapScript(clusterID string) string {
	enabledCaps := sets.New[configv1.ClusterVersionCapability](
		configv1.ClusterVersionCapabilitySets[configv1.ClusterVersionCapabilitySetCurrent]...)
	enabledCaps = enabledCaps.Delete(configv1.ClusterVersionCapabilityImageRegistry)
	capList := enabledCaps.UnsortedList()
	slices.SortFunc(capList, func(a, b configv1.ClusterVersionCapability) int {
		return strings.Compare(string(a), string(b))
	})

	payloadDir := volumeMounts.Path(cvoContainerBootstrap().Name, cvoVolumePayload().Name)
	cv := &configv1.ClusterVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterVersion",
			APIVersion: "config.openshift.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: configv1.ClusterID(clusterID),
			Capabilities: &configv1.ClusterVersionCapabilitiesSpec{
				BaselineCapabilitySet:         configv1.ClusterVersionCapabilitySetNone,
				AdditionalEnabledCapabilities: capList,
			},
		},
	}

	// TODO(thomas): ignore the error for simplicity sake today
	cvJson, _ := json.Marshal(cv)

	var scriptTemplate = `#!/bin/bash
set -euo pipefail
MANIFEST_DIR=%s/manifests
ls -la ${MANIFEST_DIR}
cat > /tmp/clusterversion.json <<-EOF
%s
EOF
oc get ns openshift-config &> /dev/null || oc create ns openshift-config
oc get ns openshift-config-managed &> /dev/null || oc create ns openshift-config-managed
oc apply -f ${MANIFEST_DIR}/0000_00_cluster-version-operator_01_clusterversions*
oc apply -f /tmp/clusterversion.json
oc get clusterversion.config.openshift.io/version -oyaml
while true; do
  echo "Applying CVO bootstrap manifests..."
  if oc apply -f ${MANIFEST_DIR}; then
    echo "Bootstrap manifests applied successfully."
    break
  fi
  sleep 1
done
`
	return fmt.Sprintf(scriptTemplate, payloadDir, string(cvJson))
}

func buildCVOContainerMain(controlPlaneReleaseImage, dataPlaneReleaseImage, namespace string, updateService configv1.URL, enableCVOManagementClusterMetricsAccess bool) func(c *corev1.Container) {
	cpath := func(vol, file string) string {
		return path.Join(volumeMounts.Path(cvoContainerMain().Name, vol), file)
	}
	return func(c *corev1.Container) {
		c.Image = controlPlaneReleaseImage
		c.Command = []string{"cluster-version-operator"}
		c.Args = []string{
			"start",
			"--release-image",
			dataPlaneReleaseImage,
			"--enable-auto-update=false",
			"--kubeconfig",
			path.Join(volumeMounts.Path(c.Name, cvoVolumeKubeconfig().Name), kas.KubeconfigKey),
			fmt.Sprintf("--listen=0.0.0.0:%d", port),
			fmt.Sprintf("--serving-cert-file=%s", cpath(cvoVolumeServerCert().Name, corev1.TLSCertKey)),
			fmt.Sprintf("--serving-key-file=%s", cpath(cvoVolumeServerCert().Name, corev1.TLSPrivateKeyKey)),
			"--hypershift=true",
			"--v=4",
		}
		if updateService != "" {
			c.Args = append(c.Args, "--update-service", string(updateService))
		}
		if enableCVOManagementClusterMetricsAccess {
			c.Args = append(c.Args, "--use-dns-for-services=true")
			c.Args = append(c.Args, "--metrics-ca-bundle-file=/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt")
			c.Args = append(c.Args, fmt.Sprintf("--metrics-url=https://thanos-querier.openshift-monitoring.svc:9092?namespace=%s", namespace))
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
	v.Secret.DefaultMode = ptr.To[int32](0640)
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
	v.Secret.DefaultMode = ptr.To[int32](0640)
	v.Secret.SecretName = manifests.ClusterVersionOperatorServerCertSecret("").Name
}

func ReconcileService(svc *corev1.Service, owner config.OwnerRef) error {
	owner.ApplyTo(svc)
	svc.Spec.Selector = cvoLabels()

	// Setting this to PreferDualStack will make the service to be created with IPv4 and IPv6 addresses if the management cluster is dual stack.
	IPFamilyPolicy := corev1.IPFamilyPolicyPreferDualStack
	svc.Spec.IPFamilyPolicy = &IPFamilyPolicy

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
			TargetPort: &targetPort,
			Scheme:     "https",
			TLSConfig: &prometheusoperatorv1.TLSConfig{
				SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
					ServerName: "cluster-version-operator",
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
			MetricRelabelConfigs: metrics.CVORelabelConfigs(metricsSet),
		},
	}

	util.ApplyClusterIDLabel(&sm.Spec.Endpoints[0], clusterID)

	return nil
}

func ReconcileRole(role *rbacv1.Role, ownerRef config.OwnerRef) error {
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{"metrics.k8s.io"},
			Resources: []string{"pods"},
			Verbs:     []string{"get"},
		},
	}
	return nil
}

func ReconcileRoleBinding(rb *rbacv1.RoleBinding, role *rbacv1.Role, ownerRef config.OwnerRef, namespace string) error {
	rb.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.SchemeGroupVersion.Group,
		Kind:     "Role",
		Name:     role.Name,
	}
	rb.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      manifests.ClusterVersionOperatorServiceAccount("").Name,
			Namespace: namespace,
		},
	}
	return nil
}

func ReconcileServiceAccount(sa *corev1.ServiceAccount, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(sa)
	util.EnsurePullSecret(sa, controlplaneoperator.PullSecret("").Name)
	return nil
}
