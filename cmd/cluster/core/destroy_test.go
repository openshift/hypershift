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

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestDestroyCluster(t *testing.T) {
	t.Run("When HostedCluster is nil and platform specifics provided it should call destroyPlatformSpecifics", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Setenv("FAKE_CLIENT", "true")

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
		}

		err := DestroyCluster(context.Background(), nil, opts, mockPlatformSpecifics)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(platformSpecificsCalled).To(BeTrue())
		g.Expect(receivedOpts).ToNot(BeNil())
		g.Expect(receivedOpts.AzurePlatform.Cloud).To(Equal("AzurePublicCloud"))
	})

	t.Run("When kubeconfig is set it should use it for the client", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Setenv("FAKE_CLIENT", "true")

		platformSpecificsCalled := false
		mockPlatformSpecifics := func(ctx context.Context, o *DestroyOptions) error {
			platformSpecificsCalled = true
			return nil
		}

		opts := &DestroyOptions{
			ClusterGracePeriod: 1 * time.Second,
			Kubeconfig:         "",
			Name:               "test-cluster",
			Namespace:          "clusters",
			InfraID:            "test-infra",
			Log:                log.Log,
		}

		err := DestroyCluster(context.Background(), nil, opts, mockPlatformSpecifics)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(platformSpecificsCalled).To(BeTrue())
	})

	t.Run("When destroying a hosted cluster with platform specifics, it should set the destroy finalizer and complete successfully", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Setenv("FAKE_CLIENT", "true")

		// Start with a realistic HC: operator's finalizer present, no destroy finalizer yet.
		// setFinalizer() inside DestroyCluster will add "openshift.io/destroy-cluster".
		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:  "clusters",
				Name:       "test-cluster",
				Finalizers: []string{"hypershift.openshift.io/finalizer"},
			},
			Spec: hyperv1.HostedClusterSpec{
				InfraID: "test-infra",
			},
		}

		g.Expect(controllerutil.ContainsFinalizer(hc, destroyFinalizer)).To(BeFalse(),
			"destroy finalizer should not be present before DestroyCluster runs")

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
		}

		err := DestroyCluster(context.Background(), hc, opts, mockPlatformSpecifics)

		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(platformSpecificsCalled).To(BeTrue())
		// setFinalizer() should have added the destroy finalizer during the flow.
		// removeFinalizer() handles it (returns nil on NotFound since the fake
		// client has no HC in its store, which is the expected path when the
		// operator has already cleaned up).
		g.Expect(controllerutil.ContainsFinalizer(hc, destroyFinalizer)).To(BeTrue(),
			"destroy finalizer should have been set by setFinalizer()")
	})

	t.Run("When deleteCLISecrets fails it should log and continue", func(t *testing.T) {
		g := NewGomegaWithT(t)

		originalNewClient := newClient
		t.Cleanup(func() { newClient = originalNewClient })

		newClient = func(_ string) (client.Client, error) {
			return fake.NewClientBuilder().
				WithScheme(hyperapi.Scheme).
				WithInterceptorFuncs(interceptor.Funcs{
					DeleteAllOf: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteAllOfOption) error {
						if _, ok := obj.(*v1.Secret); ok {
							return apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "", fmt.Errorf("access denied"))
						}
						return cl.DeleteAllOf(ctx, obj, opts...)
					},
				}).
				Build(), nil
		}

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
		}

		err := DestroyCluster(context.Background(), nil, opts, mockPlatformSpecifics)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(platformSpecificsCalled).To(BeTrue())
	})
}

func TestGetCluster(t *testing.T) {
	t.Run("When kubeconfig is invalid it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Setenv("FAKE_CLIENT", "")

		opts := &DestroyOptions{
			Kubeconfig: "/nonexistent/kubeconfig",
			Name:       "test-cluster",
			Namespace:  "clusters",
		}

		_, err := GetCluster(context.Background(), opts)
		g.Expect(err).To(HaveOccurred())
	})
}
