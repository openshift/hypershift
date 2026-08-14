package cvo

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestResourcesToRemove(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		platform hyperv1.PlatformType
		validate func(g Gomega, resources []client.Object)
	}{
		{
			name:     "When platform is IBMCloud, it should return 4 resources",
			platform: hyperv1.IBMCloudPlatform,
			validate: func(g Gomega, resources []client.Object) {
				g.Expect(resources).To(HaveLen(4))
			},
		},
		{
			name:     "When platform is IBMCloud, it should include network-operator deployment",
			platform: hyperv1.IBMCloudPlatform,
			validate: func(g Gomega, resources []client.Object) {
				found := false
				for _, r := range resources {
					if dep, ok := r.(*appsv1.Deployment); ok && dep.Name == "network-operator" {
						found = true
						g.Expect(dep.Namespace).To(Equal("openshift-network-operator"))
					}
				}
				g.Expect(found).To(BeTrue(), "expected network-operator deployment")
			},
		},
		{
			name:     "When platform is IBMCloud, it should include cluster-node-tuning-operator deployment",
			platform: hyperv1.IBMCloudPlatform,
			validate: func(g Gomega, resources []client.Object) {
				found := false
				for _, r := range resources {
					if dep, ok := r.(*appsv1.Deployment); ok && dep.Name == "cluster-node-tuning-operator" {
						found = true
						g.Expect(dep.Namespace).To(Equal("openshift-cluster-node-tuning-operator"))
					}
				}
				g.Expect(found).To(BeTrue(), "expected cluster-node-tuning-operator deployment")
			},
		},
		{
			name:     "When platform is IBMCloud, it should include cluster-image-registry-operator deployment",
			platform: hyperv1.IBMCloudPlatform,
			validate: func(g Gomega, resources []client.Object) {
				found := false
				for _, r := range resources {
					if dep, ok := r.(*appsv1.Deployment); ok && dep.Name == "cluster-image-registry-operator" {
						found = true
						g.Expect(dep.Namespace).To(Equal("openshift-image-registry"))
					}
				}
				g.Expect(found).To(BeTrue(), "expected cluster-image-registry-operator deployment")
			},
		},
		{
			name:     "When platform is IBMCloud, it should include default-account-cluster-network-operator ClusterRoleBinding",
			platform: hyperv1.IBMCloudPlatform,
			validate: func(g Gomega, resources []client.Object) {
				found := false
				for _, r := range resources {
					if crb, ok := r.(*rbacv1.ClusterRoleBinding); ok && crb.Name == "default-account-cluster-network-operator" {
						found = true
					}
				}
				g.Expect(found).To(BeTrue(), "expected default-account-cluster-network-operator ClusterRoleBinding")
			},
		},
		{
			name:     "When platform is IBMCloud, it should not include any CRDs",
			platform: hyperv1.IBMCloudPlatform,
			validate: func(g Gomega, resources []client.Object) {
				for _, r := range resources {
					_, isCRD := r.(*apiextensionsv1.CustomResourceDefinition)
					g.Expect(isCRD).To(BeFalse(), "expected no CRDs for IBMCloud platform")
				}
			},
		},
		{
			name:     "When platform is PowerVS, it should return the same resources as IBMCloud",
			platform: hyperv1.PowerVSPlatform,
			validate: func(g Gomega, resources []client.Object) {
				ibmResources := ResourcesToRemove(hyperv1.IBMCloudPlatform)
				g.Expect(resources).To(HaveLen(len(ibmResources)))
				for i, r := range resources {
					g.Expect(r.GetName()).To(Equal(ibmResources[i].GetName()))
					g.Expect(r.GetNamespace()).To(Equal(ibmResources[i].GetNamespace()))
				}
			},
		},
		{
			name:     "When platform is default (e.g., AWS), it should return 11 resources",
			platform: hyperv1.AWSPlatform,
			validate: func(g Gomega, resources []client.Object) {
				g.Expect(resources).To(HaveLen(11))
			},
		},
		{
			name:     "When platform is default, it should include machineconfigs CRD",
			platform: hyperv1.AWSPlatform,
			validate: func(g Gomega, resources []client.Object) {
				found := false
				for _, r := range resources {
					if crd, ok := r.(*apiextensionsv1.CustomResourceDefinition); ok && crd.Name == "machineconfigs.machineconfiguration.openshift.io" {
						found = true
					}
				}
				g.Expect(found).To(BeTrue(), "expected machineconfigs CRD")
			},
		},
		{
			name:     "When platform is default, it should include machineconfigpools CRD",
			platform: hyperv1.AWSPlatform,
			validate: func(g Gomega, resources []client.Object) {
				found := false
				for _, r := range resources {
					if crd, ok := r.(*apiextensionsv1.CustomResourceDefinition); ok && crd.Name == "machineconfigpools.machineconfiguration.openshift.io" {
						found = true
					}
				}
				g.Expect(found).To(BeTrue(), "expected machineconfigpools CRD")
			},
		},
		{
			name:     "When platform is default, it should include storage-related deployments",
			platform: hyperv1.AWSPlatform,
			validate: func(g Gomega, resources []client.Object) {
				storageDeployments := map[string]string{
					"cluster-storage-operator":         "openshift-cluster-storage-operator",
					"csi-snapshot-controller-operator": "openshift-cluster-storage-operator",
					"aws-ebs-csi-driver-operator":      "openshift-cluster-csi-drivers",
					"aws-ebs-csi-driver-controller":    "openshift-cluster-csi-drivers",
					"csi-snapshot-controller":          "openshift-cluster-storage-operator",
				}
				for expectedName, expectedNS := range storageDeployments {
					found := false
					for _, r := range resources {
						if dep, ok := r.(*appsv1.Deployment); ok && dep.Name == expectedName {
							found = true
							g.Expect(dep.Namespace).To(Equal(expectedNS), "unexpected namespace for deployment %s", expectedName)
						}
					}
					g.Expect(found).To(BeTrue(), "expected storage-related deployment %s", expectedName)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			resources := ResourcesToRemove(tc.platform)
			tc.validate(g, resources)
		})
	}
}
