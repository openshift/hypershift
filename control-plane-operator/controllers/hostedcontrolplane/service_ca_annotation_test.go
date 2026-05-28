package hostedcontrolplane

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDoesServiceHaveServiceCAAnnotation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		annotations map[string]string
		expected    bool
	}{
		{
			name:        "no annotations",
			annotations: nil,
			expected:    false,
		},
		{
			name: "beta annotation present, no error",
			annotations: map[string]string{
				servingCertSecretNameBeta: "my-cert",
			},
			expected: true,
		},
		{
			name: "alpha annotation present, no error",
			annotations: map[string]string{
				servingCertSecretNameAlpha: "my-cert",
			},
			expected: true,
		},
		{
			name: "beta annotation present with beta generation error",
			annotations: map[string]string{
				servingCertSecretNameBeta: "my-cert",
				servingCertGenErrorBeta:   "secret does not have corresponding service UID",
			},
			expected: false,
		},
		{
			name: "alpha annotation present with alpha generation error",
			annotations: map[string]string{
				servingCertSecretNameAlpha: "my-cert",
				servingCertGenErrorAlpha:   "secret does not have corresponding service UID",
			},
			expected: false,
		},
		{
			name: "beta annotation present with alpha generation error",
			annotations: map[string]string{
				servingCertSecretNameBeta: "my-cert",
				servingCertGenErrorAlpha:  "UID mismatch",
			},
			expected: false,
		},
		{
			name: "only generation error, no cert annotation",
			annotations: map[string]string{
				servingCertGenErrorBeta: "some error",
			},
			expected: false,
		},
		{
			name: "unrelated annotations only",
			annotations: map[string]string{
				"app.kubernetes.io/name": "test",
			},
			expected: false,
		},
		{
			name: "generation error num only triggers false",
			annotations: map[string]string{
				servingCertSecretNameBeta:   "my-cert",
				servingCertGenErrorNumAlpha: "3",
			},
			expected: false,
		},
	}

	svcBase := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "test-ns",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := svcBase.DeepCopy()
			svc.Annotations = tt.annotations
			got := doesServiceHaveServiceCAAnnotation(svc)
			if got != tt.expected {
				t.Errorf("doesServiceHaveServiceCAAnnotation() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRemoveServiceCAAnnotationAndSecret(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add corev1 to scheme: %v", err)
	}

	tests := []struct {
		name                     string
		serviceAnnotations       map[string]string
		secretExists             bool
		secretAnnotations        map[string]string
		wantRemovedAnnotations   []string
		wantPreservedAnnotations map[string]string
		wantSecretDeleted        bool
	}{
		{
			name: "removes all service-ca annotations in one batch",
			serviceAnnotations: map[string]string{
				servingCertSecretNameBeta:   "my-cert",
				servingCertGenErrorBeta:     "UID mismatch",
				servingCertGenErrorNumBeta:  "5",
				servingCertSecretNameAlpha:  "my-cert",
				servingCertGenErrorAlpha:    "UID mismatch",
				servingCertGenErrorNumAlpha: "5",
				"app.kubernetes.io/name":    "keep-me",
			},
			secretExists: true,
			secretAnnotations: map[string]string{
				"service.beta.openshift.io/originating-service-name": "test-svc",
			},
			wantRemovedAnnotations: []string{
				servingCertSecretNameBeta,
				servingCertGenErrorBeta,
				servingCertGenErrorNumBeta,
				servingCertSecretNameAlpha,
				servingCertGenErrorAlpha,
				servingCertGenErrorNumAlpha,
			},
			wantPreservedAnnotations: map[string]string{
				"app.kubernetes.io/name": "keep-me",
			},
			wantSecretDeleted: true,
		},
		{
			name: "no annotations to remove, no secret",
			serviceAnnotations: map[string]string{
				"app.kubernetes.io/name": "keep-me",
			},
			secretExists: false,
			wantPreservedAnnotations: map[string]string{
				"app.kubernetes.io/name": "keep-me",
			},
			wantSecretDeleted: false,
		},
		{
			name: "only error annotations present",
			serviceAnnotations: map[string]string{
				servingCertGenErrorBeta:    "some error",
				servingCertGenErrorNumBeta: "3",
			},
			secretExists: false,
			wantRemovedAnnotations: []string{
				servingCertGenErrorBeta,
				servingCertGenErrorNumBeta,
			},
			wantSecretDeleted: false,
		},
		{
			name: "secret without originating-service annotation is not deleted",
			serviceAnnotations: map[string]string{
				servingCertSecretNameBeta: "my-cert",
			},
			secretExists: true,
			secretAnnotations: map[string]string{
				"some-other-annotation": "value",
			},
			wantRemovedAnnotations: []string{
				servingCertSecretNameBeta,
			},
			wantSecretDeleted: false,
		},
	}

	ctx := context.Background()
	svcBase := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-svc",
			Namespace: "test-ns",
		},
	}
	secretBase := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cert",
			Namespace: "test-ns",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := svcBase.DeepCopy()
			svc.Annotations = tt.serviceAnnotations

			secretRef := secretBase.DeepCopy()

			objs := []runtime.Object{svc}
			if tt.secretExists {
				secretObj := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-cert",
						Namespace:   "test-ns",
						Annotations: tt.secretAnnotations,
					},
				}
				objs = append(objs, secretObj)
			}

			c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()

			err := removeServiceCAAnnotationAndSecret(ctx, c, svc, secretRef)
			if err != nil {
				t.Fatalf("removeServiceCAAnnotationAndSecret() error = %v", err)
			}

			// Re-fetch the service to verify annotations
			updatedSvc := &corev1.Service{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(svc), updatedSvc); err != nil {
				t.Fatalf("failed to get updated service: %v", err)
			}

			for _, key := range tt.wantRemovedAnnotations {
				if _, ok := updatedSvc.Annotations[key]; ok {
					t.Errorf("expected annotation %q to be removed from service, but it still exists", key)
				}
			}

			for key, wantVal := range tt.wantPreservedAnnotations {
				if gotVal, ok := updatedSvc.Annotations[key]; !ok {
					t.Errorf("expected annotation %q to be preserved, but it was removed", key)
				} else if gotVal != wantVal {
					t.Errorf("annotation %q = %q, want %q", key, gotVal, wantVal)
				}
			}

			// Check secret deletion
			fetchedSecret := &corev1.Secret{}
			err = c.Get(ctx, client.ObjectKey{Name: "test-cert", Namespace: "test-ns"}, fetchedSecret)
			secretGone := apierrors.IsNotFound(err)
			if tt.wantSecretDeleted && !secretGone {
				t.Errorf("expected secret to be deleted, but it still exists")
			}
			if !tt.wantSecretDeleted && tt.secretExists && secretGone {
				t.Errorf("expected secret to be preserved, but it was deleted")
			}
		})
	}
}
