package resources

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"

	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type foundationUpserter struct {
	upsert.CreateOrUpdateFN
}

func (provider foundationUpserter) CreateOrUpdate(ctx context.Context, guest client.Client, obj client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return provider.CreateOrUpdateFN(ctx, guest, obj, mutate)
}

func TestReconcile(t *testing.T) {
	t.Run("When foundation phases fail, it should preserve global interleaving and wrapped error order", func(t *testing.T) {
		assert := NewWithT(t)
		keys := []string{
			"*v1.CustomResourceDefinition/apirequestcounts.apiserver.openshift.io",
			"*v1.ClusterVersion/version",
			"*v1.ClusterOperator/openshift-apiserver",
			"*v1.Namespace/openshift-apiserver",
		}
		failures := make(map[string]error)
		for index, key := range keys {
			failures[key] = fmt.Errorf("foundation failure %d", index)
		}
		var attempts []string
		root := &reconciler{
			client:         fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(initialObjects...).WithStatusSubresource(&configv1.Infrastructure{}).Build(),
			uncachedClient: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			cpClient:       fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(cpObjects...).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build(),
			platformType:   hyperv1.NonePlatform, clusterSignerCA: "foobar", hcpName: "foo", hcpNamespace: "bar",
			releaseProvider:       &fakereleaseprovider.FakeReleaseProvider{Components: map[string]string{"cli": "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:cli-fake"}},
			ImageMetaDataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProviderHCCO{},
			CreateOrUpdateProvider: foundationUpserter{func(ctx context.Context, guest client.Client, obj client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				key := fmt.Sprintf("%T/%s", obj, obj.GetName())
				attempts = append(attempts, key)
				if failure := failures[key]; failure != nil {
					return controllerutil.OperationResultNone, failure
				}
				return controllerutil.CreateOrUpdate(ctx, guest, obj, mutate)
			}},
		}
		_, err := root.Reconcile(t.Context(), controllerruntime.Request{})
		assert.Expect(err).To(HaveOccurred())
		for _, failure := range failures {
			assert.Expect(errors.Is(err, failure)).To(BeTrue())
		}
		expectedErrors := []string{
			"failed to reconcile crds: failed to reconcile request count crd: foundation failure 0",
			"failed to reconcile clusterversion: failed to reconcile clusterVersion: foundation failure 1",
			"failed to reconcile clusterOperators: failed to reconcile *v1.ClusterOperator openshift-apiserver: foundation failure 2",
			"failed to reconcile namespaces: failed to reconcile namespace openshift-apiserver: foundation failure 3",
		}
		assert.Expect(err).To(MatchError("[" + strings.Join(expectedErrors, ", ") + "]"))
		phases := []string{
			keys[0], "*v1.Endpoints/kubernetes", "*v1.ValidatingAdmissionPolicy/", "*v1.ConfigMap/openshift-install", "*v1.PrometheusRule/",
			keys[1], keys[2], "*v1.ClusterOperator/operator-lifecycle-manager-packageserver", "*v1.Ingress/cluster", keys[3], "*v1.Namespace/openshift-route-controller-manager", "*v1.ClusterRole/",
		}
		previous := -1
		for _, phase := range phases {
			found := -1
			for index, attempt := range attempts {
				if strings.HasPrefix(attempt, phase) {
					found = index
					break
				}
			}
			assert.Expect(found).To(BeNumerically(">", previous), "phase %s; attempts: %v", phase, attempts)
			previous = found
		}
	})
}
