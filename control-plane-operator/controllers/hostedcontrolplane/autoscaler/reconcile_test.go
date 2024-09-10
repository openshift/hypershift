package autoscaler

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	hyperapi "github.com/openshift/hypershift/support/api"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestReconcile(t *testing.T) {
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
		name                      string
		capiKubeconfigSecret      client.Object
		expectedDeploymentToExist bool
		hcpAnnotations            map[string]string
		hcpStatus                 hyperv1.HostedControlPlaneStatus
		expectedReplicas          int32
		autoscalingOptions        hyperv1.ClusterAutoscaling
	}{
		{
			name:                      "when CAPI kubeconfig secret exist, deployment should be reconciled",
			capiKubeconfigSecret:      manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID),
			expectedDeploymentToExist: true,
			expectedReplicas:          1,
		},
		{
			name:                      "when CAPI kubeconfig secret doesn't exist, deployment should not be reconciled",
			expectedDeploymentToExist: false,
		},
		{
			name:                      "when HCP has DisableClusterAutoscalerAnnotation annotation, replicas should be 0",
			capiKubeconfigSecret:      manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID),
			expectedDeploymentToExist: true,
			hcpAnnotations: map[string]string{
				hyperv1.DisableClusterAutoscalerAnnotation: "true",
			},
			expectedReplicas: 0,
		},
		{
			name:                      "with autoscaling options",
			capiKubeconfigSecret:      manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID),
			expectedDeploymentToExist: true,
			expectedReplicas:          1,
			autoscalingOptions: hyperv1.ClusterAutoscaling{
				MaxNodesTotal:        ptr.To[int32](100),
				MaxPodGracePeriod:    ptr.To[int32](300),
				MaxNodeProvisionTime: "20m",
				PodPriorityThreshold: ptr.To[int32](-5),
			},
		},
	}

	createOrUpdate := controllerutil.CreateOrUpdate
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp.Annotations = tc.hcpAnnotations
			hcp.Spec.Autoscaling = tc.autoscalingOptions

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.capiKubeconfigSecret != nil {
				clientBuilder = clientBuilder.WithObjects(tc.capiKubeconfigSecret)
				hcp.Status.KubeConfig = &hyperv1.KubeconfigSecretRef{
					Name: tc.capiKubeconfigSecret.GetName(),
				}
			}
			client := clientBuilder.Build()

			cpContext := controlplanecomponent.ControlPlaneContext{
				Context:        context.Background(),
				Client:         client,
				CreateOrUpdate: createOrUpdate,
				ReleaseImageProvider: imageprovider.NewFromImages(map[string]string{
					ImageStreamAutoscalerImage: "test-image",
				}),
				HCP: hcp,
			}

			compoent := NewComponent()
			if err := compoent.Reconcile(cpContext); err != nil {
				t.Fatalf("failed to reconciler autoscaler: %v", err)
			}

			var deployments appsv1.DeploymentList
			if err := client.List(context.Background(), &deployments); err != nil {
				t.Fatalf("failed to list deployments: %v", err)
			}

			if !tc.expectedDeploymentToExist {
				if len(deployments.Items) > 0 {
					t.Fatalf("expected deployment to not exist")
				}
				return
			}

			if len(deployments.Items) == 0 {
				t.Fatalf("expected deployment to exist")
			}

			autoscalerDeployment := deployments.Items[0]
			if *autoscalerDeployment.Spec.Replicas != tc.expectedReplicas {
				t.Fatalf("expected deployment replicas %d to match expected %d", *autoscalerDeployment.Spec.Replicas, tc.expectedReplicas)
			}

			deploymentYaml, err := util.SerializeResource(&autoscalerDeployment, hyperapi.Scheme)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			testutil.CompareWithFixture(t, deploymentYaml)

			// check RBAC is created
			var roles rbacv1.RoleList
			if err := client.List(context.Background(), &roles); err != nil {
				t.Fatalf("failed to list roles: %v", err)
			}

			if len(roles.Items) == 0 {
				t.Fatalf("expected role to exist")
			}

			if roles.Items[0].Name != ComponentName {
				t.Fatalf("expected role to have name %s, got %s instead", ComponentName, roles.Items[0].Name)
			}
		})
	}
}
