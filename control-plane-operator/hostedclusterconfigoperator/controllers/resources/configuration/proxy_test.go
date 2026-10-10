package configuration

import (
	"context"
	"errors"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/globalconfig"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestReconcileProxyTrustedCA(t *testing.T) {
	t.Run("When the requested CA is missing, it should return its source identity", func(t *testing.T) {
		assert := NewWithT(t)
		hcp := configurationHCP()
		hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{Proxy: &configv1.ProxySpec{TrustedCA: configv1.ConfigMapNameReference{Name: "missing"}}}
		guest, controlPlane := configurationClients(globalconfig.ProxyConfig())
		root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: &simpleCreateOrUpdater{}}
		assert.Expect(ReconcileProxyTrustedCA(t.Context(), root.hosted(), root.controlPlane(), ProxyParamsFor(hcp.Namespace, hcp.Spec.Configuration))).To(MatchError(ContainSubstring("failed to get referenced TrustedCA configmap control-plane/missing")))
	})
	for _, remove := range []bool{false, true} {
		t.Run(fmt.Sprintf("When the proxy CA changes with removal %t, it should clean both clusters and project only the requested CA", remove), func(t *testing.T) {
			assert := NewWithT(t)
			hcp := configurationHCP()
			name := "new-ca"
			if remove {
				name = ""
			}
			hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{Proxy: &configv1.ProxySpec{TrustedCA: configv1.ConfigMapNameReference{Name: name}}}
			proxy := globalconfig.ProxyConfig()
			proxy.Spec.TrustedCA.Name = "old-ca"
			oldGuest := manifests.ProxyTrustedCAConfigMap("old-ca")
			guest, _ := configurationClients(proxy, oldGuest)
			source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "new-ca", Namespace: hcp.Namespace}, Data: map[string]string{"ca-bundle.crt": "fixture"}}
			oldSource := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "old-ca", Namespace: hcp.Namespace}}
			controlPlane := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(source, oldSource).Build()
			root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: &simpleCreateOrUpdater{}}
			assert.Expect(ReconcileProxyTrustedCA(t.Context(), root.hosted(), root.controlPlane(), ProxyParamsFor(hcp.Namespace, hcp.Spec.Configuration))).To(Succeed())
			assert.Expect(apierrors.IsNotFound(guest.Get(t.Context(), client.ObjectKeyFromObject(oldGuest), oldGuest))).To(BeTrue())
			assert.Expect(apierrors.IsNotFound(controlPlane.Get(t.Context(), client.ObjectKeyFromObject(oldSource), oldSource))).To(BeTrue())
			destination := manifests.ProxyTrustedCAConfigMap("new-ca")
			if remove {
				assert.Expect(apierrors.IsNotFound(guest.Get(t.Context(), client.ObjectKeyFromObject(destination), destination))).To(BeTrue())
			} else {
				assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(destination), destination)).To(Succeed())
				assert.Expect(destination.Data).To(Equal(source.Data))
			}
			assert.Expect(controlPlane.Get(t.Context(), client.ObjectKeyFromObject(source), source)).To(Succeed())
		})
	}
	t.Run("When stale CA deletion fails, it should ignore cleanup errors and still copy the new CA", func(t *testing.T) {
		assert := NewWithT(t)
		hcp := configurationHCP()
		hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{Proxy: &configv1.ProxySpec{TrustedCA: configv1.ConfigMapNameReference{Name: "new-ca"}}}
		proxy := globalconfig.ProxyConfig()
		proxy.Spec.TrustedCA.Name = "old-ca"
		deletes := 0
		failDelete := interceptor.Funcs{Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
			deletes++
			return errors.New("cleanup failure")
		}}
		guest := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(proxy).WithInterceptorFuncs(failDelete).Build()
		source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "new-ca", Namespace: hcp.Namespace}, Data: map[string]string{"ca-bundle.crt": "fixture"}}
		controlPlane := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(source).WithInterceptorFuncs(failDelete).Build()
		root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: &simpleCreateOrUpdater{}}
		assert.Expect(ReconcileProxyTrustedCA(t.Context(), root.hosted(), root.controlPlane(), ProxyParamsFor(hcp.Namespace, hcp.Spec.Configuration))).To(Succeed())
		assert.Expect(deletes).To(Equal(2))
		destination := manifests.ProxyTrustedCAConfigMap("new-ca")
		assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(destination), destination)).To(Succeed())
		assert.Expect(destination.Data).To(Equal(source.Data))
	})
}

func TestReconcileProxyCABundle(t *testing.T) {
	t.Run("When proxy CA is enabled then disabled, it should copy then delete only the guest user bundle", func(t *testing.T) {
		assert := NewWithT(t)
		hcp := configurationHCP()
		hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{Proxy: &configv1.ProxySpec{TrustedCA: configv1.ConfigMapNameReference{Name: "custom-ca"}}}
		guest, _ := configurationClients()
		source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "custom-ca", Namespace: hcp.Namespace}, Data: map[string]string{"ca-bundle.crt": "first"}}
		controlPlane := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(source).Build()
		root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: &simpleCreateOrUpdater{}}
		for _, data := range []string{"first", "updated"} {
			assert.Expect(controlPlane.Get(t.Context(), client.ObjectKeyFromObject(source), source)).To(Succeed())
			source.Data["ca-bundle.crt"] = data
			assert.Expect(controlPlane.Update(t.Context(), source)).To(Succeed())
			assert.Expect(ReconcileProxyCABundle(t.Context(), root.hosted(), root.controlPlane(), ProxyParamsFor(hcp.Namespace, hcp.Spec.Configuration))).To(Succeed())
			destination := manifests.OpenShiftUserCABundle()
			assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(destination), destination)).To(Succeed())
			assert.Expect(destination.Data).To(Equal(source.Data))
		}
		hcp.Spec.Configuration = nil
		assert.Expect(ReconcileProxyCABundle(t.Context(), root.hosted(), root.controlPlane(), ProxyParamsFor(hcp.Namespace, hcp.Spec.Configuration))).To(Succeed())
		assert.Expect(ReconcileProxyCABundle(t.Context(), root.hosted(), root.controlPlane(), ProxyParamsFor(hcp.Namespace, hcp.Spec.Configuration))).To(Succeed())
		destination := manifests.OpenShiftUserCABundle()
		assert.Expect(apierrors.IsNotFound(guest.Get(t.Context(), client.ObjectKeyFromObject(destination), destination))).To(BeTrue())
		assert.Expect(controlPlane.Get(t.Context(), client.ObjectKeyFromObject(source), source)).To(Succeed())
	})

	for _, testCase := range []struct {
		name string
		mode string
	}{
		{name: "When reading the user CA fails, it should preserve destination data and propagate the read error", mode: "read"},
		{name: "When the configured user CA is missing, it should preserve destination data and propagate the not found error", mode: "missing"},
		{name: "When upserting the user CA fails, it should preserve destination data and propagate the upsert error", mode: "upsert"},
		{name: "When deleting the user CA fails, it should preserve destination data and propagate the deletion error", mode: "delete"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mode := testCase.mode
			assert := NewWithT(t)
			failure := errors.New(mode + " failure")
			hcp := populatedConfigurationHCP(t)
			destination := manifests.OpenShiftUserCABundle()
			destination.Data = map[string]string{"ca-bundle.crt": "preserved"}
			upserts, deletes := 0, 0
			guest := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(destination).WithInterceptorFuncs(interceptor.Funcs{Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
				deletes++
				return failure
			}}).Build()
			controlPlane := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "custom-ca", Namespace: hcp.Namespace}, Data: map[string]string{"ca-bundle.crt": "replacement"}}).WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, target client.WithWatch, key client.ObjectKey, object client.Object, opts ...client.GetOption) error {
				if mode == "read" {
					return failure
				}
				return target.Get(ctx, key, object, opts...)
			}}).Build()
			if mode == "missing" {
				hcp.Spec.Configuration.Proxy.TrustedCA.Name = "missing"
			}
			if mode == "delete" {
				hcp.Spec.Configuration = nil
			}
			root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: configurationUpserter{func(context.Context, client.Client, client.Object, controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				upserts++
				return controllerutil.OperationResultNone, failure
			}}}
			err := ReconcileProxyCABundle(t.Context(), root.hosted(), root.controlPlane(), ProxyParamsFor(hcp.Namespace, hcp.Spec.Configuration))
			expectedUpserts, expectedDeletes := 0, 0
			switch mode {
			case "read":
				assert.Expect(err).To(MatchError("cannot get proxy CA bundle ConfigMap: read failure"))
			case "missing":
				assert.Expect(apierrors.IsNotFound(err)).To(BeTrue())
				assert.Expect(err).To(MatchError("cannot get proxy CA bundle ConfigMap: configmaps \"missing\" not found"))
			case "upsert":
				expectedUpserts = 1
				assert.Expect(err).To(MatchError("failed to reconcile the proxy CA bundle ConfigMap: upsert failure"))
			case "delete":
				expectedDeletes = 1
				assert.Expect(err).To(MatchError("error deleting *v1.ConfigMap: delete failure"))
			}
			if mode != "missing" {
				assert.Expect(errors.Is(err, failure)).To(BeTrue())
			}
			assert.Expect(upserts).To(Equal(expectedUpserts))
			assert.Expect(deletes).To(Equal(expectedDeletes))
			actual := manifests.OpenShiftUserCABundle()
			assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(actual), actual)).To(Succeed())
			assert.Expect(actual.Data).To(Equal(destination.Data))
		})
	}
}
