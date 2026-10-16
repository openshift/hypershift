package configuration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/crd"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestReconcileCRDs(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("When existing is %t, it should reconcile only the request count CRD spec", existing), func(t *testing.T) {
			assert := NewWithT(t)
			guest := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			actual := manifests.RequestCountCRD()
			if existing {
				actual.Labels = map[string]string{"unowned": "preserved"}
				actual.Spec.Group = "stale.example.com"
				assert.Expect(guest.Create(t.Context(), actual)).To(Succeed())
			}
			createOrUpdate := upsert.CreateOrUpdateFN(controllerutil.CreateOrUpdate)
			assert.Expect(reconcileCRDs(t.Context(), guest, createOrUpdate)).To(Succeed())
			assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(actual), actual)).To(Succeed())
			expected := manifests.RequestCountCRD()
			assert.Expect(crd.ReconcileRequestCountCRD(expected)).To(Succeed())
			assert.Expect(actual.Spec).To(Equal(expected.Spec))
			if existing {
				assert.Expect(actual.Labels).To(HaveKeyWithValue("unowned", "preserved"))
			}
		})
	}
	t.Run("When upsert fails, it should preserve the contextual error and sentinel", func(t *testing.T) {
		assert := NewWithT(t)
		failure := errors.New("crd failure")
		guest := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
		createOrUpdate := func(context.Context, client.Client, client.Object, controllerutil.MutateFn) (controllerutil.OperationResult, error) {
			return controllerutil.OperationResultNone, failure
		}
		err := reconcileCRDs(t.Context(), guest, createOrUpdate)
		assert.Expect(err).To(MatchError("failed to reconcile request count crd: crd failure"))
		assert.Expect(errors.Is(err, failure)).To(BeTrue())
	})
}

func TestReconcileNamespaces(t *testing.T) {
	names := []string{"openshift-apiserver", "openshift-infra", "openshift-cloud-controller-manager", "openshift-controller-manager", "openshift-kube-apiserver", "openshift-kube-controller-manager", "openshift-kube-scheduler", "openshift-etcd", "openshift-ingress", "openshift-authentication", "openshift-route-controller-manager"}
	labels := map[string]map[string]string{
		"openshift-infra":          {"pod-security.kubernetes.io/enforce": "privileged", "pod-security.kubernetes.io/audit": "privileged", "pod-security.kubernetes.io/warn": "privileged"},
		"openshift-kube-apiserver": {"openshift.io/cluster-monitoring": "true"},
		"openshift-ingress":        {"openshift.io/cluster-monitoring": "true", "network.openshift.io/policy-group": "ingress"},
	}
	for _, testCase := range []struct {
		name                         string
		existing, disabled, failures bool
	}{
		{name: "When namespaces are absent, it should create the exact ordered catalog"},
		{name: "When namespaces exist, it should merge owned labels and preserve unrelated labels", existing: true},
		{name: "When ingress is disabled, it should omit only the ingress namespace", disabled: true},
		{name: "When multiple upserts fail, it should attempt later namespaces and preserve error order", failures: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			assert := NewWithT(t)
			guest := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			hcp := &hyperv1.HostedControlPlane{}
			if testCase.disabled {
				hcp.Spec.Capabilities = &hyperv1.Capabilities{Disabled: []hyperv1.OptionalCapability{hyperv1.IngressCapability}}
			}
			var expectedNames []string
			for _, name := range names {
				if testCase.disabled && name == "openshift-ingress" {
					continue
				}
				expectedNames = append(expectedNames, name)
				if testCase.existing {
					actual := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"unowned": "preserved"}}}
					for key := range labels[name] {
						actual.Labels[key] = "stale"
					}
					assert.Expect(guest.Create(t.Context(), actual)).To(Succeed())
				}
			}
			first, second := errors.New("first failure"), errors.New("second failure")
			var attempts []string
			createOrUpdate := func(ctx context.Context, guest client.Client, obj client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				attempts = append(attempts, obj.GetName())
				if testCase.failures && obj.GetName() == names[0] {
					return controllerutil.OperationResultNone, first
				}
				if testCase.failures && obj.GetName() == names[2] {
					return controllerutil.OperationResultNone, second
				}
				return controllerutil.CreateOrUpdate(ctx, guest, obj, mutate)
			}
			err := reconcileNamespaces(t.Context(), guest, createOrUpdate, ReconcileParams{Capabilities: hcp.Spec.Capabilities})
			assert.Expect(attempts).To(Equal(expectedNames))
			if testCase.failures {
				assert.Expect(err).To(MatchError("[failed to reconcile namespace openshift-apiserver: first failure, failed to reconcile namespace openshift-cloud-controller-manager: second failure]"))
				assert.Expect(errors.Is(err, first)).To(BeTrue())
				assert.Expect(errors.Is(err, second)).To(BeTrue())
				return
			}
			assert.Expect(err).NotTo(HaveOccurred())
			actualList := &corev1.NamespaceList{}
			assert.Expect(guest.List(t.Context(), actualList)).To(Succeed())
			assert.Expect(actualList.Items).To(HaveLen(len(expectedNames)))
			for _, name := range expectedNames {
				actual := &corev1.Namespace{}
				assert.Expect(guest.Get(t.Context(), client.ObjectKey{Name: name}, actual)).To(Succeed())
				expectedLabels := map[string]string{}
				for key, value := range labels[name] {
					expectedLabels[key] = value
				}
				if testCase.existing {
					expectedLabels["unowned"] = "preserved"
				}
				if len(expectedLabels) == 0 {
					assert.Expect(actual.Labels).To(BeEmpty())
				} else {
					assert.Expect(actual.Labels).To(Equal(expectedLabels))
				}
			}
		})
	}
}

func TestReconcileClusterOperators(t *testing.T) {
	names := []string{"openshift-apiserver", "openshift-controller-manager", "kube-apiserver", "kube-controller-manager", "kube-scheduler", "operator-lifecycle-manager-packageserver"}
	expectedVersions := map[string][]configv1.OperandVersion{
		"openshift-apiserver":                      {{Name: "openshift-apiserver", Version: "4.22.0"}, {Name: "operator", Version: "4.22.0"}},
		"openshift-controller-manager":             {{Name: "openshift-controller-manager", Version: "4.22.0"}, {Name: "operator", Version: "4.22.0"}},
		"kube-apiserver":                           {{Name: "kube-apiserver", Version: "1.35.0"}, {Name: "operator", Version: "4.22.0"}, {Name: "raw-internal", Version: "4.22.0"}},
		"kube-controller-manager":                  {{Name: "kube-controller-manager", Version: "1.35.0"}, {Name: "operator", Version: "4.22.0"}, {Name: "raw-internal", Version: "4.22.0"}},
		"kube-scheduler":                           {{Name: "kube-scheduler", Version: "1.35.0"}, {Name: "operator", Version: "4.22.0"}, {Name: "raw-internal", Version: "4.22.0"}},
		"operator-lifecycle-manager-packageserver": {{Name: "operator", Version: "4.22.0"}},
	}
	expectedMissingVersions := map[string][]configv1.OperandVersion{
		"openshift-apiserver":                      {{Name: "openshift-apiserver", Version: "4.22.0"}, {Name: "operator", Version: "4.22.0"}},
		"openshift-controller-manager":             {{Name: "openshift-controller-manager", Version: "4.22.0"}, {Name: "operator", Version: "4.22.0"}},
		"kube-apiserver":                           {{Name: "operator", Version: "4.22.0"}, {Name: "raw-internal", Version: "4.22.0"}},
		"kube-controller-manager":                  {{Name: "operator", Version: "4.22.0"}, {Name: "raw-internal", Version: "4.22.0"}},
		"kube-scheduler":                           {{Name: "operator", Version: "4.22.0"}, {Name: "raw-internal", Version: "4.22.0"}},
		"operator-lifecycle-manager-packageserver": {{Name: "operator", Version: "4.22.0"}},
	}
	expectedRelatedObjects := map[string][]configv1.ObjectReference{
		"openshift-apiserver": {
			{Group: "operator.openshift.io", Resource: "openshiftapiservers", Name: "cluster"},
			{Resource: "namespaces", Name: "openshift-config"},
			{Resource: "namespaces", Name: "openshift-config-managed"},
			{Resource: "namespaces", Name: "openshift-apiserver-operator"},
			{Resource: "namespaces", Name: "openshift-apiserver"},
			{Group: "apiregistration.k8s.io", Resource: "apiservices", Name: "v1.apps.openshift.io"},
			{Group: "apiregistration.k8s.io", Resource: "apiservices", Name: "v1.authorization.openshift.io"},
			{Group: "apiregistration.k8s.io", Resource: "apiservices", Name: "v1.build.openshift.io"},
			{Group: "apiregistration.k8s.io", Resource: "apiservices", Name: "v1.image.openshift.io"},
			{Group: "apiregistration.k8s.io", Resource: "apiservices", Name: "v1.oauth.openshift.io"},
			{Group: "apiregistration.k8s.io", Resource: "apiservices", Name: "v1.project.openshift.io"},
			{Group: "apiregistration.k8s.io", Resource: "apiservices", Name: "v1.quota.openshift.io"},
			{Group: "apiregistration.k8s.io", Resource: "apiservices", Name: "v1.route.openshift.io"},
			{Group: "apiregistration.k8s.io", Resource: "apiservices", Name: "v1.security.openshift.io"},
			{Group: "apiregistration.k8s.io", Resource: "apiservices", Name: "v1.template.openshift.io"},
			{Group: "apiregistration.k8s.io", Resource: "apiservices", Name: "v1.user.openshift.io"},
		},
		"openshift-controller-manager": {
			{Group: "operator.openshift.io", Resource: "openshiftcontrollermanagers", Name: "cluster"},
			{Resource: "namespaces", Name: "openshift-config"},
			{Resource: "namespaces", Name: "openshift-config-managed"},
			{Resource: "namespaces", Name: "openshift-controller-manager-operator"},
			{Resource: "namespaces", Name: "openshift-controller-manager"},
		},
		"kube-apiserver": {
			{Group: "operator.openshift.io", Resource: "kubeapiservers", Name: "cluster"},
			{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"},
			{Resource: "namespaces", Name: "openshift-config"},
			{Resource: "namespaces", Name: "openshift-config-managed"},
			{Resource: "namespaces", Name: "openshift-kube-apiserver-operator"},
			{Resource: "namespaces", Name: "openshift-kube-apiserver"},
		},
		"kube-controller-manager": {
			{Resource: "namespaces", Name: "openshift-config"},
			{Resource: "namespaces", Name: "openshift-config-managed"},
			{Resource: "namespaces", Name: "openshift-kube-controller-manager"},
			{Resource: "namespaces", Name: "openshift-kube-controller-manager-operator"},
			{Group: "operator.openshift.io", Resource: "kubecontrollermanagers", Name: "cluster"},
		},
		"kube-scheduler": {
			{Group: "operator.openshift.io", Resource: "kubeschedulers", Name: "cluster"},
			{Resource: "namespaces", Name: "openshift-config"},
			{Resource: "namespaces", Name: "openshift-kube-scheduler"},
			{Resource: "namespaces", Name: "openshift-kube-scheduler-operator"},
		},
		"operator-lifecycle-manager-packageserver": {{Resource: "namespaces", Name: "openshift-operator-lifecycle-manager"}},
	}
	for _, testCase := range []struct {
		name               string
		existing, failures bool
		missingVersion     bool
	}{
		{name: "When operators are absent, it should create the exact catalog with distinct release and Kubernetes versions"},
		{name: "When the Kubernetes version is missing, it should omit only operands targeting that version", missingVersion: true},
		{name: "When operators exist, it should replace related objects and preserve stable conditions", existing: true},
		{name: "When multiple operators fail, it should attempt later operators and preserve error order", failures: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			assert := NewWithT(t)
			guest := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			old := metav1.NewTime(time.Unix(1, 0).UTC())
			for _, name := range names {
				if !testCase.existing {
					continue
				}
				assert.Expect(guest.Create(t.Context(), &configv1.ClusterOperator{ObjectMeta: metav1.ObjectMeta{Name: name}, Status: configv1.ClusterOperatorStatus{
					RelatedObjects: []configv1.ObjectReference{{Name: "stale"}},
					Versions:       []configv1.OperandVersion{{Name: "stale", Version: "0.0.0"}},
					Conditions: []configv1.ClusterOperatorStatusCondition{
						{Type: configv1.OperatorAvailable, Status: configv1.ConditionTrue, Reason: hyperv1.AsExpectedReason, LastTransitionTime: old, Message: "preserved"},
						{Type: configv1.OperatorProgressing, Status: configv1.ConditionTrue, Reason: hyperv1.AsExpectedReason, LastTransitionTime: old},
						{Type: configv1.OperatorDegraded, Status: configv1.ConditionFalse, Reason: "stale", LastTransitionTime: old},
					},
				}})).To(Succeed())
			}
			first, second := errors.New("first failure"), errors.New("second failure")
			var attempts []string
			createOrUpdate := func(ctx context.Context, guest client.Client, obj client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				attempts = append(attempts, obj.GetName())
				if testCase.failures && obj.GetName() == names[0] {
					return controllerutil.OperationResultNone, first
				}
				if testCase.failures && obj.GetName() == names[2] {
					return controllerutil.OperationResultNone, second
				}
				return controllerutil.CreateOrUpdate(ctx, guest, obj, mutate)
			}
			versions := map[string]string{"release": "4.22.0", "kubernetes": "1.35.0"}
			if testCase.missingVersion {
				delete(versions, "kubernetes")
			}
			err := reconcileClusterOperators(t.Context(), guest, createOrUpdate, ReconcileParams{Versions: versions})
			assert.Expect(attempts).To(Equal(names))
			if testCase.failures {
				assert.Expect(err).To(MatchError("[failed to reconcile *v1.ClusterOperator openshift-apiserver: first failure, failed to reconcile *v1.ClusterOperator kube-apiserver: second failure]"))
				assert.Expect(errors.Is(err, first)).To(BeTrue())
				assert.Expect(errors.Is(err, second)).To(BeTrue())
				return
			}
			assert.Expect(err).NotTo(HaveOccurred())
			for _, name := range names {
				actual := &configv1.ClusterOperator{}
				assert.Expect(guest.Get(t.Context(), client.ObjectKey{Name: name}, actual)).To(Succeed())
				assert.Expect(actual.Status.Conditions).To(HaveLen(4))
				for index, conditionType := range []configv1.ClusterStatusConditionType{configv1.OperatorAvailable, configv1.OperatorProgressing, configv1.OperatorDegraded, configv1.OperatorUpgradeable} {
					condition := actual.Status.Conditions[index]
					assert.Expect(condition.Type).To(Equal(conditionType))
					expectedStatus := configv1.ConditionFalse
					if index == 0 || index == 3 {
						expectedStatus = configv1.ConditionTrue
					}
					assert.Expect(condition.Status).To(Equal(expectedStatus))
					assert.Expect(condition.Reason).To(Equal(hyperv1.AsExpectedReason))
					if testCase.existing && index == 0 {
						assert.Expect(condition.LastTransitionTime.Equal(&old)).To(BeTrue())
						assert.Expect(condition.Message).To(Equal("preserved"))
					} else {
						assert.Expect(condition.LastTransitionTime.Time.After(old.Time)).To(BeTrue())
					}
				}
				if testCase.missingVersion {
					assert.Expect(actual.Status.Versions).To(Equal(expectedMissingVersions[name]), "operator %s", name)
				} else {
					assert.Expect(actual.Status.Versions).To(Equal(expectedVersions[name]), "operator %s", name)
				}
				assert.Expect(actual.Status.RelatedObjects).To(Equal(expectedRelatedObjects[name]), "operator %s", name)
			}
		})
	}
}
