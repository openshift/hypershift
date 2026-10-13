package util

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHostedClusterFromAnnotation(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := hyperv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	existingHC := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "clusters",
		},
	}

	tests := []struct {
		name      string
		obj       client.Object
		existing  []client.Object
		wantErr   bool
		wantHCNS  string
		wantHCN   string
		errSubstr string
	}{
		{
			name: "When annotation is missing it should return an error",
			obj: &hyperv1.AzurePrivateLinkService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pls",
					Namespace: "ns",
				},
			},
			wantErr:   true,
			errSubstr: "missing",
		},
		{
			name: "When annotation value is empty it should return a format error",
			obj: &hyperv1.AzurePrivateLinkService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pls",
					Namespace: "ns",
					Annotations: map[string]string{
						HostedClusterAnnotation: "",
					},
				},
			},
			wantErr:   true,
			errSubstr: "invalid",
		},
		{
			name: "When annotation has no slash it should return a format error",
			obj: &hyperv1.AzurePrivateLinkService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pls",
					Namespace: "ns",
					Annotations: map[string]string{
						HostedClusterAnnotation: "invalid",
					},
				},
			},
			wantErr:   true,
			errSubstr: "invalid",
		},
		{
			name: "When annotation has empty namespace it should return a format error",
			obj: &hyperv1.AzurePrivateLinkService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pls",
					Namespace: "ns",
					Annotations: map[string]string{
						HostedClusterAnnotation: "/my-cluster",
					},
				},
			},
			wantErr:   true,
			errSubstr: "invalid",
		},
		{
			name: "When annotation has empty name it should return a format error",
			obj: &hyperv1.AzurePrivateLinkService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pls",
					Namespace: "ns",
					Annotations: map[string]string{
						HostedClusterAnnotation: "clusters/",
					},
				},
			},
			wantErr:   true,
			errSubstr: "invalid",
		},
		{
			name: "When HostedCluster does not exist it should return a not found error",
			obj: &hyperv1.AzurePrivateLinkService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pls",
					Namespace: "ns",
					Annotations: map[string]string{
						HostedClusterAnnotation: "clusters/nonexistent",
					},
				},
			},
			wantErr:   true,
			errSubstr: "failed to get hosted cluster",
		},
		{
			name: "When annotation is valid and HostedCluster exists it should return the HostedCluster",
			obj: &hyperv1.AzurePrivateLinkService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pls",
					Namespace: "ns",
					Annotations: map[string]string{
						HostedClusterAnnotation: "clusters/my-cluster",
					},
				},
			},
			existing: []client.Object{existingHC},
			wantHCNS: "clusters",
			wantHCN:  "my-cluster",
		},
		{
			name: "When annotation has extra slashes it should handle SplitN correctly",
			obj: &hyperv1.AzurePrivateLinkService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pls",
					Namespace: "ns",
					Annotations: map[string]string{
						HostedClusterAnnotation: "clusters/name/extra",
					},
				},
			},
			wantErr:   true,
			errSubstr: "failed to get hosted cluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			builder := fake.NewClientBuilder().WithScheme(scheme)
			for _, obj := range tt.existing {
				builder = builder.WithObjects(obj)
			}
			fakeClient := builder.Build()

			hc, err := HostedClusterFromAnnotation(context.Background(), fakeClient, tt.obj)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errSubstr)
				}
				if tt.errSubstr != "" && !contains(err.Error(), tt.errSubstr) {
					t.Fatalf("expected error containing %q, got %q", tt.errSubstr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if hc.Namespace != tt.wantHCNS || hc.Name != tt.wantHCN {
				t.Fatalf("expected HC %s/%s, got %s/%s", tt.wantHCNS, tt.wantHCN, hc.Namespace, hc.Name)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
