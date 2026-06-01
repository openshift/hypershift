package capiprovider

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/k8sutil"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptRole(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                 string
		existingRules        []rbacv1.PolicyRule
		platformPolicyRules  []rbacv1.PolicyRule
		hcpAnnotations       map[string]string
		expectedTotalRules   int
		expectedAnnotKey     string
		expectedAnnotValue   string
		shouldAppendPlatform bool
	}{
		{
			name: "When platform policy rules are provided, it should append them to role",
			existingRules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{""},
					Resources: []string{"configmaps"},
					Verbs:     []string{"get", "list"},
				},
			},
			platformPolicyRules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{"cluster.x-k8s.io"},
					Resources: []string{"awsmachines"},
					Verbs:     []string{"get", "list", "watch"},
				},
				{
					APIGroups: []string{"infrastructure.cluster.x-k8s.io"},
					Resources: []string{"awsclusters"},
					Verbs:     []string{"get", "list", "watch", "update"},
				},
			},
			expectedTotalRules:   3,
			shouldAppendPlatform: true,
		},
		{
			name: "When platform policy rules are nil, it should not append any rules",
			existingRules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{""},
					Resources: []string{"configmaps"},
					Verbs:     []string{"get", "list"},
				},
			},
			platformPolicyRules:  nil,
			expectedTotalRules:   1,
			shouldAppendPlatform: false,
		},
		{
			name: "When platform policy rules are empty, it should not append any rules",
			existingRules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{""},
					Resources: []string{"configmaps"},
					Verbs:     []string{"get", "list"},
				},
			},
			platformPolicyRules:  []rbacv1.PolicyRule{},
			expectedTotalRules:   1,
			shouldAppendPlatform: false,
		},
		{
			name: "When HCP has hosted cluster annotation, it should set role annotation",
			existingRules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{""},
					Resources: []string{"configmaps"},
					Verbs:     []string{"get", "list"},
				},
			},
			platformPolicyRules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{"cluster.x-k8s.io"},
					Resources: []string{"awsmachines"},
					Verbs:     []string{"get", "list", "watch"},
				},
			},
			hcpAnnotations: map[string]string{
				k8sutil.HostedClusterAnnotation: "test-namespace/test-cluster",
			},
			expectedTotalRules:   2,
			expectedAnnotKey:     k8sutil.HostedClusterAnnotation,
			expectedAnnotValue:   "test-namespace/test-cluster",
			shouldAppendPlatform: true,
		},
		{
			name:          "When role has no existing rules, it should append platform rules",
			existingRules: []rbacv1.PolicyRule{},
			platformPolicyRules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{"cluster.x-k8s.io"},
					Resources: []string{"azuremachines"},
					Verbs:     []string{"get", "list", "watch"},
				},
			},
			expectedTotalRules:   1,
			shouldAppendPlatform: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			role := &rbacv1.Role{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "capi-provider",
					Namespace: "test-namespace",
				},
				Rules: tc.existingRules,
			}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-hcp",
					Namespace:   "test-namespace",
					Annotations: tc.hcpAnnotations,
				},
			}

			capi := &CAPIProviderOptions{
				platformPolicyRules: tc.platformPolicyRules,
			}

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			err := capi.adaptRole(cpContext, role)
			g.Expect(err).ToNot(HaveOccurred())

			// Check total number of rules
			g.Expect(role.Rules).To(HaveLen(tc.expectedTotalRules))

			// Verify platform rules were appended if expected
			if tc.shouldAppendPlatform && len(tc.platformPolicyRules) > 0 {
				// The last N rules should match the platform rules
				startIdx := len(tc.existingRules)
				for i, platformRule := range tc.platformPolicyRules {
					g.Expect(role.Rules[startIdx+i]).To(Equal(platformRule))
				}
			}

			// Check annotations
			if tc.expectedAnnotKey != "" {
				g.Expect(role.Annotations).To(HaveKey(tc.expectedAnnotKey))
				g.Expect(role.Annotations[tc.expectedAnnotKey]).To(Equal(tc.expectedAnnotValue))
			}
		})
	}
}

func TestAdaptRole_WithNilAnnotations(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "capi-provider",
			Namespace: "test-namespace",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"configmaps"},
				Verbs:     []string{"get", "list"},
			},
		},
	}

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
			Annotations: map[string]string{
				k8sutil.HostedClusterAnnotation: "test-namespace/test-cluster",
			},
		},
	}

	capi := &CAPIProviderOptions{
		platformPolicyRules: nil,
	}

	cpContext := component.WorkloadContext{
		Context: t.Context(),
		HCP:     hcp,
	}

	err := capi.adaptRole(cpContext, role)
	g.Expect(err).ToNot(HaveOccurred())

	// Should create annotations map and set the annotation
	g.Expect(role.Annotations).ToNot(BeNil())
	g.Expect(role.Annotations[k8sutil.HostedClusterAnnotation]).To(Equal("test-namespace/test-cluster"))
}
