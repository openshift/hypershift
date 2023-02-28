package upsert

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var _ CreateOrUpdateProvider = &createOrUpdateProvider{}

func TestCreateOrUpdateServices(t *testing.T) {
	existingService := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test-namespace",
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeLoadBalancer,
			Selector: map[string]string{"app": "private-router"},
			Ports: []corev1.ServicePort{
				{Name: "https", Port: 443, TargetPort: intstr.FromString("https"), Protocol: corev1.ProtocolTCP},
				{Name: "kube-apiserver", Port: 6443, TargetPort: intstr.FromString("https"), Protocol: corev1.ProtocolTCP},
			},
		},
	}

	updatedFirstPort := existingService.DeepCopy()
	expectedFirstPort := updatedFirstPort.DeepCopy()
	expectedFirstPort.Spec.Ports[0] = corev1.ServicePort{Name: "https", Port: 443, TargetPort: intstr.FromInt(443), Protocol: corev1.ProtocolTCP}

	testsCases := []struct {
		name                    string
		initialService          *corev1.Service
		mutateFunction          controllerutil.MutateFn
		expectedOperationResult controllerutil.OperationResult
		expectedService         *corev1.Service
	}{
		{
			name:           "existing service, nothing mutated. Operation result should be None",
			initialService: existingService.DeepCopy(),
			mutateFunction: func() error {
				return nil
			},
			expectedOperationResult: controllerutil.OperationResultNone,
			expectedService:         existingService.DeepCopy(),
		},
		{
			name:           "existing service with same ports copied over",
			initialService: updatedFirstPort,
			mutateFunction: func() error {
				updatedFirstPort.Spec.Ports[0] = corev1.ServicePort{
					Name: "https",
					Port: 443,
				}
				return nil
			},
			expectedOperationResult: controllerutil.OperationResultUpdated,
			expectedService:         expectedFirstPort,
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			client := fake.NewClientBuilder().WithRuntimeObjects(tc.initialService).Build()
			operationResult, err := (&createOrUpdateProvider{}).CreateOrUpdate(context.Background(), client, tc.initialService, tc.mutateFunction)
			if err != nil {
				t.Fatalf("CreateOrUpdate failed: %v", err)
			}

			if operationResult != tc.expectedOperationResult {
				t.Fatalf("expected operation result %s, got %s", tc.expectedOperationResult, operationResult)
			}

			updatedService := &corev1.Service{}
			if err := client.Get(context.Background(), crclient.ObjectKeyFromObject(tc.initialService), updatedService); err != nil {
				t.Fatalf("Error getting updated service: %v", err)
			}

			g.Expect(updatedService.Spec).To(Equal(tc.expectedService.Spec))
		})
	}
}

func TestCreateOrUpdateDeployments(t *testing.T) {
	existingDeployment := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test-namespace",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "privileged-container",
							SecurityContext: &corev1.SecurityContext{
								Privileged: Bool(true),
							},
						},
						{
							Name: "kube-apiserver",
							SecurityContext: &corev1.SecurityContext{
								Privileged: Bool(false),
							},
						},
					},
					ServiceAccountName: "service-account",
				},
			},
		},
	}

	updateFirstContainer := existingDeployment.DeepCopy()
	expectedFirstContainer := updateFirstContainer.DeepCopy()
	expectedFirstContainer.Spec.Template.Spec.Containers[0] = corev1.Container{
		Name: "apply-bootstrap",
	}

	updatedFirstContainerPriviliges := existingDeployment.DeepCopy()
	expectedFirstContainerPriviliges := updatedFirstContainerPriviliges.DeepCopy()
	expectedFirstContainerPriviliges.Spec.Template.Spec.Containers[0].SecurityContext.Privileged = Bool(false)

	sameFirstContainerCopySC := existingDeployment.DeepCopy()
	expectedSameFirstContainerCopySC := sameFirstContainerCopySC.DeepCopy()

	testsCases := []struct {
		name                    string
		initialDeployment       *appsv1.Deployment
		mutateFunction          controllerutil.MutateFn
		expectedOperationResult controllerutil.OperationResult
		expectedDeployment      *appsv1.Deployment
	}{
		{
			name:              "existing deployment, nothing changed. Operation result should be None",
			initialDeployment: existingDeployment.DeepCopy(),
			mutateFunction: func() error {
				return nil
			},
			expectedOperationResult: controllerutil.OperationResultNone,
			expectedDeployment:      existingDeployment.DeepCopy(),
		},
		{
			name:              "existing deployment with new container list is updated",
			initialDeployment: updateFirstContainer,
			mutateFunction: func() error {
				updateFirstContainer.Spec.Template.Spec.Containers[0] = corev1.Container{
					Name: "apply-bootstrap",
				}
				return nil
			},
			expectedOperationResult: controllerutil.OperationResultUpdated,
			expectedDeployment:      expectedFirstContainer,
		},
		{
			name:              "existing deployment with existing container list is updated",
			initialDeployment: updatedFirstContainerPriviliges,
			mutateFunction: func() error {
				updatedFirstContainerPriviliges.Spec.Template.Spec.Containers[0].SecurityContext.Privileged = Bool(false)
				return nil
			},
			expectedOperationResult: controllerutil.OperationResultUpdated,
			expectedDeployment:      expectedFirstContainerPriviliges,
		},
		{
			name:              "existing deployment with existing container list copied over. Nothing changed",
			initialDeployment: sameFirstContainerCopySC,
			mutateFunction: func() error {
				sameFirstContainerCopySC.Spec.Template.Spec.Containers[0].SecurityContext = nil
				return nil
			},
			expectedOperationResult: controllerutil.OperationResultNone,
			expectedDeployment:      expectedSameFirstContainerCopySC,
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			client := fake.NewClientBuilder().WithRuntimeObjects(tc.initialDeployment).Build()
			operationResult, err := (&createOrUpdateProvider{}).CreateOrUpdate(context.Background(), client, tc.initialDeployment, tc.mutateFunction)
			if err != nil {
				t.Fatalf("CreateOrUpdate failed: %v", err)
			}

			if operationResult != tc.expectedOperationResult {
				t.Fatalf("expected operation result %s, got %s", tc.expectedOperationResult, operationResult)
			}

			updatedDeployment := &appsv1.Deployment{}
			if err := client.Get(context.Background(), crclient.ObjectKeyFromObject(tc.initialDeployment), updatedDeployment); err != nil {
				t.Fatalf("Error getting updated deployment: %v", err)
			}

			g.Expect(updatedDeployment.Spec).To(Equal(tc.expectedDeployment.Spec))
		})
	}
}

func Bool(b bool) *bool {
	return &b
}
