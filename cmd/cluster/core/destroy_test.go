package core

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/log"
	hyperapi "github.com/openshift/hypershift/support/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDestroyCluster(t *testing.T) {
	t.Run("When HostedCluster is nil and platform specifics provided it should call destroyPlatformSpecifics", func(t *testing.T) {
		g := NewGomegaWithT(t)

		platformSpecificsCalled := false
		var receivedOpts *DestroyOptions
		mockPlatformSpecifics := func(ctx context.Context, o *DestroyOptions) error {
			platformSpecificsCalled = true
			receivedOpts = o
			return nil
		}

		opts := &DestroyOptions{
			ClusterGracePeriod: 1 * time.Second,
			Name:               "test-cluster",
			Namespace:          "clusters",
			InfraID:            "test-infra",
			Log:                log.Log,
			Client:             fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build(),
			AzurePlatform: AzurePlatformDestroyOptions{
				Cloud:    "AzurePublicCloud",
				Location: "eastus",
			},
		}

		err := DestroyCluster(context.Background(), nil, opts, mockPlatformSpecifics)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(platformSpecificsCalled).To(BeTrue())
		g.Expect(receivedOpts).ToNot(BeNil())
		g.Expect(receivedOpts.AzurePlatform.Cloud).To(Equal("AzurePublicCloud"))
	})

	t.Run("When HostedCluster exists it should delete it using injected client", func(t *testing.T) {
		g := NewGomegaWithT(t)

		existingCluster := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "clusters",
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(existingCluster).
			Build()

		platformSpecificsCalled := false
		mockPlatformSpecifics := func(ctx context.Context, o *DestroyOptions) error {
			platformSpecificsCalled = true
			return nil
		}

		opts := &DestroyOptions{
			ClusterGracePeriod: 1 * time.Second,
			Name:               "test-cluster",
			Namespace:          "clusters",
			InfraID:            "test-infra",
			Log:                log.Log,
			Client:             fakeClient,
		}

		err := DestroyCluster(context.Background(), existingCluster, opts, mockPlatformSpecifics)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(platformSpecificsCalled).To(BeTrue())
	})
}

func TestGetCluster(t *testing.T) {
	t.Run("When HostedCluster exists it should return it using the injected client", func(t *testing.T) {
		g := NewGomegaWithT(t)

		existingCluster := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "clusters",
			},
		}

		fakeClient := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(existingCluster).
			Build()

		opts := &DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			Log:       log.Log,
			Client:    fakeClient,
		}

		cluster, err := GetCluster(context.Background(), opts)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cluster).ToNot(BeNil())
		g.Expect(cluster.Name).To(Equal("test-cluster"))
		g.Expect(cluster.Namespace).To(Equal("clusters"))
	})

	t.Run("When HostedCluster does not exist it should return nil", func(t *testing.T) {
		g := NewGomegaWithT(t)

		fakeClient := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			Build()

		opts := &DestroyOptions{
			Name:      "nonexistent-cluster",
			Namespace: "clusters",
			InfraID:   "test-infra",
			Log:       log.Log,
			Client:    fakeClient,
		}

		cluster, err := GetCluster(context.Background(), opts)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cluster).To(BeNil())
	})
}
