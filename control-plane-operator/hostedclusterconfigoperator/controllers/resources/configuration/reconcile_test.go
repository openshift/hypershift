package configuration

import (
	"context"
	"fmt"
	"maps"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestReconcile(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		stage       Stage
		count       int
		objectType  string
		objectNames []string
	}{
		{"When the stage is CRDs, it should attempt only the CRD", CRDs, 1, "*v1.CustomResourceDefinition", []string{"apirequestcounts.apiserver.openshift.io"}},
		{"When the stage is ClusterVersion, it should attempt only the cluster version", ClusterVersion, 1, "*v1.ClusterVersion", []string{"version"}},
		{"When the stage is ClusterOperators, it should attempt only the operator catalog", ClusterOperators, 6, "*v1.ClusterOperator", []string{"openshift-apiserver", "openshift-controller-manager", "kube-apiserver", "kube-controller-manager", "kube-scheduler", "operator-lifecycle-manager-packageserver"}},
		{"When the stage is Namespaces, it should attempt only the namespace catalog", Namespaces, 11, "*v1.Namespace", []string{"openshift-apiserver", "openshift-infra", "openshift-cloud-controller-manager", "openshift-controller-manager", "openshift-kube-apiserver", "openshift-kube-controller-manager", "openshift-kube-scheduler", "openshift-etcd", "openshift-ingress", "openshift-authentication", "openshift-route-controller-manager"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			assert := NewWithT(t)
			params := ReconcileParams{Capabilities: &hyperv1.Capabilities{}, Versions: map[string]string{"release": "4.22.0", "kubernetes": "1.35.0"}}
			capabilitiesBefore, versionsBefore := params.Capabilities.DeepCopy(), maps.Clone(params.Versions)
			var attempts []string
			createOrUpdate := func(_ context.Context, _ client.Client, object client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				assert.Expect(fmt.Sprintf("%T", object)).To(Equal(testCase.objectType))
				attempts = append(attempts, object.GetName())
				return controllerutil.OperationResultNone, mutate()
			}
			assert.Expect(Reconcile(t.Context(), nil, createOrUpdate, params, testCase.stage)).To(Succeed())
			assert.Expect(attempts).To(HaveLen(testCase.count))
			assert.Expect(attempts).To(Equal(testCase.objectNames))
			assert.Expect(params.Capabilities).To(Equal(capabilitiesBefore))
			assert.Expect(params.Versions).To(Equal(versionsBefore))
		})
	}
	t.Run("When the stage is unknown, it should return an error without invoking upsert", func(t *testing.T) {
		NewWithT(t).Expect(Reconcile(t.Context(), nil, nil, ReconcileParams{}, Stage(-1))).To(MatchError("unknown configuration reconciliation stage: -1"))
	})
}
