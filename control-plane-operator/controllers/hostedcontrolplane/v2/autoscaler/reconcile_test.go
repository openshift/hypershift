package autoscaler

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	hyperapi "github.com/openshift/hypershift/support/api"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	. "github.com/onsi/gomega"
)

func TestPredicate(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "hcp-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
		},
	}

	testCases := []struct {
		name                 string
		capiKubeconfigSecret client.Object
		hcpAnnotations       map[string]string

		expected bool
	}{
		{
			name:                 "when CAPI kubeconfig secret exist predicate returns true",
			capiKubeconfigSecret: manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID),
			expected:             true,
		},
		{
			name:     "when CAPI kubeconfig secret doesn't exist, predicate return false",
			expected: false,
		},
		{
			name: "when HCP has DisableMachineManagement annotation predicate return false",
			hcpAnnotations: map[string]string{
				hyperv1.DisableMachineManagement: "true",
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp.Annotations = tc.hcpAnnotations

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.capiKubeconfigSecret != nil {
				clientBuilder = clientBuilder.WithObjects(tc.capiKubeconfigSecret)
				hcp.Status.KubeConfig = &hyperv1.KubeconfigSecretRef{
					Name: tc.capiKubeconfigSecret.GetName(),
				}
			}
			client := clientBuilder.Build()

			cpContext := controlplanecomponent.ControlPlaneContext{
				Context:                  context.Background(),
				Client:                   client,
				CreateOrUpdateProviderV2: upsert.NewV2(false),
				HCP:                      hcp,
			}

			g := NewGomegaWithT(t)

			result, err := Predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestAdaptDeployment(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "hcp-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
		},
	}

	testCases := []struct {
		name              string
		hcpAnnotations    map[string]string
		AutoscalerOptions hyperv1.ClusterAutoscaling
		ExpectedArgs      []string
		expectedReplicas  int32
	}{
		{
			name: "when HCP has DisableClusterAutoscalerAnnotation annotation replicas should be 0",
			hcpAnnotations: map[string]string{
				hyperv1.DisableClusterAutoscalerAnnotation: "true",
			},
			expectedReplicas: 0,
		},
		{
			name: "when autoscaling options is set, container has optional arguments",
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				MaxNodesTotal:        ptr.To[int32](100),
				MaxPodGracePeriod:    ptr.To[int32](300),
				MaxNodeProvisionTime: "20m",
				PodPriorityThreshold: ptr.To[int32](-5),
			},
			ExpectedArgs: []string{
				"--max-nodes-total=100",
				"--max-graceful-termination-sec=300",
				"--max-node-provision-time=20m",
				"--expendable-pods-priority-cutoff=-5",
			},
			expectedReplicas: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp.Annotations = tc.hcpAnnotations
			hcp.Spec.Autoscaling = tc.AutoscalerOptions

			cpContext := controlplanecomponent.ControlPlaneContext{
				HCP: hcp,
			}

			g := NewGomegaWithT(t)

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = AdaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(deployment.Spec.Replicas).To(HaveValue(Equal(tc.expectedReplicas)))

			if len(tc.ExpectedArgs) > 0 {
				observedArgs := deployment.Spec.Template.Spec.Containers[0].Args
				g.Expect(observedArgs).To(ContainElements(tc.ExpectedArgs))
			}
		})
	}
}

func TestReconcileExisting(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "hcp-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra-id",
			Autoscaling: hyperv1.ClusterAutoscaling{
				MaxNodesTotal:        ptr.To[int32](100),
				MaxPodGracePeriod:    ptr.To[int32](300),
				MaxNodeProvisionTime: "20m",
				PodPriorityThreshold: ptr.To[int32](-5),
			},
		},
	}

	capiKubeconfigSecret := manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID)
	hcp.Status.KubeConfig = &hyperv1.KubeconfigSecretRef{
		Name: capiKubeconfigSecret.Name,
	}

	oldDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ComponentName,
			Namespace: hcp.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: ComponentName,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									// exisint resources requests should be preserved.
									corev1.ResourceCPU:    resource.MustParse("777m"),
									corev1.ResourceMemory: resource.MustParse("88Mi"),
								},
							},
						},
					},
				},
			},
		},
	}

	client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(capiKubeconfigSecret).
		WithObjects(oldDeployment).
		Build()
	cpContext := controlplanecomponent.ControlPlaneContext{
		Context:                  context.Background(),
		Client:                   client,
		CreateOrUpdateProviderV2: upsert.NewV2(false),
		ReleaseImageProvider:     testutil.FakeImageProvider(),
		HCP:                      hcp,
	}

	compoent := NewComponent()
	if err := compoent.Reconcile(cpContext); err != nil {
		t.Fatalf("failed to reconciler autoscaler: %v", err)
	}

	var deployments appsv1.DeploymentList
	if err := client.List(context.Background(), &deployments); err != nil {
		t.Fatalf("failed to list deployments: %v", err)
	}

	if len(deployments.Items) == 0 {
		t.Fatalf("expected deployment to exist")
	}

	deploymentYaml, err := util.SerializeResource(&deployments.Items[0], hyperapi.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, deploymentYaml, testutil.WithSuffix("_deployment"))

	// check RBAC is created
	var roleBindings rbacv1.RoleBindingList
	if err := client.List(context.Background(), &roleBindings); err != nil {
		t.Fatalf("failed to list roles: %v", err)
	}

	if len(roleBindings.Items) == 0 {
		t.Fatalf("expected role binding to exist")
	}

	roleBindingYaml, err := util.SerializeResource(&roleBindings.Items[0], hyperapi.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, roleBindingYaml, testutil.WithSuffix("_rolebinding"))
}
