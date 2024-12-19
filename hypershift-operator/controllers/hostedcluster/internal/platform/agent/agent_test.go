package agent

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/upsert"

	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/google/go-cmp/cmp"
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

	roleBinding := &rbacv1.RoleBinding{}
	err = client.Get(context.Background(), types.NamespacedName{
		Namespace: hostedCluster.Spec.Platform.Agent.AgentNamespace,
		Name:      fmt.Sprintf("%s-%s", CredentialsRBACPrefix, controlPlaneNamespace),
	}, roleBinding)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(roleBinding.Subjects[0].Namespace).To(BeIdenticalTo(controlPlaneNamespace))
	g.Expect(roleBinding.Subjects[0].Kind).To(BeIdenticalTo("ServiceAccount"))
	g.Expect(roleBinding.Subjects[0].Name).To(BeIdenticalTo("capi-provider"))
}

func TestDeleteCredentials(t *testing.T) {
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

	// test noop
	err := platform.DeleteCredentials(context.Background(),
		client,
		hostedCluster, controlPlaneNamespace)
	g.Expect(err).ToNot(HaveOccurred())

	// Create the creds
	err = platform.ReconcileCredentials(context.Background(),
		client, upsert.New(false).CreateOrUpdate,
		hostedCluster, controlPlaneNamespace)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify the roleBinding exists
	roleBinding := &rbacv1.RoleBinding{}
	err = client.Get(context.Background(), types.NamespacedName{
		Namespace: hostedCluster.Spec.Platform.Agent.AgentNamespace,
		Name:      fmt.Sprintf("%s-%s", CredentialsRBACPrefix, controlPlaneNamespace),
	}, roleBinding)
	g.Expect(err).ToNot(HaveOccurred())

	err = platform.DeleteCredentials(context.Background(),
		client,
		hostedCluster, controlPlaneNamespace)
	g.Expect(err).ToNot(HaveOccurred())

	roleBinding = &rbacv1.RoleBinding{}
	err = client.Get(context.Background(), types.NamespacedName{
		Namespace: hostedCluster.Spec.Platform.Agent.AgentNamespace,
		Name:      fmt.Sprintf("%s-%s", CredentialsRBACPrefix, controlPlaneNamespace),
	}, roleBinding)
	g.Expect(apierrors.IsNotFound(err)).To(Equal(true))
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
			goInfraCR, err := Agent{}.ReconcileCAPIInfraCR(context.Background(), client, controllerutil.CreateOrUpdate, test.hostedCluster, test.controlPlaneNamespace, test.APIEndpoint)
			g.Expect(err).To(Not(HaveOccurred()))
			if diff := cmp.Diff(goInfraCR, test.expectedObject); diff != "" {
				t.Errorf("got and expected differ: %s", diff)
			}
		})
	}
}
