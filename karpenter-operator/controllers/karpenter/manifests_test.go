package karpenter

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr/testr"
)

func TestReconcileKarpenter(t *testing.T) {
	g := NewWithT(t)
	scheme := scheme()

	infraID := "test"
	hcpNamespace := fmt.Sprintf("%s-hcp", infraID)
	kubeconfigName := fmt.Sprintf("%s-kubeconfig", infraID)

	fakeImageOverride := "fakeImageOverride"
	fakeCapiKubeConfigSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kubeconfigName,
			Namespace: hcpNamespace,
		},
		Data: map[string][]byte{
			kubeconfigName: []byte("fake-kubeconfig"),
		},
	}

	testCases := []struct {
		name                      string
		hcp                       *hyperv1.HostedControlPlane
		objects                   []client.Object
		expectReconcileDeployment bool
		expectedImage             string
	}{
		{
			name: "Karpenter uses default provider image",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: infraID,
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "tatooine",
						},
					},
				},
				Status: hyperv1.HostedControlPlaneStatus{
					KubeConfig: &hyperv1.KubeconfigSecretRef{
						Name: kubeconfigName,
						Key:  kubeconfigName,
					},
				},
			},
			objects:                   []client.Object{fakeCapiKubeConfigSecret},
			expectReconcileDeployment: true,
			expectedImage:             "public.ecr.aws/karpenter/controller:1.0.7", // TODO: lifecycle image
		},
		{
			name: "Karpenter uses override provider image",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: hcpNamespace,
					Annotations: map[string]string{
						hyperkarpenterv1.KarpenterProviderAWSImage: fakeImageOverride,
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: infraID,
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "tatooine",
						},
					},
				},
				Status: hyperv1.HostedControlPlaneStatus{
					KubeConfig: &hyperv1.KubeconfigSecretRef{
						Name: kubeconfigName,
						Key:  kubeconfigName,
					},
				},
			},
			objects:                   []client.Object{fakeCapiKubeConfigSecret},
			expectReconcileDeployment: true,
			expectedImage:             fakeImageOverride,
		},
		{
			name: "Karpenter deployment is nil because of incorrect config",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: infraID,
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							Region: "tatooine",
						},
					},
				},
				// missing kubeconfig
			},
			objects:                   []client.Object{fakeCapiKubeConfigSecret},
			expectReconcileDeployment: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			objects := append([]client.Object{tc.hcp}, tc.objects...)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			r := &Reconciler{
				ManagementClient:       fakeClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			}

			ctx := log.IntoContext(context.Background(), testr.New(t))

			err := r.reconcileKarpenter(ctx, tc.hcp)
			g.Expect(err).NotTo(HaveOccurred())

			roles := &rbacv1.RoleList{}
			err = fakeClient.List(ctx, roles, client.InNamespace(tc.hcp.Namespace))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(roles.Items).To(HaveLen(1))

			serviceAccounts := &corev1.ServiceAccountList{}
			err = fakeClient.List(ctx, serviceAccounts, client.InNamespace(tc.hcp.Namespace))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(serviceAccounts.Items).To(HaveLen(1))

			rolebindings := &rbacv1.RoleBindingList{}
			err = fakeClient.List(ctx, rolebindings, client.InNamespace(tc.hcp.Namespace))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(rolebindings.Items).To(HaveLen(1))

			expectedDeploymentsLen := 0
			if tc.expectReconcileDeployment {
				expectedDeploymentsLen = 1
			}
			deployments := &appsv1.DeploymentList{}
			err = fakeClient.List(ctx, deployments, client.InNamespace(tc.hcp.Namespace))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(deployments.Items).To(HaveLen(expectedDeploymentsLen))

			// Verify image
			if tc.expectedImage != "" && len(deployments.Items) > 0 {
				g.Expect(deployments.Items[0].Spec.Template.Spec.Containers[0].Image).To(Equal(tc.expectedImage))
			}
		})
	}
}

type simpleCreateOrUpdater struct{}

func (*simpleCreateOrUpdater) CreateOrUpdate(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return controllerutil.CreateOrUpdate(ctx, c, obj, f)
}
