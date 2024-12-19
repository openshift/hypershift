package cno

import (
	"strconv"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestReconcileDeployment(t *testing.T) {
	tcs := []struct {
		name                        string
		params                      Params
		expectProxyAPIServerAddress bool
		cnoResources                *corev1.ResourceRequirements
	}{
		{
			name:                        "No private apiserver connectivity, proxy apiserver address is set",
			expectProxyAPIServerAddress: true,
		},
		{
			name:   "Private apiserver connectivity, proxy apiserver address is unset",
			params: Params{IsPrivate: true},
		},
		{
			name:                        "Preserve existing resources",
			expectProxyAPIServerAddress: true,
			cnoResources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("500Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1000m"),
					corev1.ResourceMemory: resource.MustParse("1000Mi"),
				},
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			if tc.params.ReleaseVersion == "" {
				tc.params.ReleaseVersion = "4.11.0"
			}

			dep := &appsv1.Deployment{}

			if tc.cnoResources != nil {
				dep.Spec.Template.Spec.Containers = []corev1.Container{
					{
						Name:      operatorName,
						Resources: *tc.cnoResources,
					},
				}
			}

			if err := ReconcileDeployment(dep, tc.params, hyperv1.NonePlatform); err != nil {
				t.Fatalf("ReconcileDeployment: %v", err)
			}

			var hasProxyAPIServerAddress bool
			for _, envVar := range dep.Spec.Template.Spec.Containers[0].Env {
				if envVar.Name == "PROXY_INTERNAL_APISERVER_ADDRESS" {
					hasProxyAPIServerAddress = envVar.Value == "true"
					break
				}
			}

			if hasProxyAPIServerAddress != tc.expectProxyAPIServerAddress {
				t.Errorf("expected 'PROXY_INTERNAL_APISERVER_ADDRESS' env var to be %s, was %s",
					strconv.FormatBool(tc.expectProxyAPIServerAddress),
					strconv.FormatBool(hasProxyAPIServerAddress))
			}

			deploymentYaml, err := util.SerializeResource(dep, hyperapi.Scheme)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			testutil.CompareWithFixture(t, deploymentYaml)

		})
	}
}

func TestReconcileRole(t *testing.T) {
	type args struct {
		role        *rbacv1.Role
		ownerRef    config.OwnerRef
		networkType hyperv1.NetworkType
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "Network type OVNKubernetes",
			args: args{
				role:        &rbacv1.Role{},
				ownerRef:    config.OwnerRef{},
				networkType: hyperv1.OVNKubernetes,
			},
		},
		{
			name: "Network type OpenShiftSDN",
			args: args{
				role:        &rbacv1.Role{},
				ownerRef:    config.OwnerRef{},
				networkType: hyperv1.OpenShiftSDN,
			},
		},
		{
			name: "Network type Calico",
			args: args{
				role:        &rbacv1.Role{},
				ownerRef:    config.OwnerRef{},
				networkType: hyperv1.Calico,
			},
		},
		{
			name: "Network type Other",
			args: args{
				role:        &rbacv1.Role{},
				ownerRef:    config.OwnerRef{},
				networkType: hyperv1.Other,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			g := NewGomegaWithT(t)
			err := ReconcileRole(tt.args.role, tt.args.ownerRef, tt.args.networkType)
			g.Expect(err).To(BeNil())
			g.Expect(tt.args.role.Rules).To(BeEquivalentTo(expectedRules(tt.args.networkType)))
		})
	}
}

func expectedRules(networkType hyperv1.NetworkType) []rbacv1.PolicyRule {

	ovnRules := []rbacv1.PolicyRule{
		{
			APIGroups: []string{corev1.SchemeGroupVersion.Group},
			Resources: []string{
				"events",
				"configmaps",
				"pods",
				"secrets",
				"services",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{"policy"},
			Resources: []string{"poddisruptionbudgets"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{appsv1.SchemeGroupVersion.Group},
			Resources: []string{"statefulsets", "deployments"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{routev1.SchemeGroupVersion.Group},
			Resources: []string{"routes", "routes/custom-host"},
			Verbs:     []string{"*"},
		},
		{
			APIGroups: []string{"monitoring.coreos.com", "monitoring.rhobs"},
			Resources: []string{
				"servicemonitors",
				"prometheusrules",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{hyperv1.GroupVersion.Group},
			Resources: []string{
				"hostedcontrolplanes",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{hyperv1.GroupVersion.Group},
			Resources: []string{
				"hostedcontrolplanes/status",
			},
			Verbs: []string{"*"},
		},
	}

	otherNetworkRules := []rbacv1.PolicyRule{
		{
			APIGroups: []string{corev1.SchemeGroupVersion.Group},
			Resources: []string{
				"configmaps",
			},
			ResourceNames: []string{
				"openshift-service-ca.crt",
				caConfigMap,
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{corev1.SchemeGroupVersion.Group},
			Resources: []string{
				"configmaps",
			},
			ResourceNames: []string{
				"ovnkube-identity-cm",
			},
			Verbs: []string{
				"list",
				"get",
				"watch",
				"create",
				"patch",
				"update",
			},
		},
		{
			APIGroups: []string{appsv1.SchemeGroupVersion.Group},
			Resources: []string{"statefulsets", "deployments"},
			Verbs:     []string{"list", "watch"},
		},
		{
			APIGroups: []string{appsv1.SchemeGroupVersion.Group},
			Resources: []string{"deployments"},
			ResourceNames: []string{
				"multus-admission-controller",
				"network-node-identity",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{corev1.SchemeGroupVersion.Group},
			Resources: []string{"services"},
			ResourceNames: []string{
				"multus-admission-controller",
				"network-node-identity",
			},
			Verbs: []string{"*"},
		},
		{
			APIGroups: []string{hyperv1.GroupVersion.Group},
			Resources: []string{
				"hostedcontrolplanes",
			},
			Verbs: []string{
				"get",
				"list",
				"watch",
			},
		},
		{
			APIGroups: []string{hyperv1.GroupVersion.Group},
			Resources: []string{
				"hostedcontrolplanes/status",
			},
			Verbs: []string{"*"},
		},
	}

	if networkType != hyperv1.OVNKubernetes {
		return otherNetworkRules
	}

	return ovnRules
}
