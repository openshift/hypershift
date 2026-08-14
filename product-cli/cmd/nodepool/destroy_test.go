package nodepool

import (
	"context"
	"fmt"
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/spf13/pflag"
)

func TestNewDestroyCommand(t *testing.T) {
	t.Parallel()

	t.Run("When destroy nodepool command is created, it should register exactly the expected flags", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		cmd := NewDestroyCommand()
		expectedFlags := []string{"name", "namespace"}
		var actualFlags []string
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			actualFlags = append(actualFlags, f.Name)
		})
		sort.Strings(actualFlags)
		g.Expect(actualFlags).To(Equal(expectedFlags))
	})
}

func TestDestroyNodePoolOptionsRun(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	_ = hyperv1.AddToScheme(scheme)

	tests := []struct {
		name      string
		opts      *DestroyNodePoolOptions
		client    func() crclient.Client
		expectErr string
	}{
		{
			name: "When the NodePool exists, it should delete it successfully and return nil",
			opts: &DestroyNodePoolOptions{
				Name:      "test-np",
				Namespace: "clusters",
			},
			client: func() crclient.Client {
				existingNP := &hyperv1.NodePool{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-np",
						Namespace: "clusters",
					},
				}
				return fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(existingNP).
					Build()
			},
		},
		{
			name: "When the NodePool is not found, it should return nil",
			opts: &DestroyNodePoolOptions{
				Name:      "nonexistent-np",
				Namespace: "clusters",
			},
			client: func() crclient.Client {
				return fake.NewClientBuilder().
					WithScheme(scheme).
					Build()
			},
		},
		{
			name: "When the delete fails with a non-NotFound error, it should return a wrapped error",
			opts: &DestroyNodePoolOptions{
				Name:      "test-np",
				Namespace: "clusters",
			},
			client: func() crclient.Client {
				return fake.NewClientBuilder().
					WithScheme(scheme).
					WithInterceptorFuncs(interceptor.Funcs{
						Delete: func(ctx context.Context, client crclient.WithWatch, obj crclient.Object, opts ...crclient.DeleteOption) error {
							return fmt.Errorf("connection refused")
						},
					}).
					Build()
			},
			expectErr: "failed to delete NodePool clusters/test-np: connection refused",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			err := tc.opts.run(t.Context(), tc.client())

			if tc.expectErr != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tc.expectErr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}
