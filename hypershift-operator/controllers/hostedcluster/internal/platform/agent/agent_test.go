package agent

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/upsert"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileCredentials(t *testing.T) {
	g := NewGomegaWithT(t)
	platform := &Agent{}
	hostedCluster := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AgentPlatform,
				Agent: &hyperv1.AgentPlatformSpec{
					AgentNamespace: "test",
				},
			},
		},
	}
	controlPlaneNamespace := "test"
	client := fake.NewClientBuilder().Build()

	err := platform.ReconcileCredentials(context.Background(),
		client, upsert.New(false).CreateOrUpdate,
		hostedCluster, controlPlaneNamespace)
	g.Expect(err).ToNot(HaveOccurred())

	role := &rbacv1.Role{}
	err = client.Get(context.Background(), types.NamespacedName{
		Namespace: hostedCluster.Spec.Platform.Agent.AgentNamespace,
		Name:      credentialsRBACName,
	}, role)
	g.Expect(err).ToNot(HaveOccurred())

	roleBinding := &rbacv1.RoleBinding{}
	err = client.Get(context.Background(), types.NamespacedName{
		Namespace: hostedCluster.Spec.Platform.Agent.AgentNamespace,
		Name:      fmt.Sprintf("%s-%s", credentialsRBACName, controlPlaneNamespace),
	}, roleBinding)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(roleBinding.Subjects[0].Namespace).To(BeIdenticalTo(controlPlaneNamespace))
	g.Expect(roleBinding.Subjects[0].Kind).To(BeIdenticalTo("ServiceAccount"))
	g.Expect(roleBinding.Subjects[0].Name).To(BeIdenticalTo("capi-provider"))
}
