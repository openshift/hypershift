package agent

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/upsert"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/google/go-cmp/cmp"
)

func TestReconcileCAPIProviderRole(t *testing.T) {
	g := NewGomegaWithT(t)
	hostedCluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "clusters",
			Name:      "test-cluster",
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AgentPlatform,
				Agent: &hyperv1.AgentPlatformSpec{
					AgentNamespace: "test",
				},
			},
		},
	}
	controlPlaneNamespace := "test-cp"
	client := fake.NewClientBuilder().Build()

	err := ReconcileCAPIProviderRole(t.Context(),
		client, upsert.New(false).CreateOrUpdate,
		hostedCluster, controlPlaneNamespace)
	g.Expect(err).ToNot(HaveOccurred())

	roleBinding := &rbacv1.RoleBinding{}
	err = client.Get(t.Context(), types.NamespacedName{
		Namespace: hostedCluster.Spec.Platform.Agent.AgentNamespace,
		Name:      fmt.Sprintf("%s-%s", CredentialsRBACPrefix, controlPlaneNamespace),
	}, roleBinding)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(roleBinding.Subjects[0].Namespace).To(BeIdenticalTo(controlPlaneNamespace))
	g.Expect(roleBinding.Subjects[0].Kind).To(BeIdenticalTo("ServiceAccount"))
	g.Expect(roleBinding.Subjects[0].Name).To(BeIdenticalTo("capi-provider"))
	g.Expect(roleBinding.Labels[capiProviderAgentRBACLabelKey]).To(Equal(capiProviderAgentRBACLabelValue))
	g.Expect(roleBinding.Annotations[k8sutil.HostedClusterAnnotation]).To(Equal("clusters/test-cluster"))
	g.Expect(roleBinding.RoleRef.APIGroup).To(Equal(rbacv1.GroupName))

	roleName := fmt.Sprintf("%s-%s", CAPIProviderRoleName, controlPlaneNamespace)
	g.Expect(roleBinding.RoleRef.Name).To(Equal(roleName))

	role := &rbacv1.Role{}
	err = client.Get(t.Context(), types.NamespacedName{
		Namespace: hostedCluster.Spec.Platform.Agent.AgentNamespace,
		Name:      roleName,
	}, role)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(role.Labels[capiProviderAgentRBACLabelKey]).To(Equal(capiProviderAgentRBACLabelValue))
	g.Expect(role.Annotations[k8sutil.HostedClusterAnnotation]).To(Equal("clusters/test-cluster"))
	g.Expect(role.Rules).To(HaveLen(1))
	g.Expect(role.Rules[0].APIGroups).To(Equal([]string{"agent-install.openshift.io"}))
	g.Expect(role.Rules[0].Resources).To(Equal([]string{"agents"}))
	g.Expect(role.Rules[0].Verbs).To(Equal([]string{"get", "list", "watch", "update", "patch"}))
}

func TestReconcileCAPIProviderRole_WhenCalledMultipleTimes_ItShouldBeIdempotent(t *testing.T) {
	g := NewGomegaWithT(t)
	hostedCluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "clusters",
			Name:      "test-cluster",
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AgentPlatform,
				Agent: &hyperv1.AgentPlatformSpec{
					AgentNamespace: "test",
				},
			},
		},
	}
	controlPlaneNamespace := "test-cp"
	client := fake.NewClientBuilder().Build()

	err := ReconcileCAPIProviderRole(t.Context(),
		client, upsert.New(false).CreateOrUpdate,
		hostedCluster, controlPlaneNamespace)
	g.Expect(err).ToNot(HaveOccurred())

	err = ReconcileCAPIProviderRole(t.Context(),
		client, upsert.New(false).CreateOrUpdate,
		hostedCluster, controlPlaneNamespace)
	g.Expect(err).ToNot(HaveOccurred())

	roleName := fmt.Sprintf("%s-%s", CAPIProviderRoleName, controlPlaneNamespace)
	role := &rbacv1.Role{}
	err = client.Get(t.Context(), types.NamespacedName{
		Namespace: hostedCluster.Spec.Platform.Agent.AgentNamespace,
		Name:      roleName,
	}, role)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(role.Rules).To(HaveLen(1))
	g.Expect(role.Rules[0].APIGroups).To(Equal([]string{"agent-install.openshift.io"}))
	g.Expect(role.Rules[0].Resources).To(Equal([]string{"agents"}))
	g.Expect(role.Rules[0].Verbs).To(Equal([]string{"get", "list", "watch", "update", "patch"}))
	g.Expect(role.Labels[capiProviderAgentRBACLabelKey]).To(Equal(capiProviderAgentRBACLabelValue))
	g.Expect(role.Annotations[k8sutil.HostedClusterAnnotation]).To(Equal("clusters/test-cluster"))
}

// TestDeleteCredentialsWithMultipleClusters verifies that each HostedCluster's RBAC resources
// (Role and RoleBinding) are independently deleted without affecting other clusters sharing the same agent namespace.
func TestDeleteCredentialsWithMultipleClusters(t *testing.T) {
	g := NewGomegaWithT(t)
	platform := &Agent{}
	agentNamespace := "shared-agent-ns"
	hostedCluster1 := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "clusters",
			Name:      "cluster1",
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type:  hyperv1.AgentPlatform,
				Agent: &hyperv1.AgentPlatformSpec{AgentNamespace: agentNamespace},
			},
		},
	}
	hostedCluster2 := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "clusters",
			Name:      "cluster2",
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type:  hyperv1.AgentPlatform,
				Agent: &hyperv1.AgentPlatformSpec{AgentNamespace: agentNamespace},
			},
		},
	}
	cpNamespace1 := "cp-one"
	cpNamespace2 := "cp-two"
	c := fake.NewClientBuilder().Build()

	err := ReconcileCAPIProviderRole(t.Context(), c, upsert.New(false).CreateOrUpdate, hostedCluster1, cpNamespace1)
	g.Expect(err).ToNot(HaveOccurred())
	err = ReconcileCAPIProviderRole(t.Context(), c, upsert.New(false).CreateOrUpdate, hostedCluster2, cpNamespace2)
	g.Expect(err).ToNot(HaveOccurred())

	// Delete cluster1's credentials
	err = platform.DeleteCredentials(t.Context(), c, hostedCluster1, cpNamespace1)
	g.Expect(err).ToNot(HaveOccurred())

	// Cluster1's Role and RoleBinding should be deleted
	role1Name := fmt.Sprintf("%s-%s", CAPIProviderRoleName, cpNamespace1)
	role := &rbacv1.Role{}
	err = c.Get(t.Context(), types.NamespacedName{Namespace: agentNamespace, Name: role1Name}, role)
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "cluster1's Role should be deleted")

	rb1Name := fmt.Sprintf("%s-%s", CredentialsRBACPrefix, cpNamespace1)
	rb := &rbacv1.RoleBinding{}
	err = c.Get(t.Context(), types.NamespacedName{Namespace: agentNamespace, Name: rb1Name}, rb)
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "cluster1's RoleBinding should be deleted")

	// Cluster2's Role and RoleBinding should still exist
	role2Name := fmt.Sprintf("%s-%s", CAPIProviderRoleName, cpNamespace2)
	err = c.Get(t.Context(), types.NamespacedName{Namespace: agentNamespace, Name: role2Name}, role)
	g.Expect(err).ToNot(HaveOccurred(), "cluster2's Role should remain untouched")

	rb2Name := fmt.Sprintf("%s-%s", CredentialsRBACPrefix, cpNamespace2)
	err = c.Get(t.Context(), types.NamespacedName{Namespace: agentNamespace, Name: rb2Name}, rb)
	g.Expect(err).ToNot(HaveOccurred(), "cluster2's RoleBinding should remain untouched")

	// Delete cluster2's credentials
	err = platform.DeleteCredentials(t.Context(), c, hostedCluster2, cpNamespace2)
	g.Expect(err).ToNot(HaveOccurred())

	// Now cluster2's resources should be deleted too
	err = c.Get(t.Context(), types.NamespacedName{Namespace: agentNamespace, Name: role2Name}, role)
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "cluster2's Role should be deleted")

	err = c.Get(t.Context(), types.NamespacedName{Namespace: agentNamespace, Name: rb2Name}, rb)
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "cluster2's RoleBinding should be deleted")
}

func TestDeleteCredentials(t *testing.T) {
	g := NewGomegaWithT(t)
	platform := &Agent{}
	hostedCluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "clusters",
			Name:      "test-cluster",
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AgentPlatform,
				Agent: &hyperv1.AgentPlatformSpec{
					AgentNamespace: "test",
				},
			},
		},
	}
	controlPlaneNamespace := "test-cp"
	client := fake.NewClientBuilder().Build()

	// test noop - deleting non-existent resources should not error
	err := platform.DeleteCredentials(t.Context(),
		client,
		hostedCluster, controlPlaneNamespace)
	g.Expect(err).ToNot(HaveOccurred())

	// Create the RBAC resources
	err = ReconcileCAPIProviderRole(t.Context(),
		client, upsert.New(false).CreateOrUpdate,
		hostedCluster, controlPlaneNamespace)
	g.Expect(err).ToNot(HaveOccurred())

	roleName := fmt.Sprintf("%s-%s", CAPIProviderRoleName, controlPlaneNamespace)
	bindingName := fmt.Sprintf("%s-%s", CredentialsRBACPrefix, controlPlaneNamespace)

	// Verify the RoleBinding exists
	roleBinding := &rbacv1.RoleBinding{}
	err = client.Get(t.Context(), types.NamespacedName{
		Namespace: hostedCluster.Spec.Platform.Agent.AgentNamespace,
		Name:      bindingName,
	}, roleBinding)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify the Role exists
	role := &rbacv1.Role{}
	err = client.Get(t.Context(), types.NamespacedName{
		Namespace: hostedCluster.Spec.Platform.Agent.AgentNamespace,
		Name:      roleName,
	}, role)
	g.Expect(err).ToNot(HaveOccurred())

	// Delete credentials
	err = platform.DeleteCredentials(t.Context(),
		client,
		hostedCluster, controlPlaneNamespace)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify RoleBinding is deleted
	roleBinding = &rbacv1.RoleBinding{}
	err = client.Get(t.Context(), types.NamespacedName{
		Namespace: hostedCluster.Spec.Platform.Agent.AgentNamespace,
		Name:      bindingName,
	}, roleBinding)
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "RoleBinding should be deleted")

	// Verify Role is deleted
	role = &rbacv1.Role{}
	err = client.Get(t.Context(), types.NamespacedName{
		Namespace: hostedCluster.Spec.Platform.Agent.AgentNamespace,
		Name:      roleName,
	}, role)
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "Role should be deleted")
}

func TestReconcileCAPIInfraCR(t *testing.T) {
	hostedClusterNamespace := "clusters"
	hostedClusterName := "hc1"
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hostedClusterNamespace, hostedClusterName)

	ignitionEndpoint := "ign"
	caSecret := ignitionserver.IgnitionCACertSecret(controlPlaneNamespace)

	APIEndpoint := hyperv1.APIEndpoint{
		Host: "example.com",
		Port: 443,
	}

	tests := map[string]struct {
		hostedCluster         *hyperv1.HostedCluster
		APIEndpoint           hyperv1.APIEndpoint
		controlPlaneNamespace string
		expectedObject        client.Object
	}{
		"When no ign endpoint it should not create infra cluster": {
			controlPlaneNamespace: controlPlaneNamespace,
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hostedClusterName,
					Namespace: hostedClusterNamespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AgentPlatform,
					},
				},
			},
			APIEndpoint:    APIEndpoint,
			expectedObject: nil,
		},
		"When ign endpoint exists it should create infra cluster": {
			controlPlaneNamespace: controlPlaneNamespace,
			hostedCluster: &hyperv1.HostedCluster{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      hostedClusterName,
					Namespace: hostedClusterNamespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AgentPlatform,
					},
				},
				Status: hyperv1.HostedClusterStatus{
					IgnitionEndpoint: ignitionEndpoint,
				},
			},
			APIEndpoint: APIEndpoint,
			expectedObject: &agentv1.AgentCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: controlPlaneNamespace,
					Name:      hostedClusterName,
					// since resource created through fakeclient this is set to 1 to ensure the struct compare works
					ResourceVersion: "1",
				},
				Spec: agentv1.AgentClusterSpec{
					IgnitionEndpoint: &agentv1.IgnitionEndpoint{
						Url:                    "https://" + ignitionEndpoint + "/ignition",
						CaCertificateReference: &agentv1.CaCertificateReference{Name: caSecret.Name, Namespace: caSecret.Namespace},
					},
					ControlPlaneEndpoint: capiv1.APIEndpoint{
						Port: APIEndpoint.Port,
						Host: APIEndpoint.Host,
					},
				},
				Status: agentv1.AgentClusterStatus{},
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()
			goInfraCR, err := Agent{}.ReconcileCAPIInfraCR(t.Context(), client, controllerutil.CreateOrUpdate, test.hostedCluster, test.controlPlaneNamespace, test.APIEndpoint)
			g.Expect(err).To(Not(HaveOccurred()))
			if diff := cmp.Diff(goInfraCR, test.expectedObject); diff != "" {
				t.Errorf("got and expected differ: %s", diff)
			}
		})
	}
}
