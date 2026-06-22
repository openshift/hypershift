package oauth

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"

	rbacv1 "k8s.io/api/rbac/v1"
)

func TestReconcileOauthServingCertRole(t *testing.T) {
	testsCases := []struct {
		name         string
		inputRole    *rbacv1.Role
		expectedRole *rbacv1.Role
	}{
		{
			name:      "when empty role specified the rules are populated",
			inputRole: manifests.OAuthServingCertRole(),
			expectedRole: &rbacv1.Role{
				ObjectMeta: manifests.OAuthServingCertRole().ObjectMeta,
				Rules: []rbacv1.PolicyRule{
					{
						APIGroups:     []string{""},
						ResourceNames: []string{"oauth-serving-cert"},
						Resources:     []string{"configmaps"},
						Verbs:         []string{"get", "list", "watch"},
					},
				},
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := ReconcileOauthServingCertRole(tc.inputRole)
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(tc.inputRole).To(BeEquivalentTo(tc.expectedRole))
		})
	}
}

func TestReconcileOauthServingCertRoleBinding(t *testing.T) {
	testsCases := []struct {
		name                string
		inputRoleBinding    *rbacv1.RoleBinding
		expectedRoleBinding *rbacv1.RoleBinding
	}{
		{
			name:             "when empty role binding specified the roleref and subjects are populated",
			inputRoleBinding: manifests.OAuthServingCertRoleBinding(),
			expectedRoleBinding: &rbacv1.RoleBinding{
				ObjectMeta: manifests.OAuthServingCertRoleBinding().ObjectMeta,
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "Role",
					Name:     manifests.OAuthServingCertRole().Name,
				},
				Subjects: []rbacv1.Subject{
					{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Group",
						Name:     "system:authenticated",
					},
				},
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := ReconcileOauthServingCertRoleBinding(tc.inputRoleBinding)
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(tc.inputRoleBinding).To(BeEquivalentTo(tc.expectedRoleBinding))
		})
	}
}
