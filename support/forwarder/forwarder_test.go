package forwarder

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestGetRunningKubeAPIServerPod(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name        string
		pods        []client.Object
		cpNamespace string
		wantErr     bool
		errContains string
	}{
		{
			name: "successfully find running kube-apiserver pod",
			pods: []client.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver-1",
						Namespace: "test-namespace",
						Labels: map[string]string{
							"app":                              "kube-apiserver",
							hyperv1.ControlPlaneComponentLabel: "kube-apiserver",
						},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodRunning,
					},
				},
			},
			cpNamespace: "test-namespace",
			wantErr:     false,
		},
		{
			name: "no running kube-apiserver pod found",
			pods: []client.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver-1",
						Namespace: "test-namespace",
						Labels: map[string]string{
							"app":                              "kube-apiserver",
							hyperv1.ControlPlaneComponentLabel: "kube-apiserver",
						},
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodPending,
					},
				},
			},
			cpNamespace: "test-namespace",
			wantErr:     true,
			errContains: "did not find running kube-apiserver pod",
		},
		{
			name: "no kube-apiserver pods found",
			pods: []client.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "other-pod",
						Namespace: "test-namespace",
					},
				},
			},
			cpNamespace: "test-namespace",
			wantErr:     true,
			errContains: "did not find running kube-apiserver pod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.pods...).
				Build()

			got, err := GetRunningKubeAPIServerPod(context.Background(), fakeClient, tt.cpNamespace)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got none")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %v", tt.errContains, err)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if got == nil {
				t.Error("expected pod but got nil")
				return
			}

			if got.Status.Phase != corev1.PodRunning {
				t.Errorf("expected pod phase to be Running, got %v", got.Status.Phase)
			}

			if got.Namespace != tt.cpNamespace {
				t.Errorf("expected pod namespace to be %q, got %q", tt.cpNamespace, got.Namespace)
			}

			// Verify required labels
			requiredLabels := map[string]string{
				"app":                              "kube-apiserver",
				hyperv1.ControlPlaneComponentLabel: "kube-apiserver",
			}
			for key, value := range requiredLabels {
				if got.Labels[key] != value {
					t.Errorf("expected pod to have label %q=%q, got %q", key, value, got.Labels[key])
				}
			}
		})
	}
}
