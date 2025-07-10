package cvo

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

func (cvo *clusterVersionOperator) adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	if cvo.enableCVOManagementClusterMetricsAccess {
		if deployment.Spec.Template.Labels == nil {
			deployment.Spec.Template.Labels = map[string]string{}
		}
		deployment.Spec.Template.Labels[config.NeedMetricsServerAccessLabel] = "true"
		deployment.Spec.Template.Spec.ServiceAccountName = ComponentName
	}

	featureSet := configv1.Default
	if cpContext.HCP.Spec.Configuration != nil && cpContext.HCP.Spec.Configuration.FeatureGate != nil {
		featureSet = cpContext.HCP.Spec.Configuration.FeatureGate.FeatureSet
	}

	// The CVO prepare-payload script needs the ReleaseImage digest for disconnected environments
	controlPlaneReleaseImage, dataPlaneReleaseImage, err := discoverCVOReleaseImages(cpContext)
	if err != nil {
		return fmt.Errorf("failed to discover CVO release images: %w", err)
	}

	util.UpdateContainer("prepare-payload", deployment.Spec.Template.Spec.InitContainers, func(c *corev1.Container) {
		c.Args = []string{
			"-c",
			preparePayloadScript(cpContext.HCP.Spec.Platform.Type, util.HCPOAuthEnabled(cpContext.HCP), featureSet),
		}
		c.Image = controlPlaneReleaseImage
	})

	// the ClusterVersion resource is created by the CVO bootstrap container.
	// we marshal it to json as a means to validate its formatting, which protects
	// us against easily preventable mistakes, such as typos.
	cv := &configv1.ClusterVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterVersion",
			APIVersion: "config.openshift.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
		Spec: configv1.ClusterVersionSpec{
			ClusterID: configv1.ClusterID(cpContext.HCP.Spec.ClusterID),
		},
	}
	cv.Spec.Capabilities = &configv1.ClusterVersionCapabilitiesSpec{
		BaselineCapabilitySet:         configv1.ClusterVersionCapabilitySetNone,
		AdditionalEnabledCapabilities: capabilities.CalculateEnabledCapabilities(cpContext.HCP.Spec.Capabilities),
	}
	clusterVersionJSON, err := json.Marshal(cv)
	if err != nil {
		return err
	}
	util.UpdateContainer("bootstrap", deployment.Spec.Template.Spec.InitContainers, func(c *corev1.Container) {
		c.Env = append(c.Env, corev1.EnvVar{
			Name:  "CLUSTER_VERSION_JSON",
			Value: string(clusterVersionJSON),
		})
	})

	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		util.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "RELEASE_IMAGE",
			Value: dataPlaneReleaseImage,
		})

		if updateService := cpContext.HCP.Spec.UpdateService; updateService != "" {
			c.Args = append(c.Args, "--update-service", string(updateService))
		}
		if cvo.enableCVOManagementClusterMetricsAccess {
			c.Args = append(c.Args, "--use-dns-for-services=true")
			c.Args = append(c.Args, "--metrics-ca-bundle-file=/var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt")
			c.Args = append(c.Args, fmt.Sprintf("--metrics-url=https://thanos-querier.openshift-monitoring.svc:9092?namespace=%s", cpContext.HCP.Namespace))
		}
	})

	return nil
}

var (
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
		// Omitted this file in order to allow the HCCO to create the resource. This allows us to reconcile and sync
		// the HCP.Configuration.operatorhub with OperatorHub object in the HostedCluster. This will only occur once.
		// From that point the HCCO will use the OperatorHub object in the HostedCluster as a source of truth.
		"0000_03_marketplace-operator_02_operatorhub.cr.yaml",
	}
)

func preparePayloadScript(platformType hyperv1.PlatformType, oauthEnabled bool, featureSet configv1.FeatureSet) string {
	payloadDir := "/var/payload"
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
	toRemove := resourcesToRemove(platformType)
	if len(toRemove) > 0 {
		// NOTE: the name of the cleanup file indicates the CVO runlevel for the cleanup.
		// A level of 0000_01 forces the cleanup to happen first without waiting for any cluster operators to
		// become available.
		stmts = append(stmts, fmt.Sprintf("cat > %s/release-manifests/0000_01_cleanup.yaml <<EOF", payloadDir))
	}
	for _, obj := range toRemove {
		name := obj.GetName()
		namespace := obj.GetNamespace()
		gvk, err := apiutil.GVKForObject(obj, hyperapi.Scheme)
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

func resourcesToRemove(platformType hyperv1.PlatformType) []client.Object {
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
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "csi-snapshot-controller", Namespace: "openshift-cluster-storage-operator"}},
		}
	}
}

func discoverCVOReleaseImages(cpContext component.WorkloadContext) (string, string, error) {
	var (
		controlPlaneReleaseImage string
		dataPlaneReleaseImage    string
	)

	pullSecret := common.PullSecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext.Context, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return "", "", fmt.Errorf("failed to get pull secret for namespace %s: %w", cpContext.HCP.Namespace, err)
	}
	pullSecretBytes := pullSecret.Data[corev1.DockerConfigJsonKey]

	cpReleaseImage := cpContext.HCP.Spec.ReleaseImage
	if cpContext.HCP.Spec.ControlPlaneReleaseImage != nil {
		cpReleaseImage = *cpContext.HCP.Spec.ControlPlaneReleaseImage
	}

	_, controlPlaneReleaseImageRef, err := cpContext.ImageMetadataProvider.GetDigest(cpContext.Context, cpReleaseImage, pullSecretBytes)
	if err != nil {
		return "", "", fmt.Errorf("failed to get control plane release image digest %s: %w", controlPlaneReleaseImageRef, err)
	}
	controlPlaneReleaseImage = controlPlaneReleaseImageRef.String()

	if cpReleaseImage != cpContext.HCP.Spec.ReleaseImage {
		_, dataPlaneReleaseImageRef, err := cpContext.ImageMetadataProvider.GetDigest(cpContext.Context, cpContext.HCP.Spec.ReleaseImage, pullSecret.Data[corev1.DockerConfigJsonKey])
		if err != nil {
			return "", "", fmt.Errorf("failed to get data plane release image digest %s: %w", cpContext.HCP.Spec.ReleaseImage, err)
		}

		dataPlaneReleaseImage = dataPlaneReleaseImageRef.String()
	} else {
		dataPlaneReleaseImage = controlPlaneReleaseImage
	}

	return controlPlaneReleaseImage, dataPlaneReleaseImage, nil
}
