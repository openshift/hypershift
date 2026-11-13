package machine

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/client/clientset/clientset/scheme"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/operator"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func TestSetupSkipsNonKubevirt(t *testing.T) {
	gt := NewWithT(t)

	err := Setup(context.Background(), &operator.HostedClusterConfigOperatorConfig{
		PlatformType: hyperv1.AWSPlatform,
	})

	gt.Expect(err).NotTo(HaveOccurred())
}

func TestSetupKubevirtPropagatesInfraCacheRegistrationError(t *testing.T) {
	gt := NewWithT(t)

	err := Setup(context.Background(), &operator.HostedClusterConfigOperatorConfig{
		Manager:             setupManagerRejectingCache{},
		CPCluster:           setupCPCluster{},
		HCPName:             "hcp",
		Namespace:           "ns",
		PlatformType:        hyperv1.KubevirtPlatform,
		KubevirtInfraConfig: &rest.Config{Host: "https://example.com"},
	})

	gt.Expect(err).To(MatchError("failed to add kubevirt infra cache: manager is stopping"))
}

type setupManager struct {
	manager.Manager
}

func (setupManager) Add(manager.Runnable) error {
	return nil
}

func (setupManager) GetControllerOptions() ctrlconfig.Controller {
	return ctrlconfig.Controller{}
}

type setupManagerRejectingCache struct {
	setupManager
}

func (setupManagerRejectingCache) Add(runnable manager.Runnable) error {
	if _, ok := runnable.(cache.Cache); ok {
		return errors.New("manager is stopping")
	}
	return nil
}

type setupCPCluster struct {
	cluster.Cluster
}

func (setupCPCluster) GetAPIReader() client.Reader {
	return fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(&hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns",
				Name:      "hcp",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Kubevirt: &hyperv1.KubevirtPlatformSpec{},
				},
			},
		}).
		Build()
}

func (setupCPCluster) GetClient() client.Client {
	return nil
}

func (setupCPCluster) GetCache() cache.Cache {
	return nil
}

func TestAddKubevirtInfraCache(t *testing.T) {
	startErr := errors.New("cache start failed")
	addErr := errors.New("manager is stopping")

	tests := []struct {
		name         string
		addErr       error
		startErr     error
		wantAddError string
	}{
		{
			name:     "manager accepts cache",
			startErr: startErr,
		},
		{
			name:         "manager rejects cache",
			addErr:       addErr,
			wantAddError: "failed to add kubevirt infra cache: manager is stopping",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gt := NewWithT(t)
			var added manager.Runnable
			add := func(runnable manager.Runnable) error {
				added = runnable
				return tt.addErr
			}

			err := addKubevirtInfraCache(add, manager.RunnableFunc(func(context.Context) error {
				return tt.startErr
			}))

			if tt.wantAddError != "" {
				gt.Expect(err).To(MatchError(tt.wantAddError))
				return
			}

			gt.Expect(err).NotTo(HaveOccurred())
			gt.Expect(added).NotTo(BeNil())
			gt.Expect(added.Start(context.Background())).To(MatchError(tt.startErr))
		})
	}
}

func TestAddKubevirtInfraCacheStopsOnCancellation(t *testing.T) {
	gt := NewWithT(t)
	var added manager.Runnable

	err := addKubevirtInfraCache(func(runnable manager.Runnable) error {
		added = runnable
		return nil
	}, manager.RunnableFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}))

	gt.Expect(err).NotTo(HaveOccurred())
	gt.Expect(added).NotTo(BeNil())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- added.Start(ctx)
	}()
	cancel()

	gt.Eventually(done, time.Second).Should(Receive(BeNil()))
}
