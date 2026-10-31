package hostedcluster

import (
	"context"
	"reflect"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconcileGlobalConfigSync(t *testing.T) {
	t.Parallel()
	proxyConfig := func(name string) *hyperv1.ClusterConfiguration {
		return &hyperv1.ClusterConfiguration{Proxy: &configv1.ProxySpec{TrustedCA: configv1.ConfigMapNameReference{Name: name}}}
	}
	sharedConfig := proxyConfig("old-ca")
	sharedConfig.Image = &configv1.ImageSpec{AdditionalTrustedCA: configv1.ConfigMapNameReference{Name: "old-ca"}}
	for _, testCase := range []struct {
		name          string
		oldName       string
		initial       *hyperv1.ClusterConfiguration
		configuration *hyperv1.ClusterConfiguration
		persisted     *hyperv1.ClusterConfiguration
		keepOld       bool
	}{
		{name: "When a reference is replaced, it should delete the old copy", oldName: "old-ca", initial: proxyConfig("old-ca"), configuration: proxyConfig("new-ca")},
		{name: "When a reference is removed, it should delete its copy", oldName: "old-ca", initial: proxyConfig("old-ca"), configuration: proxyConfig("")},
		{name: "When configuration becomes nil, it should delete obsolete copies", oldName: "old-ca", initial: proxyConfig("old-ca")},
		{name: "When two fields share a reference, it should retain the remaining field's copy", oldName: "old-ca", initial: sharedConfig, configuration: &hyperv1.ClusterConfiguration{Image: sharedConfig.Image}, keepOld: true},
		{name: "When the persisted HCP still references the old copy, it should retain it", oldName: "old-ca", initial: proxyConfig("old-ca"), configuration: proxyConfig("new-ca"), persisted: proxyConfig("old-ca"), keepOld: true},
		{name: "When the reserved bundle is no longer referenced, it should retain it", oldName: "user-ca-bundle", initial: proxyConfig("user-ca-bundle"), keepOld: true},
		{name: "When an unreferenced legacy copy is unlabeled, it should retain it", oldName: "old-ca", keepOld: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "example"}}
			controlPlaneNamespace := "clusters-example"
			cluster := client.ObjectKeyFromObject(hc).String()
			source := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: testCase.oldName, Labels: map[string]string{"source": "original"}, Annotations: map[string]string{"description": "original"}},
				Data:       map[string]string{"ca-bundle.crt": "original"}, BinaryData: map[string][]byte{"binary": {1}}, Immutable: ptr.To(false),
			}
			newSource := source.DeepCopy()
			newSource.Name = "new-ca"
			newSource.Labels[globalConfigCopyLabel] = "true"
			oldCopy := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: testCase.oldName, Annotations: map[string]string{k8sutil.HostedClusterAnnotation: cluster}}}
			foreign := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: "foreign", Labels: map[string]string{globalConfigCopyLabel: "true"}, Annotations: map[string]string{k8sutil.HostedClusterAnnotation: "clusters/other"}}}
			otherNamespace := foreign.DeepCopy()
			otherNamespace.Namespace = "other-control-plane"
			otherNamespace.Annotations[k8sutil.HostedClusterAnnotation] = cluster
			sourceSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "auth"}, Data: map[string][]byte{"token": []byte("original")}}
			secretCopy := sourceSecret.DeepCopy()
			secretCopy.Namespace = controlPlaneNamespace
			var observedSource, observedCopy *corev1.ConfigMap
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(source, newSource, oldCopy, foreign, otherNamespace, sourceSecret, secretCopy).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, delegate client.WithWatch, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
					err := delegate.Get(ctx, key, object, options...)
					if key == client.ObjectKeyFromObject(source) && err == nil {
						observedSource = object.(*corev1.ConfigMap)
					}
					return err
				},
			}).Build()
			upsertResource := createOrUpdateWithAnnotationFactory(upsert.New(false))(reconcile.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
			createOrUpdate := func(ctx context.Context, currentClient client.Client, object client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				result, err := upsertResource(ctx, currentClient, object, mutate)
				if object.GetName() == source.Name {
					observedCopy = object.(*corev1.ConfigMap)
				}
				return result, err
			}
			reconciler := &HostedClusterReconciler{Client: fakeClient}
			if testCase.initial != nil {
				hc.Spec.Configuration = testCase.initial.DeepCopy()
				hc.Spec.Configuration.Authentication = &configv1.AuthenticationSpec{WebhookTokenAuthenticator: &configv1.WebhookTokenAuthenticator{KubeConfig: configv1.SecretNameReference{Name: sourceSecret.Name}}}
				if err := reconciler.reconcileGlobalConfigSync(ctx, hc, createOrUpdate, controlPlaneNamespace); err != nil {
					t.Fatal(err)
				}
				if observedCopy.Labels[globalConfigCopyLabel] != "true" {
					t.Fatal("active existing copy was not adopted")
				}
				if !reflect.DeepEqual(observedCopy.Data, observedSource.Data) || !reflect.DeepEqual(observedCopy.BinaryData, observedSource.BinaryData) || !reflect.DeepEqual(observedCopy.Immutable, observedSource.Immutable) {
					t.Fatal("copy does not match source content")
				}
				if observedSource.Labels[globalConfigCopyLabel] != "" {
					t.Fatal("tracking label leaked onto source")
				}
				sourceSnapshot := observedSource.DeepCopy()
				observedCopy.Labels["source"] = "changed"
				observedCopy.Annotations["description"] = "changed"
				observedCopy.Data["ca-bundle.crt"] = "changed"
				observedCopy.BinaryData["binary"][0] = 2
				*observedCopy.Immutable = true
				if !reflect.DeepEqual(sourceSnapshot, observedSource) {
					t.Fatal("destination mutations changed source metadata or content")
				}
			}
			if testCase.persisted != nil {
				hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: controlPlaneNamespace, Name: hc.Name}}
				hcp.Spec.Configuration = testCase.persisted.DeepCopy()
				if err := fakeClient.Create(ctx, hcp); err != nil {
					t.Fatal(err)
				}
			}
			hc.Spec.Configuration = testCase.configuration
			for iteration := 0; iteration < 2; iteration++ {
				if err := reconciler.reconcileGlobalConfigSync(ctx, hc, createOrUpdate, controlPlaneNamespace); err != nil {
					t.Fatal(err)
				}
			}
			err := fakeClient.Get(ctx, client.ObjectKeyFromObject(oldCopy), &corev1.ConfigMap{})
			if testCase.keepOld && err != nil {
				t.Fatalf("expected old copy to remain: %v", err)
			}
			if !testCase.keepOld && !apierrors.IsNotFound(err) {
				t.Fatalf("expected old copy to be deleted, got %v", err)
			}
			if testCase.configuration != nil && testCase.configuration.Proxy != nil && testCase.configuration.Proxy.TrustedCA.Name == newSource.Name {
				copy := &corev1.ConfigMap{}
				if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: controlPlaneNamespace, Name: newSource.Name}, copy); err != nil {
					t.Fatal(err)
				}
				if copy.Labels[globalConfigCopyLabel] != "true" || copy.Annotations[k8sutil.HostedClusterAnnotation] != cluster {
					t.Fatal("replacement copy is not logically tracked")
				}
			}
			for _, preserved := range []client.Object{source, newSource, foreign, otherNamespace, sourceSecret, secretCopy} {
				if err := fakeClient.Get(ctx, client.ObjectKeyFromObject(preserved), preserved); err != nil {
					t.Fatalf("expected %T %s to remain: %v", preserved, client.ObjectKeyFromObject(preserved), err)
				}
			}
		})
	}
}
