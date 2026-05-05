package k8sutil

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHasAnnotationWithValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		annotations map[string]string
		key         string
		value       string
		want        bool
	}{
		{
			name:        "When annotation exists with matching value it should return true",
			annotations: map[string]string{"foo": "bar"},
			key:         "foo",
			value:       "bar",
			want:        true,
		},
		{
			name:        "When annotation exists with different value it should return false",
			annotations: map[string]string{"foo": "baz"},
			key:         "foo",
			value:       "bar",
			want:        false,
		},
		{
			name:        "When annotation does not exist it should return false",
			annotations: map[string]string{"other": "value"},
			key:         "foo",
			value:       "bar",
			want:        false,
		},
		{
			name:        "When annotations map is nil it should return false",
			annotations: nil,
			key:         "foo",
			value:       "bar",
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			obj := &metav1.ObjectMeta{Annotations: tt.annotations}
			g.Expect(HasAnnotationWithValue(obj, tt.key, tt.value)).To(Equal(tt.want))
		})
	}
}

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
			name: "When annotation is valid but HostedCluster does not exist it should return an error",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			builder := fake.NewClientBuilder().WithScheme(scheme)
			for _, obj := range tt.existing {
				builder = builder.WithObjects(obj)
			}
			fakeClient := builder.Build()

			hc, err := HostedClusterFromAnnotation(t.Context(), fakeClient, tt.obj)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				if tt.errSubstr != "" {
					g.Expect(err).To(MatchError(ContainSubstring(tt.errSubstr)))
				}
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(hc.Namespace).To(Equal(tt.wantHCNS))
			g.Expect(hc.Name).To(Equal(tt.wantHCN))
		})
	}
}
