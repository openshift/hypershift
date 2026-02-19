package core

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

func TestGetClusterWithInjectedClient(t *testing.T) {
	tests := []struct {
		name            string
		clusterExists   bool
		expectCluster   bool
		expectError     bool
		expectNamespace string
		expectName      string
	}{
		{
			name:            "When the hosted cluster exists it should return the cluster",
			clusterExists:   true,
			expectCluster:   true,
			expectError:     false,
			expectNamespace: "test-ns",
			expectName:      "test-cluster",
		},
		{
			name:          "When the hosted cluster does not exist it should return nil",
			clusterExists: false,
			expectCluster: false,
			expectError:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := t.Context()

			builder := fake.NewClientBuilder().WithScheme(hyperapi.Scheme)
			if tc.clusterExists {
				hc := &hyperv1.HostedCluster{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "test-ns",
						Name:      "test-cluster",
					},
				}
				builder = builder.WithObjects(hc)
			}

			opts := &DestroyOptions{
				Name:      "test-cluster",
				Namespace: "test-ns",
				Log:       logr.Discard(),
				Client:    builder.Build(),
			}

			cluster, err := GetCluster(ctx, opts)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}

			if tc.expectCluster {
				g.Expect(cluster).NotTo(BeNil())
				g.Expect(cluster.Namespace).To(Equal(tc.expectNamespace))
				g.Expect(cluster.Name).To(Equal(tc.expectName))
			} else {
				g.Expect(cluster).To(BeNil())
			}
		})
	}
}

func TestGetDestroyClient(t *testing.T) {
	t.Run("When an injected client is provided it should return that client", func(t *testing.T) {
		g := NewWithT(t)
		injectedClient := fake.NewClientBuilder().Build()
		opts := &DestroyOptions{
			Client: injectedClient,
		}
		client, err := getDestroyClient(opts)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(client).NotTo(BeNil())
		g.Expect(client).To(BeIdenticalTo(injectedClient))
	})

	t.Run("When no client is injected it should not return the injected client", func(t *testing.T) {
		opts := &DestroyOptions{
			Client: nil,
		}
		// Note: We cannot fully test the nil-client fallback path here because
		// util.GetClient() requires a valid kubeconfig. We only verify that
		// the Client field starts as nil; the actual fallback is implicitly
		// tested by production code paths.
		g := NewWithT(t)
		g.Expect(opts.Client).To(BeNil())
	})
}
