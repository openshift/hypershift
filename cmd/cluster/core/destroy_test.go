package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/log"
	hyperapi "github.com/openshift/hypershift/support/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
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
			AzurePlatform: AzurePlatformDestroyOptions{
				Cloud:    "AzurePublicCloud",
				Location: "eastus",
			},
			ClientFn: func() (client.Client, error) {
				return fake.NewClientBuilder().Build(), nil
			},
		}

		err := DestroyCluster(context.Background(), nil, opts, mockPlatformSpecifics)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(platformSpecificsCalled).To(BeTrue())
		g.Expect(receivedOpts).ToNot(BeNil())
		g.Expect(receivedOpts.AzurePlatform.Cloud).To(Equal("AzurePublicCloud"))
	})
}

func TestGetCluster(t *testing.T) {
	t.Run("When the HostedCluster exists it should return it", func(t *testing.T) {
		g := NewGomegaWithT(t)
		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: "clusters",
			},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			WithObjects(hc).
			Build()

		opts := &DestroyOptions{
			Name:      "my-cluster",
			Namespace: "clusters",
			Log:       log.Log,
			ClientFn: func() (client.Client, error) {
				return fakeClient, nil
			},
		}

		result, err := GetCluster(context.Background(), opts)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).ToNot(BeNil())
		g.Expect(result.Name).To(Equal("my-cluster"))
	})

	t.Run("When the HostedCluster does not exist it should return nil", func(t *testing.T) {
		g := NewGomegaWithT(t)
		fakeClient := fake.NewClientBuilder().
			WithScheme(hyperapi.Scheme).
			Build()

		opts := &DestroyOptions{
			Name:      "nonexistent",
			Namespace: "clusters",
			InfraID:   "test-infra",
			Log:       log.Log,
			ClientFn: func() (client.Client, error) {
				return fakeClient, nil
			},
		}

		result, err := GetCluster(context.Background(), opts)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(BeNil())
	})

	t.Run("When the client factory returns an error it should propagate", func(t *testing.T) {
		g := NewGomegaWithT(t)
		opts := &DestroyOptions{
			Name:      "test",
			Namespace: "clusters",
			Log:       log.Log,
			ClientFn: func() (client.Client, error) {
				return nil, fmt.Errorf("connection refused")
			},
		}

		result, err := GetCluster(context.Background(), opts)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("connection refused"))
		g.Expect(result).To(BeNil())
	})
}

func TestDestroyOptionsGetClient(t *testing.T) {
	t.Run("When ClientFn is set it should use the provided function", func(t *testing.T) {
		g := NewGomegaWithT(t)
		expectedClient := fake.NewClientBuilder().Build()
		opts := &DestroyOptions{
			ClientFn: func() (client.Client, error) {
				return expectedClient, nil
			},
		}
		c, err := opts.GetClient()
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(c).To(Equal(expectedClient))
	})
}
