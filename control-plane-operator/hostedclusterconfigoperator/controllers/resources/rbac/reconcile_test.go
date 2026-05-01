package rbac

import (
	"testing"

	. "github.com/onsi/gomega"

	rbacv1 "k8s.io/api/rbac/v1"
)

func TestReconcileNodeCredentialProviderTokenAudienceClusterRole(t *testing.T) {
	g := NewGomegaWithT(t)

	t.Run("When reconciling it should set correct rules", func(t *testing.T) {
		r := &rbacv1.ClusterRole{}
		err := ReconcileNodeCredentialProviderTokenAudienceClusterRole(r)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(r.Rules).To(HaveLen(2))

		g.Expect(r.Rules[0].APIGroups).To(Equal([]string{""}))
		g.Expect(r.Rules[0].Resources).To(Equal([]string{"serviceaccounts/token"}))
		g.Expect(r.Rules[0].Verbs).To(Equal([]string{"create"}))

		g.Expect(r.Rules[1].APIGroups).To(Equal([]string{""}))
		g.Expect(r.Rules[1].Resources).To(Equal([]string{"api://AzureADTokenExchange"}))
		g.Expect(r.Rules[1].Verbs).To(Equal([]string{"request-serviceaccounts-token-audience"}))
	})

	t.Run("When reconciling it should not use a wildcard audience", func(t *testing.T) {
		r := &rbacv1.ClusterRole{}
		err := ReconcileNodeCredentialProviderTokenAudienceClusterRole(r)
		g.Expect(err).ToNot(HaveOccurred())

		for _, rule := range r.Rules {
			for _, resource := range rule.Resources {
				g.Expect(resource).ToNot(Equal("*"), "audience resource must be scoped, not wildcard")
			}
		}
	})
}

func TestReconcileNodeCredentialProviderTokenAudienceClusterRoleBinding(t *testing.T) {
	g := NewGomegaWithT(t)

	t.Run("When reconciling it should bind to system:nodes group", func(t *testing.T) {
		r := &rbacv1.ClusterRoleBinding{}
		err := ReconcileNodeCredentialProviderTokenAudienceClusterRoleBinding(r)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(r.RoleRef.Kind).To(Equal("ClusterRole"))
		g.Expect(r.RoleRef.Name).To(Equal("system:node:credential-provider-token-audience"))
		g.Expect(r.RoleRef.APIGroup).To(Equal("rbac.authorization.k8s.io"))

		g.Expect(r.Subjects).To(HaveLen(1))
		g.Expect(r.Subjects[0].Kind).To(Equal("Group"))
		g.Expect(r.Subjects[0].Name).To(Equal("system:nodes"))
	})
}
