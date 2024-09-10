package cvo

import (
	"fmt"
	"path"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

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

	util.UpdateContainer("prepare-payload", deployment.Spec.Template.Spec.InitContainers, func(c *corev1.Container) {
		c.Args = []string{
			"-c",
			preparePayloadScript(cpContext.HCP.Spec.Platform.Type, util.HCPOAuthEnabled(cpContext.HCP)),
		}
	})
	util.UpdateContainer("bootstrap", deployment.Spec.Template.Spec.InitContainers, func(c *corev1.Container) {
		c.Env = append(c.Env, corev1.EnvVar{
			Name:  "CLUSTER_ID",
			Value: cpContext.HCP.Spec.ClusterID,
		})
	})

	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		util.UpsertEnvVar(c, corev1.EnvVar{
			Name:  "RELEASE_IMAGE",
			Value: cpContext.HCP.Spec.ReleaseImage,
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

func preparePayloadScript(platformType hyperv1.PlatformType, oauthEnabled bool) string {
	payloadDir := "/var/payload"
	var stmts []string

	stmts = append(stmts,
		fmt.Sprintf("cp -R /manifests %s/", payloadDir),
		fmt.Sprintf("rm %s/manifests/*_deployment.yaml", payloadDir),
		fmt.Sprintf("rm %s/manifests/*_servicemonitor.yaml", payloadDir),
		fmt.Sprintf("cp -R /release-manifests %s/", payloadDir),
	)
	for _, manifest := range manifestsToOmit {
		if platformType == hyperv1.IBMCloudPlatform || platformType == hyperv1.PowerVSPlatform {
			if manifest == "0000_50_cluster-storage-operator_10_deployment-ibm-cloud-managed.yaml" || manifest == "0000_50_cluster-csi-snapshot-controller-operator_07_deployment-ibm-cloud-managed.yaml" {
				continue
			}
		}
		stmts = append(stmts, fmt.Sprintf("rm %s", path.Join(payloadDir, "release-manifests", manifest)))
	}
	if !oauthEnabled {
		stmts = append(stmts, fmt.Sprintf("rm %s", path.Join(payloadDir, "release-manifests", "0000_50_console-operator_01-oauth.yaml")))
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
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "csi-snapshot-webhook", Namespace: "openshift-cluster-storage-operator"}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "csi-snapshot-controller", Namespace: "openshift-cluster-storage-operator"}},
		}
	}
}
