package resources

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hosted-cluster-config-operator/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type testClient struct {
	client.Client
	createCount     int
	getErrorCount   int
	randomGetErrors bool
}

var randomSource = rand.New(rand.NewSource(time.Now().UnixNano()))

func (c *testClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.createCount++
	return c.Client.Create(ctx, obj, opts...)
}

func (c *testClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	if c.randomGetErrors {
		if randomSource.Int()%3 == 0 {
			c.getErrorCount++
			return fmt.Errorf("random error")
		}
	}
	return c.Client.Get(ctx, key, obj)
}

func TestReconcileErrorHandling(t *testing.T) {

	// get initial number of creates with no get errors
	var totalCreates int
	{
		fakeClient := &testClient{
			Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
		}

		r := &reconciler{
			client:                 fakeClient,
			CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			platformType:           hyperv1.NonePlatform,
			clusterSignerCA:        "foobar",
			cpClient:               fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(fakeHCP(), fakeIngressCert()).Build(),
			hcpName:                "foo",
			hcpNamespace:           "bar",
		}
		_, err := r.Reconcile(context.Background(), controllerruntime.Request{})
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		totalCreates = fakeClient.createCount
	}

	// test with random get errors
	for i := 0; i < 100; i++ {
		fakeClient := &testClient{
			Client:          fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			randomGetErrors: true,
		}
		r := &reconciler{
			client:                 fakeClient,
			CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			platformType:           hyperv1.NonePlatform,
			clusterSignerCA:        "foobar",
			cpClient:               fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(fakeHCP(), fakeIngressCert()).Build(),
			hcpName:                "foo",
			hcpNamespace:           "bar",
		}
		r.Reconcile(context.Background(), controllerruntime.Request{})
		if totalCreates-fakeClient.getErrorCount != fakeClient.createCount {
			t.Fatalf("Unexpected number of creates: %d/%d with errors %d", fakeClient.createCount, totalCreates, fakeClient.getErrorCount)
		}
	}
}

type simpleCreateOrUpdater struct{}

func (*simpleCreateOrUpdater) CreateOrUpdate(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return controllerutil.CreateOrUpdate(ctx, c, obj, f)
}

func fakeHCP() *hyperv1.HostedControlPlane {
	hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "bar"}}
	hcp.Status.ControlPlaneEndpoint.Host = "server"
	hcp.Status.ControlPlaneEndpoint.Port = 1234
	return hcp
}

func fakeIngressCert() *corev1.Secret {
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "ingress-crt", Namespace: "bar"}}
	s.Data = map[string][]byte{
		"tls.crt": []byte("12345"),
		"tls.key": []byte("12345"),
	}
	return s
}
