//go:build e2ev2

// This file is generated. Do not edit manually.
// Run: go run /tmp/generate_workloads.go > generated_workloads.go

package internal

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// WorkloadSpec represents a control plane workload with its pod selector
type WorkloadSpec struct {
	Type        string
	Name        string
	Platform    *hyperv1.PlatformType
	PodSelector map[string]string
}

// GetControlPlaneWorkloads returns a static list of control plane workloads
func GetControlPlaneWorkloads() []WorkloadSpec {
	awsPlatform := hyperv1.AWSPlatform
	azurePlatform := hyperv1.AzurePlatform
	kubevirtPlatform := hyperv1.KubevirtPlatform
	openstackPlatform := hyperv1.OpenStackPlatform
	powervsPlatform := hyperv1.PowerVSPlatform

	return []WorkloadSpec{
		{
			Type: "CronJob",
			Name: "olm-collect-profiles",
			PodSelector: map[string]string{
				"app": "olm-collect-profiles",
				"hypershift.openshift.io/control-plane-component":    "olm-collect-profiles",
				"hypershift.openshift.io/need-management-kas-access": "true",
			},
		},
		{
			Type:     "Deployment",
			Name:     "aws-cloud-controller-manager",
			Platform: &awsPlatform,
			PodSelector: map[string]string{
				"app": "cloud-controller-manager",
			},
		},
		{
			Type:     "Deployment",
			Name:     "aws-ebs-csi-driver-controller",
			Platform: &awsPlatform,
			PodSelector: map[string]string{
				"app": "aws-ebs-csi-driver-controller",
			},
		},
		{
			Type:     "Deployment",
			Name:     "aws-ebs-csi-driver-operator",
			Platform: &awsPlatform,
			PodSelector: map[string]string{
				"name": "aws-ebs-csi-driver-operator",
			},
		},
		{
			Type: "Deployment",
			Name: "capi-provider",
			PodSelector: map[string]string{
				"app":           "capi-provider-controller-manager",
				"control-plane": "capi-provider-controller-manager",
			},
		},
		{
			Type: "Deployment",
			Name: "catalog-operator",
			PodSelector: map[string]string{
				"app": "catalog-operator",
			},
		},
		{
			Type: "Deployment",
			Name: "certified-operators-catalog",
			PodSelector: map[string]string{
				"olm.catalogSource": "certified-operators",
			},
		},
		{
			Type:     "Deployment",
			Name:     "cloud-credential-operator",
			Platform: &awsPlatform,
			PodSelector: map[string]string{
				"app": "cloud-credential-operator",
			},
		},
		{
			Type: "Deployment",
			Name: "cloud-network-config-controller",
			PodSelector: map[string]string{
				"app": "cloud-network-config-controller",
			},
		},
		{
			Type: "Deployment",
			Name: "cluster-api",
			PodSelector: map[string]string{
				"app":  "cluster-api",
				"name": "cluster-api",
			},
		},
		{
			Type: "Deployment",
			Name: "cluster-autoscaler",
			PodSelector: map[string]string{
				"app": "cluster-autoscaler",
			},
		},
		{
			Type: "Deployment",
			Name: "cluster-image-registry-operator",
			PodSelector: map[string]string{
				"name": "cluster-image-registry-operator",
			},
		},
		{
			Type: "Deployment",
			Name: "cluster-network-operator",
			PodSelector: map[string]string{
				"name": "cluster-network-operator",
			},
		},
		{
			Type: "Deployment",
			Name: "cluster-node-tuning-operator",
			PodSelector: map[string]string{
				"name": "cluster-node-tuning-operator",
			},
		},
		{
			Type: "Deployment",
			Name: "cluster-policy-controller",
			PodSelector: map[string]string{
				"app": "cluster-policy-controller",
			},
		},
		{
			Type: "Deployment",
			Name: "cluster-storage-operator",
			PodSelector: map[string]string{
				"name": "cluster-storage-operator",
			},
		},
		{
			Type: "Deployment",
			Name: "cluster-version-operator",
			PodSelector: map[string]string{
				"app":     "cluster-version-operator",
				"k8s-app": "cluster-version-operator",
			},
		},
		{
			Type: "Deployment",
			Name: "community-operators-catalog",
			PodSelector: map[string]string{
				"olm.catalogSource": "community-operators",
			},
		},
		{
			Type: "Deployment",
			Name: "control-plane-operator",
			PodSelector: map[string]string{
				"name": "control-plane-operator",
			},
		},
		{
			Type: "Deployment",
			Name: "control-plane-pki-operator",
			PodSelector: map[string]string{
				"app": "control-plane-pki-operator",
			},
		},
		{
			Type: "Deployment",
			Name: "csi-snapshot-controller",
			PodSelector: map[string]string{
				"app": "csi-snapshot-controller",
			},
		},
		{
			Type: "Deployment",
			Name: "csi-snapshot-controller-operator",
			PodSelector: map[string]string{
				"app": "csi-snapshot-controller-operator",
			},
		},
		{
			Type: "Deployment",
			Name: "dns-operator",
			PodSelector: map[string]string{
				"name": "dns-operator",
			},
		},
		{
			Type: "Deployment",
			Name: "hosted-cluster-config-operator",
			PodSelector: map[string]string{
				"app": "hosted-cluster-config-operator",
			},
		},
		{
			Type: "Deployment",
			Name: "ignition-server",
			PodSelector: map[string]string{
				"app": "ignition-server",
			},
		},
		{
			Type: "Deployment",
			Name: "ignition-server-proxy",
			PodSelector: map[string]string{
				"app": "ignition-server-proxy",
			},
		},
		{
			Type: "Deployment",
			Name: "ingress-operator",
			PodSelector: map[string]string{
				"name": "ingress-operator",
			},
		},
		{
			Type: "Deployment",
			Name: "konnectivity-agent",
			PodSelector: map[string]string{
				"app": "konnectivity-agent",
			},
		},
		{
			Type: "Deployment",
			Name: "kube-apiserver",
			PodSelector: map[string]string{
				"app": "kube-apiserver",
			},
		},
		{
			Type: "Deployment",
			Name: "kube-controller-manager",
			PodSelector: map[string]string{
				"app": "kube-controller-manager",
			},
		},
		{
			Type: "Deployment",
			Name: "kube-scheduler",
			PodSelector: map[string]string{
				"app": "kube-scheduler",
			},
		},
		{
			Type: "Deployment",
			Name: "machine-approver",
			PodSelector: map[string]string{
				"app": "machine-approver",
			},
		},
		{
			Type: "Deployment",
			Name: "multus-admission-controller",
			PodSelector: map[string]string{
				"app": "multus-admission-controller",
			},
		},
		{
			Type: "Deployment",
			Name: "network-node-identity",
			PodSelector: map[string]string{
				"app": "network-node-identity",
			},
		},
		{
			Type: "Deployment",
			Name: "oauth-openshift",
			PodSelector: map[string]string{
				"app": "oauth-openshift",
			},
		},
		{
			Type: "Deployment",
			Name: "olm-operator",
			PodSelector: map[string]string{
				"app": "olm-operator",
			},
		},
		{
			Type: "Deployment",
			Name: "openshift-apiserver",
			PodSelector: map[string]string{
				"app": "openshift-apiserver",
			},
		},
		{
			Type: "Deployment",
			Name: "openshift-controller-manager",
			PodSelector: map[string]string{
				"app": "openshift-controller-manager",
			},
		},
		{
			Type: "Deployment",
			Name: "openshift-oauth-apiserver",
			PodSelector: map[string]string{
				"app": "openshift-oauth-apiserver",
			},
		},
		{
			Type: "Deployment",
			Name: "openshift-route-controller-manager",
			PodSelector: map[string]string{
				"app": "openshift-route-controller-manager",
			},
		},
		{
			Type: "Deployment",
			Name: "ovnkube-control-plane",
			PodSelector: map[string]string{
				"app": "ovnkube-control-plane",
			},
		},
		{
			Type: "Deployment",
			Name: "packageserver",
			PodSelector: map[string]string{
				"app": "packageserver",
			},
		},
		{
			Type: "Deployment",
			Name: "redhat-marketplace-catalog",
			PodSelector: map[string]string{
				"olm.catalogSource": "redhat-marketplace",
			},
		},
		{
			Type: "Deployment",
			Name: "redhat-operators-catalog",
			PodSelector: map[string]string{
				"olm.catalogSource": "redhat-operators",
			},
		},
		{
			Type:     "Deployment",
			Name:     "router",
			Platform: &awsPlatform,
			PodSelector: map[string]string{
				"app": "private-router",
			},
		},
		{
			Type:     "Deployment",
			Name:     "karpenter",
			Platform: &awsPlatform,
			PodSelector: map[string]string{
				"app": "karpenter",
			},
		},
		{
			Type:     "Deployment",
			Name:     "karpenter-operator",
			Platform: &awsPlatform,
			PodSelector: map[string]string{
				"app": "karpenter-operator",
			},
		},
		{
			Type:     "Deployment",
			Name:     "aws-node-termination-handler",
			Platform: &awsPlatform,
			PodSelector: map[string]string{
				"app": "aws-node-termination-handler",
			},
		},
		{
			Type:     "Deployment",
			Name:     "kubevirt-cloud-controller-manager",
			Platform: &kubevirtPlatform,
			PodSelector: map[string]string{
				"app": "cloud-controller-manager",
			},
		},
		{
			Type:     "Deployment",
			Name:     "kubevirt-csi-controller",
			Platform: &kubevirtPlatform,
			PodSelector: map[string]string{
				"app": "kubevirt-csi-driver",
			},
		},
		{
			Type:     "Deployment",
			Name:     "openstack-cloud-controller-manager",
			Platform: &openstackPlatform,
			PodSelector: map[string]string{
				"k8s-app": "openstack-cloud-controller-manager",
			},
		},
		{
			Type:     "Deployment",
			Name:     "powervs-cloud-controller-manager",
			Platform: &powervsPlatform,
			PodSelector: map[string]string{
				"k8s-app": "cloud-controller-manager",
			},
		},
		{
			Type: "CronJob",
			Name: "etcd-backup",
			PodSelector: map[string]string{
				"app": "etcd-backup",
			},
		},
		{
			Type: "Job",
			Name: "featuregate-generator",
			PodSelector: map[string]string{
				"app":                          "featuregate-generator",
				"batch.kubernetes.io/job-name": "featuregate-generator",
				"hypershift.openshift.io/control-plane-component":    "featuregate-generator",
				"hypershift.openshift.io/need-management-kas-access": "true",
				"job-name": "featuregate-generator",
			},
		},
		{
			Type: "StatefulSet",
			Name: "etcd",
			PodSelector: map[string]string{
				"app": "etcd",
			},
		},
		{
			Type:     "Deployment",
			Name:     "azure-cloud-controller-manager",
			Platform: &azurePlatform,
			PodSelector: map[string]string{
				"app": "cloud-controller-manager",
			},
		},
		{
			Type:     "Deployment",
			Name:     "azure-disk-csi-driver-controller",
			Platform: &azurePlatform,
			PodSelector: map[string]string{
				"app": "azure-disk-csi-driver-controller",
			},
		},
		{
			Type:     "Deployment",
			Name:     "azure-disk-csi-driver-operator",
			Platform: &azurePlatform,
			PodSelector: map[string]string{
				"name": "azure-disk-csi-driver-operator",
			},
		},
		{
			Type:     "Deployment",
			Name:     "azure-file-csi-driver-controller",
			Platform: &azurePlatform,
			PodSelector: map[string]string{
				"app": "azure-file-csi-driver-controller",
			},
		},
		{
			Type:     "Deployment",
			Name:     "azure-file-csi-driver-operator",
			Platform: &azurePlatform,
			PodSelector: map[string]string{
				"name": "azure-file-csi-driver-operator",
			},
		},
	}
}
