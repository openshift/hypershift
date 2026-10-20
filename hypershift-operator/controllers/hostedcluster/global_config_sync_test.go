package hostedcluster

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const globalConfigTestNamespace = "clusters-example"

func globalConfigTestHostedCluster() *hyperv1.HostedCluster {
	return &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
		Namespace: "clusters", Name: "example", UID: "fd6db6e0-c587-4d45-8382-d93b6bc6567f",
	}}
}

func globalConfigTestSource(name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: name,
			Labels: map[string]string{"config": "custom"}, Annotations: map[string]string{"description": "custom CA"}},
		Data: map[string]string{"ca-bundle.crt": "trusted-ca"}, BinaryData: map[string][]byte{"binary": {1, 2, 3}}, Immutable: ptr.To(false),
	}
}

func globalConfigTestCopy(name string) *corev1.ConfigMap {
	hc := globalConfigTestHostedCluster()
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Namespace: globalConfigTestNamespace, Name: name, UID: types.UID("ce644728-2066-44e2-85b1-" + name), ResourceVersion: "1",
		Labels:      map[string]string{globalConfigCopyLabel: "true"},
		Annotations: map[string]string{globalConfigOwnerUIDAnnotation: string(hc.UID), k8sutil.HostedClusterAnnotation: client.ObjectKeyFromObject(hc).String()},
	}}
}

func globalConfigTestProxy(name string) *hyperv1.ClusterConfiguration {
	return &hyperv1.ClusterConfiguration{Proxy: &configv1.ProxySpec{TrustedCA: configv1.ConfigMapNameReference{Name: name}}}
}

func globalConfigTestUpsert(hc *hyperv1.HostedCluster) upsert.CreateOrUpdateFN {
	return createOrUpdateWithAnnotationFactory(upsert.New(false))(reconcile.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
}

func TestReconcileGlobalConfigSync(t *testing.T) {
	t.Parallel()
	shared := globalConfigTestProxy("ca-a")
	shared.Image = &configv1.ImageSpec{AdditionalTrustedCA: configv1.ConfigMapNameReference{Name: "ca-a"}}
	fullUnion := globalConfigTestProxy("proxy-ca")
	fullUnion.APIServer = &configv1.APIServerSpec{ClientCA: configv1.ConfigMapNameReference{Name: "api-ca"}}
	fullUnion.Authentication = &configv1.AuthenticationSpec{
		Type: configv1.AuthenticationTypeOIDC, OAuthMetadata: configv1.ConfigMapNameReference{Name: "oauth-metadata"},
		OIDCProviders: []configv1.OIDCProvider{{Issuer: configv1.TokenIssuer{CertificateAuthority: configv1.ConfigMapNameReference{Name: "oidc-ca"}}}},
	}
	fullUnion.Image = &configv1.ImageSpec{AdditionalTrustedCA: configv1.ConfigMapNameReference{Name: "image-ca"}}
	fullUnion.OAuth = &configv1.OAuthSpec{IdentityProviders: []configv1.IdentityProvider{{IdentityProviderConfig: configv1.IdentityProviderConfig{
		BasicAuth: &configv1.BasicAuthIdentityProvider{OAuthRemoteConnectionInfo: configv1.OAuthRemoteConnectionInfo{CA: configv1.ConfigMapNameReference{Name: "idp-ca"}}},
	}}}}
	fullUnion.Scheduler = &configv1.SchedulerSpec{Policy: configv1.ConfigMapNameReference{Name: "scheduler-policy"}}
	testCases := []struct {
		name        string
		configs     []*hyperv1.ClusterConfiguration
		sourceNames []string
		legacy      bool
		tracked     bool
		nilFields   bool
		want        []string
	}{
		{name: "When a reference is replaced, it should remove the old tracked copy", configs: []*hyperv1.ClusterConfiguration{globalConfigTestProxy("ca-a"), globalConfigTestProxy("ca-b")}, sourceNames: []string{"ca-a", "ca-b"}, want: []string{"ca-b"}},
		{name: "When a reference is removed, it should remove its tracked copy", configs: []*hyperv1.ClusterConfiguration{globalConfigTestProxy("ca-a"), globalConfigTestProxy("")}, sourceNames: []string{"ca-a"}},
		{name: "When configuration becomes nil, it should remove tracked copies", configs: []*hyperv1.ClusterConfiguration{globalConfigTestProxy("ca-a"), nil}, sourceNames: []string{"ca-a"}},
		{name: "When configuration becomes empty, it should remove tracked copies", configs: []*hyperv1.ClusterConfiguration{globalConfigTestProxy("ca-a"), {}}, sourceNames: []string{"ca-a"}},
		{name: "When another section still references a shared copy, it should retain that copy", configs: []*hyperv1.ClusterConfiguration{shared, {Image: shared.Image}}, sourceNames: []string{"ca-a"}, want: []string{"ca-a"}},
		{name: "When the last shared reference is removed, it should remove the tracked copy", configs: []*hyperv1.ClusterConfiguration{shared, {Image: shared.Image}, nil}, sourceNames: []string{"ca-a"}},
		{name: "When every supported section references ConfigMaps, it should sync the entire union", configs: []*hyperv1.ClusterConfiguration{fullUnion}, sourceNames: []string{"api-ca", "oauth-metadata", "oidc-ca", "image-ca", "idp-ca", "proxy-ca", "scheduler-policy"}, want: []string{"api-ca", "oauth-metadata", "oidc-ca", "image-ca", "idp-ca", "proxy-ca", "scheduler-policy"}},
		{name: "When an active destination is untracked, it should sync without granting cleanup authority", configs: []*hyperv1.ClusterConfiguration{globalConfigTestProxy("ca-a")}, sourceNames: []string{"ca-a"}, legacy: true, want: []string{"ca-a"}},
		{name: "When an untracked copy becomes obsolete, it should retain that legacy copy", configs: []*hyperv1.ClusterConfiguration{globalConfigTestProxy("ca-a"), nil}, sourceNames: []string{"ca-a"}, legacy: true, want: []string{"ca-a"}},
		{name: "When configuration is initially nil, it should succeed without active sync", configs: []*hyperv1.ClusterConfiguration{nil}},
		{name: "When source contents and metadata are nil, it should copy nil fields and add only destination tracking", configs: []*hyperv1.ClusterConfiguration{globalConfigTestProxy("ca-a")}, sourceNames: []string{"ca-a"}, nilFields: true, want: []string{"ca-a"}},
		{name: "When an existing tracked destination needs an update, it should retain tracking and refresh all fields", configs: []*hyperv1.ClusterConfiguration{globalConfigTestProxy("ca-a")}, sourceNames: []string{"ca-a"}, tracked: true, want: []string{"ca-a"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			hc := globalConfigTestHostedCluster()
			var objects []client.Object
			originals := map[string]*corev1.ConfigMap{}
			for _, name := range testCase.sourceNames {
				source := globalConfigTestSource(name)
				if testCase.nilFields {
					source.Labels, source.Annotations, source.Data, source.BinaryData, source.Immutable = nil, nil, nil, nil, nil
				}
				originals[name] = source.DeepCopy()
				objects = append(objects, source)
			}
			if testCase.legacy {
				objects = append(objects, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: globalConfigTestNamespace, Name: "ca-a"}})
			}
			if testCase.tracked {
				objects = append(objects, globalConfigTestCopy("ca-a"))
			}
			writes := 0
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, base client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					writes++
					obj.SetUID(types.UID("69746a33-33ae-453d-930b-" + obj.GetName()))
					return base.Create(ctx, obj, opts...)
				},
				Update: func(ctx context.Context, base client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					writes++
					return base.Update(ctx, obj, opts...)
				},
				Delete: func(ctx context.Context, base client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					writes++
					options := (&client.DeleteOptions{}).ApplyOptions(opts)
					if options.Preconditions == nil || options.Preconditions.UID == nil || options.Preconditions.ResourceVersion == nil || *options.Preconditions.UID != obj.GetUID() || *options.Preconditions.ResourceVersion != obj.GetResourceVersion() {
						t.Fatal("cleanup must delete with the observed UID and resourceVersion preconditions")
					}
					return base.Delete(ctx, obj, opts...)
				},
			}).Build()
			reconciler := &HostedClusterReconciler{Client: fakeClient}
			for _, configuration := range testCase.configs {
				hc.Spec.Configuration = configuration
				if err := reconciler.reconcileGlobalConfigSync(ctx, hc, globalConfigTestUpsert(hc), globalConfigTestNamespace); err != nil {
					t.Fatal(err)
				}
			}
			before := writes
			if err := reconciler.reconcileGlobalConfigSync(ctx, hc, globalConfigTestUpsert(hc), globalConfigTestNamespace); err != nil {
				t.Fatal(err)
			}
			if writes != before {
				t.Fatalf("unchanged reconciliation wrote %d objects", writes-before)
			}
			var destinations corev1.ConfigMapList
			if err := fakeClient.List(ctx, &destinations, client.InNamespace(globalConfigTestNamespace)); err != nil {
				t.Fatal(err)
			}
			actual := sets.New[string]()
			for _, destination := range destinations.Items {
				actual.Insert(destination.Name)
				if globalConfigMapTracked(&destination, hc) == testCase.legacy {
					t.Fatalf("unexpected tracking state for %s", destination.Name)
				}
				var source corev1.ConfigMap
				if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: hc.Namespace, Name: destination.Name}, &source); err != nil {
					t.Fatal(err)
				}
				assertGlobalConfigCopy(t, &destination, &source)
			}
			if !actual.Equal(sets.New[string](testCase.want...)) {
				t.Fatalf("destination names: got %v, want %v", actual, testCase.want)
			}
			for _, name := range testCase.sourceNames {
				var source corev1.ConfigMap
				if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: hc.Namespace, Name: name}, &source); err != nil {
					t.Fatal(err)
				}
				assertGlobalConfigSourcePreserved(t, &source, originals[name], hc)
			}
		})
	}

	t.Run("When destination fields are mutated, it should not alias source contents or metadata", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		hc := globalConfigTestHostedCluster()
		hc.Spec.Configuration = globalConfigTestProxy("ca-a")
		var observedSource *corev1.ConfigMap
		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(globalConfigTestSource("ca-a")).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, base client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				err := base.Get(ctx, key, obj, opts...)
				if key.Namespace == hc.Namespace {
					observedSource = obj.(*corev1.ConfigMap)
				}
				return err
			},
		}).Build()
		reconciler := &HostedClusterReconciler{Client: fakeClient}
		var observedDestination *corev1.ConfigMap
		mutatingUpsert := func(ctx context.Context, syncClient client.Client, obj client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
			result, err := globalConfigTestUpsert(hc)(ctx, syncClient, obj, mutate)
			observedDestination = obj.(*corev1.ConfigMap)
			return result, err
		}
		if err := reconciler.reconcileGlobalConfigSync(ctx, hc, mutatingUpsert, globalConfigTestNamespace); err != nil {
			t.Fatal(err)
		}
		before := observedSource.DeepCopy()
		if observedSource.Annotations[k8sutil.HostedClusterAnnotation] != "" || observedSource.Labels[globalConfigCopyLabel] != "" {
			t.Fatal("upsert wrapper mutated source metadata")
		}
		observedDestination.Labels["config"] = "changed"
		observedDestination.Annotations["description"] = "changed"
		observedDestination.Data["ca-bundle.crt"] = "changed"
		observedDestination.BinaryData["binary"][0] = 9
		*observedDestination.Immutable = true
		if !reflect.DeepEqual(before, observedSource) {
			t.Fatal("source aliases destination fields")
		}
	})
	t.Run("When API operations fail, it should retain copies and report actionable errors", testReconcileGlobalConfigSyncErrors)
	t.Run("When Secrets are referenced, it should preserve synchronization without cleanup", testReconcileGlobalConfigSyncSecrets)
	t.Run("When a real API server enforces safety, it should preserve immutable and raced copies", testReconcileGlobalConfigSyncAPIServer)
}

func assertGlobalConfigCopy(test *testing.T, destination, source *corev1.ConfigMap) {
	test.Helper()
	if !reflect.DeepEqual(destination.Data, source.Data) || !reflect.DeepEqual(destination.BinaryData, source.BinaryData) || !reflect.DeepEqual(destination.Immutable, source.Immutable) {
		test.Fatalf("copy contents were not preserved on %s", destination.Name)
	}
	labels, annotations := maps.Clone(destination.Labels), maps.Clone(destination.Annotations)
	delete(labels, globalConfigCopyLabel)
	delete(annotations, globalConfigOwnerUIDAnnotation)
	delete(annotations, k8sutil.HostedClusterAnnotation)
	if !maps.Equal(labels, source.Labels) || !maps.Equal(annotations, source.Annotations) {
		test.Fatalf("source user metadata was not preserved on %s", destination.Name)
	}
}

func assertGlobalConfigSourcePreserved(test *testing.T, source, original *corev1.ConfigMap, hc *hyperv1.HostedCluster) {
	test.Helper()
	if source.Labels[globalConfigCopyLabel] != "" || source.Annotations[globalConfigOwnerUIDAnnotation] != "" || source.Annotations[k8sutil.HostedClusterAnnotation] != "" {
		test.Fatalf("destination metadata leaked to source %s", source.Name)
	}
	expected := original.DeepCopy()
	if expected.Annotations == nil {
		expected.Annotations = map[string]string{}
	}
	expected.Annotations[referencedResourceAnnotationPrefix+hc.Name] = "true"
	if !reflect.DeepEqual(source.Data, expected.Data) || !reflect.DeepEqual(source.BinaryData, expected.BinaryData) || !reflect.DeepEqual(source.Immutable, expected.Immutable) || !maps.Equal(source.Labels, expected.Labels) || !maps.Equal(source.Annotations, expected.Annotations) {
		test.Fatalf("source %s changed beyond its approved watch annotation", source.Name)
	}
}

func TestValidateGlobalConfigMapSource(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name    string
		mutate  func(*corev1.ConfigMap)
		wantErr bool
	}{
		{name: "When metadata is nil, it should accept the source", mutate: func(source *corev1.ConfigMap) { source.Labels = nil; source.Annotations = nil }},
		{name: "When source metadata is ordinary, it should preserve it"},
		{name: "When the cluster annotation matches, it should accept the source", mutate: func(source *corev1.ConfigMap) {
			source.Annotations[k8sutil.HostedClusterAnnotation] = "clusters/example"
		}},
		{name: "When the cluster annotation differs, it should reject the source", mutate: func(source *corev1.ConfigMap) { source.Annotations[k8sutil.HostedClusterAnnotation] = "clusters/other" }, wantErr: true},
		{name: "When the reserved label looks valid, it should reject the source", mutate: func(source *corev1.ConfigMap) { source.Labels[globalConfigCopyLabel] = "true" }, wantErr: true},
		{name: "When the reserved label is empty, it should reject the source", mutate: func(source *corev1.ConfigMap) { source.Labels[globalConfigCopyLabel] = "" }, wantErr: true},
		{name: "When the reserved owner UID matches, it should reject the source", mutate: func(source *corev1.ConfigMap) {
			source.Annotations[globalConfigOwnerUIDAnnotation] = string(globalConfigTestHostedCluster().UID)
		}, wantErr: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			source := globalConfigTestSource("ca-a")
			if testCase.mutate != nil {
				testCase.mutate(source)
			}
			before := source.DeepCopy()
			err := validateGlobalConfigMapSource(source, globalConfigTestHostedCluster())
			if (err != nil) != testCase.wantErr || !reflect.DeepEqual(before, source) {
				t.Fatalf("source validation: error %v, want error %v; source changed: %v", err, testCase.wantErr, !reflect.DeepEqual(before, source))
			}
		})
	}
}

func TestGlobalConfigMapTracked(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name   string
		mutate func(*corev1.ConfigMap, *hyperv1.HostedCluster)
		want   bool
	}{
		{name: "When full instance provenance matches, it should recognize the copy", want: true},
		{name: "When the sync label is missing, it should not recognize the copy", mutate: func(copy *corev1.ConfigMap, _ *hyperv1.HostedCluster) { delete(copy.Labels, globalConfigCopyLabel) }},
		{name: "When the sync label differs, it should not recognize the copy", mutate: func(copy *corev1.ConfigMap, _ *hyperv1.HostedCluster) { copy.Labels[globalConfigCopyLabel] = "false" }},
		{name: "When the UID annotation is missing, it should not recognize the copy", mutate: func(copy *corev1.ConfigMap, _ *hyperv1.HostedCluster) {
			delete(copy.Annotations, globalConfigOwnerUIDAnnotation)
		}},
		{name: "When the UID belongs to an old instance, it should not recognize the copy", mutate: func(copy *corev1.ConfigMap, _ *hyperv1.HostedCluster) {
			copy.Annotations[globalConfigOwnerUIDAnnotation] = "old-instance"
		}},
		{name: "When the cluster annotation is missing, it should not recognize the copy", mutate: func(copy *corev1.ConfigMap, _ *hyperv1.HostedCluster) {
			delete(copy.Annotations, k8sutil.HostedClusterAnnotation)
		}},
		{name: "When the cluster name differs, it should not recognize the copy", mutate: func(copy *corev1.ConfigMap, _ *hyperv1.HostedCluster) {
			copy.Annotations[k8sutil.HostedClusterAnnotation] = "clusters/other"
		}},
		{name: "When the cluster namespace differs, it should not recognize the copy", mutate: func(copy *corev1.ConfigMap, _ *hyperv1.HostedCluster) {
			copy.Annotations[k8sutil.HostedClusterAnnotation] = "other/example"
		}},
		{name: "When the HostedCluster UID is empty, it should not recognize the copy", mutate: func(_ *corev1.ConfigMap, hc *hyperv1.HostedCluster) { hc.UID = "" }},
		{name: "When another object owns the copy, it should not recognize the copy", mutate: func(copy *corev1.ConfigMap, _ *hyperv1.HostedCluster) {
			copy.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "other", UID: "other"}}
		}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			copy, hc := globalConfigTestCopy("ca-a"), globalConfigTestHostedCluster()
			if testCase.mutate != nil {
				testCase.mutate(copy, hc)
			}
			if actual := globalConfigMapTracked(copy, hc); actual != testCase.want {
				t.Fatalf("tracked: got %v, want %v", actual, testCase.want)
			}
		})
	}
}

func TestCleanupGlobalConfigMaps(t *testing.T) {
	t.Parallel()
	t.Run("When cleanup safety prerequisites are missing, it should fail closed", testCleanupGlobalConfigMapsGuards)
	t.Run("When an obsolete copy is already absent, it should ignore delete NotFound", func(t *testing.T) {
		t.Parallel()
		fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(globalConfigTestCopy("obsolete")).WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
				return apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, obj.GetName())
			},
		}).Build()
		reconciler := &HostedClusterReconciler{Client: fakeClient}
		if err := reconciler.cleanupGlobalConfigMaps(context.Background(), globalConfigTestHostedCluster(), globalConfigTestNamespace, sets.New[string]()); err != nil {
			t.Fatal(err)
		}
	})
	testCases := []struct {
		name    string
		mutate  func(*corev1.ConfigMap)
		desired []string
		retain  bool
	}{
		{name: "When a tracked copy is obsolete, it should delete it"},
		{name: "When a tracked copy is desired, it should retain it", desired: []string{"ca-a"}, retain: true},
		{name: "When a source carries tracking metadata, it should retain the source", mutate: func(copy *corev1.ConfigMap) { copy.Namespace = "clusters" }, retain: true},
		{name: "When a copy is in another namespace, it should retain it", mutate: func(copy *corev1.ConfigMap) { copy.Namespace = "another-control-plane" }, retain: true},
		{name: "When a copy is untracked legacy data, it should retain it", mutate: func(copy *corev1.ConfigMap) { copy.Labels = nil; copy.Annotations = nil }, retain: true},
		{name: "When a copy has only the generic cluster annotation, it should retain it", mutate: func(copy *corev1.ConfigMap) {
			copy.Labels = nil
			delete(copy.Annotations, globalConfigOwnerUIDAnnotation)
		}, retain: true},
		{name: "When a copy has only the sync label, it should retain it", mutate: func(copy *corev1.ConfigMap) { copy.Annotations = nil }, retain: true},
		{name: "When an old instance owns a copy, it should retain it", mutate: func(copy *corev1.ConfigMap) { copy.Annotations[globalConfigOwnerUIDAnnotation] = "old-instance" }, retain: true},
		{name: "When another cluster owns a copy, it should retain it", mutate: func(copy *corev1.ConfigMap) { copy.Annotations[k8sutil.HostedClusterAnnotation] = "clusters/other" }, retain: true},
		{name: "When another ownerReference exists, it should retain the copy", mutate: func(copy *corev1.ConfigMap) {
			copy.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "other", UID: "other"}}
		}, retain: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			copy := globalConfigTestCopy("ca-a")
			if testCase.mutate != nil {
				testCase.mutate(copy)
			}
			before := copy.DeepCopy()
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(copy).Build()
			reconciler := &HostedClusterReconciler{Client: fakeClient}
			if err := reconciler.cleanupGlobalConfigMaps(ctx, globalConfigTestHostedCluster(), globalConfigTestNamespace, sets.New[string](testCase.desired...)); err != nil {
				t.Fatal(err)
			}
			var actual corev1.ConfigMap
			err := fakeClient.Get(ctx, client.ObjectKeyFromObject(copy), &actual)
			if testCase.retain {
				if err != nil || !reflect.DeepEqual(&actual, before) {
					t.Fatalf("retained object changed: %v, %#v", err, actual)
				}
			} else if !apierrors.IsNotFound(err) {
				t.Fatalf("obsolete copy not deleted: %v", err)
			}
		})
	}
}

func testCleanupGlobalConfigMapsGuards(t *testing.T) {
	t.Helper()
	t.Parallel()
	testCases := []struct {
		name       string
		namespace  string
		emptyUID   bool
		guard      bool
		stripField string
		listError  bool
		wantError  string
	}{
		{name: "When the owner UID is empty, it should return before listing", namespace: globalConfigTestNamespace, emptyUID: true, guard: true, wantError: "global config cleanup requires"},
		{name: "When the destination namespace is empty, it should return before listing", guard: true, wantError: "global config cleanup requires"},
		{name: "When the destination is the source namespace, it should return before listing", namespace: "clusters", guard: true, wantError: "global config cleanup requires"},
		{name: "When the candidate UID is missing, it should refuse an unguarded delete", namespace: globalConfigTestNamespace, stripField: "uid", wantError: "without UID and resourceVersion"},
		{name: "When the candidate resourceVersion is missing, it should refuse an unguarded delete", namespace: globalConfigTestNamespace, stripField: "resourceVersion", wantError: "without UID and resourceVersion"},
		{name: "When List returns NotFound, it should propagate the error rather than ignore it", namespace: globalConfigTestNamespace, listError: true, wantError: "failed to list global config copies in namespace clusters-example"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			hc := globalConfigTestHostedCluster()
			if testCase.emptyUID {
				hc.UID = ""
			}
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(globalConfigTestCopy("obsolete")).WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, base client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if testCase.guard {
						t.Fatal("invalid cleanup entry must not list")
					}
					if testCase.listError {
						return apierrors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, testCase.namespace)
					}
					if err := base.List(ctx, list, opts...); err != nil {
						return err
					}
					copies := list.(*corev1.ConfigMapList)
					for index := range copies.Items {
						if testCase.stripField == "uid" {
							copies.Items[index].UID = ""
						} else {
							copies.Items[index].ResourceVersion = ""
						}
					}
					return nil
				},
				Delete: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.DeleteOption) error {
					t.Fatal("unsafe cleanup must not delete")
					return nil
				},
			}).Build()
			reconciler := &HostedClusterReconciler{Client: fakeClient}
			err := reconciler.cleanupGlobalConfigMaps(ctx, hc, testCase.namespace, sets.New[string]())
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("got %v, want %s", err, testCase.wantError)
			}
			if testCase.listError && !apierrors.IsNotFound(err) {
				t.Fatalf("List error classification lost: %v", err)
			}
			if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: globalConfigTestNamespace, Name: "obsolete"}, &corev1.ConfigMap{}); err != nil {
				t.Fatalf("unsafe cleanup changed the candidate: %v", err)
			}
		})
	}
}

func testReconcileGlobalConfigSyncErrors(t *testing.T) {
	t.Helper()
	t.Parallel()
	sentinel := errors.New("injected API failure")
	testCases := []struct {
		name       string
		operation  string
		mutate     func(*corev1.ConfigMap, *corev1.ConfigMap, *hyperv1.HostedCluster)
		namespace  string
		missing    bool
		wantPrefix string
	}{
		{name: "When source Get fails, it should preserve the cause and skip cleanup", operation: "source-get", wantPrefix: "failed to get referenced configmap clusters/ca-a"},
		{name: "When the source is missing, it should return NotFound and skip cleanup", missing: true, wantPrefix: "failed to get referenced configmap clusters/ca-a"},
		{name: "When destination Get fails, it should preserve the cause and skip cleanup", operation: "destination-get", wantPrefix: "failed to reconcile referenced config map clusters-example/ca-a"},
		{name: "When source watch registration fails, it should preserve the cause and skip cleanup", operation: "source-update", wantPrefix: "failed to set referenced resource annotation on configmap clusters/ca-a"},
		{name: "When destination creation fails, it should preserve the cause and skip cleanup", operation: "create", wantPrefix: "failed to reconcile referenced config map clusters-example/ca-a"},
		{name: "When destination update fails, it should preserve the cause and skip cleanup", operation: "destination-update", wantPrefix: "failed to reconcile referenced config map clusters-example/ca-a"},
		{name: "When listing copies fails, it should preserve the cause", operation: "list", wantPrefix: "failed to list global config copies in namespace clusters-example"},
		{name: "When deleting a copy fails, it should preserve the cause", operation: "delete", wantPrefix: "failed to delete global config copy clusters-example/obsolete"},
		{name: "When source provenance is supplied, it should reject it without source writes", mutate: func(source, _ *corev1.ConfigMap, _ *hyperv1.HostedCluster) {
			source.Labels[globalConfigCopyLabel] = "true"
		}, wantPrefix: "invalid referenced configmap"},
		{name: "When destination provenance is partial, it should reject adoption", mutate: func(_, destination *corev1.ConfigMap, _ *hyperv1.HostedCluster) {
			delete(destination.Annotations, globalConfigOwnerUIDAnnotation)
		}, wantPrefix: "failed to reconcile referenced config map"},
		{name: "When destination UID belongs to an old instance, it should reject adoption", mutate: func(_, destination *corev1.ConfigMap, _ *hyperv1.HostedCluster) {
			destination.Annotations[globalConfigOwnerUIDAnnotation] = "old-instance"
		}, wantPrefix: "failed to reconcile referenced config map"},
		{name: "When an untracked destination names another cluster, it should reject adoption", mutate: func(_, destination *corev1.ConfigMap, _ *hyperv1.HostedCluster) {
			destination.Labels = nil
			destination.Annotations = map[string]string{k8sutil.HostedClusterAnnotation: "clusters/other"}
		}, wantPrefix: "failed to reconcile referenced config map"},
		{name: "When another ownerReference exists, it should reject active synchronization", mutate: func(_, destination *corev1.ConfigMap, _ *hyperv1.HostedCluster) {
			destination.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "other", UID: "other"}}
		}, wantPrefix: "failed to reconcile referenced config map"},
		{name: "When the HostedCluster UID is empty, it should fail closed", mutate: func(_, _ *corev1.ConfigMap, hc *hyperv1.HostedCluster) { hc.UID = "" }, wantPrefix: "global config sync requires"},
		{name: "When the destination is the source namespace, it should fail closed", namespace: "clusters", wantPrefix: "global config sync requires"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			hc := globalConfigTestHostedCluster()
			hc.Spec.Configuration = globalConfigTestProxy("ca-a")
			source, destination := globalConfigTestSource("ca-a"), globalConfigTestCopy("ca-a")
			if testCase.mutate != nil {
				testCase.mutate(source, destination, hc)
			}
			objects := []client.Object{globalConfigTestCopy("obsolete")}
			if !testCase.missing {
				objects = append(objects, source)
			}
			if testCase.operation != "create" && testCase.mutate != nil || testCase.operation == "destination-update" {
				objects = append(objects, destination)
			}
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, base client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if key.Name == "ca-a" && (testCase.operation == "source-get" && key.Namespace == hc.Namespace || testCase.operation == "destination-get" && key.Namespace == globalConfigTestNamespace) {
						return sentinel
					}
					return base.Get(ctx, key, obj, opts...)
				},
				Create: func(ctx context.Context, base client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if testCase.operation == "create" {
						return sentinel
					}
					obj.SetUID("47a28f9e-49c8-43ef-8a03-49a7a64418f9")
					return base.Create(ctx, obj, opts...)
				},
				Update: func(ctx context.Context, base client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					if testCase.operation == "source-update" && obj.GetNamespace() == hc.Namespace || testCase.operation == "destination-update" && obj.GetNamespace() == globalConfigTestNamespace {
						return sentinel
					}
					if testCase.wantPrefix == "invalid referenced configmap" {
						t.Fatal("conflicting source was written")
					}
					return base.Update(ctx, obj, opts...)
				},
				List: func(ctx context.Context, base client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if testCase.operation == "list" {
						return sentinel
					}
					return base.List(ctx, list, opts...)
				},
				Delete: func(ctx context.Context, _ client.WithWatch, _ client.Object, _ ...client.DeleteOption) error {
					if testCase.operation != "delete" {
						t.Fatal("cleanup ran after synchronization failure")
					}
					return sentinel
				},
			}).Build()
			reconciler := &HostedClusterReconciler{Client: fakeClient}
			namespace := globalConfigTestNamespace
			if testCase.namespace != "" {
				namespace = testCase.namespace
			}
			err := reconciler.reconcileGlobalConfigSync(ctx, hc, globalConfigTestUpsert(hc), namespace)
			if err == nil || !strings.Contains(err.Error(), testCase.wantPrefix) {
				t.Fatalf("error: got %v, want %q", err, testCase.wantPrefix)
			}
			if testCase.operation != "" && !errors.Is(err, sentinel) {
				t.Fatalf("error cause not preserved: %v", err)
			}
			if testCase.missing && !apierrors.IsNotFound(err) {
				t.Fatalf("expected source NotFound: %v", err)
			}
			var obsolete corev1.ConfigMap
			if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: globalConfigTestNamespace, Name: "obsolete"}, &obsolete); err != nil {
				t.Fatalf("obsolete copy should remain after failure: %v", err)
			}
		})
	}
}

func testReconcileGlobalConfigSyncSecrets(t *testing.T) {
	t.Helper()
	t.Parallel()
	testCases := []struct {
		name      string
		aro       bool
		empty     bool
		missing   bool
		wantError string
	}{
		{name: "When a Secret is referenced, it should copy all fields and retain obsolete Secrets"},
		{name: "When an ARO hosted-source Secret is empty, it should preserve its validation and sync", aro: true, empty: true},
		{name: "When an ARO hosted-source Secret contains data, it should reject it before cleanup", aro: true, wantError: "is not empty"},
		{name: "When a referenced Secret is absent, it should report its identity before cleanup", missing: true, wantError: "failed to get referenced secret clusters/auth"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			hc := globalConfigTestHostedCluster()
			hc.Spec.Configuration = &hyperv1.ClusterConfiguration{APIServer: &configv1.APIServerSpec{ServingCerts: configv1.APIServerServingCerts{
				NamedCertificates: []configv1.APIServerNamedServingCert{{ServingCertificate: configv1.SecretNameReference{Name: "auth"}}},
			}}}
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: "auth", Labels: map[string]string{"config": "custom"}, Annotations: map[string]string{"description": "auth"}},
				Data:       map[string][]byte{"token": []byte("test-only-placeholder")}, Immutable: ptr.To(true), Type: corev1.SecretTypeOpaque,
			}
			if testCase.empty {
				secret.Data = nil
			}
			if testCase.aro {
				hc.Spec.Platform.Type = hyperv1.AzurePlatform
				hc.Spec.Platform.Azure = &hyperv1.AzurePlatformSpec{AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities}}
				secret.Annotations[hyperv1.HostedClusterSourcedAnnotation] = "true"
			}
			obsoleteSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: globalConfigTestNamespace, Name: "old-auth"}}
			objects := []client.Object{obsoleteSecret, globalConfigTestCopy("obsolete")}
			if !testCase.missing {
				objects = append(objects, secret)
			}
			var sourceInMemory *corev1.Secret
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, base client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					err := base.Get(ctx, key, obj, opts...)
					if key.Namespace == hc.Namespace {
						sourceInMemory = obj.(*corev1.Secret)
					}
					return err
				},
				List: func(ctx context.Context, base client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if _, secretList := list.(*corev1.SecretList); secretList {
						t.Fatal("global config cleanup listed Secrets")
					}
					return base.List(ctx, list, opts...)
				},
				Delete: func(ctx context.Context, base client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if _, secretObject := obj.(*corev1.Secret); secretObject {
						t.Fatal("global config cleanup deleted a Secret")
					}
					return base.Delete(ctx, obj, opts...)
				},
			}).Build()
			reconciler := &HostedClusterReconciler{Client: fakeClient}
			err := reconciler.reconcileGlobalConfigSync(ctx, hc, globalConfigTestUpsert(hc), globalConfigTestNamespace)
			if testCase.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantError) {
					t.Fatalf("got %v, want %s", err, testCase.wantError)
				}
				if getErr := fakeClient.Get(ctx, client.ObjectKey{Namespace: globalConfigTestNamespace, Name: "obsolete"}, &corev1.ConfigMap{}); getErr != nil {
					t.Fatalf("Secret failure did not prevent cleanup: %v", getErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var destination corev1.Secret
			if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: globalConfigTestNamespace, Name: secret.Name}, &destination); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(destination.Data, secret.Data) || !reflect.DeepEqual(destination.Immutable, secret.Immutable) || destination.Type != secret.Type || !reflect.DeepEqual(destination.Labels, secret.Labels) || destination.Annotations["description"] != "auth" {
				t.Fatal("Secret fields were not preserved")
			}
			if sourceInMemory.Annotations[k8sutil.HostedClusterAnnotation] != "" {
				t.Fatal("upsert wrapper mutated Secret source metadata")
			}
			hc.Spec.Configuration = &hyperv1.ClusterConfiguration{}
			if err := reconciler.reconcileGlobalConfigSync(ctx, hc, globalConfigTestUpsert(hc), globalConfigTestNamespace); err != nil {
				t.Fatal(err)
			}
			hc.Spec.Configuration = nil
			if err := reconciler.reconcileGlobalConfigSync(ctx, hc, globalConfigTestUpsert(hc), globalConfigTestNamespace); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"auth", "old-auth"} {
				if err := fakeClient.Get(ctx, client.ObjectKey{Namespace: globalConfigTestNamespace, Name: name}, &corev1.Secret{}); err != nil {
					t.Fatalf("Secret %s was cleaned up: %v", name, err)
				}
			}
		})
	}
}

func testReconcileGlobalConfigSyncAPIServer(t *testing.T) {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is required for API-server Immutable and delete-precondition regressions")
	}
	testEnv := &envtest.Environment{}
	config, err := testEnv.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := testEnv.Stop(); err != nil {
			t.Error(err)
		}
	})
	apiClient, err := client.NewWithWatch(config, client.Options{Scheme: api.Scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hc := globalConfigTestHostedCluster()
	for _, namespace := range []string{hc.Namespace, globalConfigTestNamespace} {
		if err := apiClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
			t.Fatal(err)
		}
	}
	source := globalConfigTestSource("ca-a")
	if err := apiClient.Create(ctx, source); err != nil {
		t.Fatal(err)
	}
	hc.Spec.Configuration = globalConfigTestProxy(source.Name)
	reconciler := &HostedClusterReconciler{Client: apiClient}
	if err := reconciler.reconcileGlobalConfigSync(ctx, hc, globalConfigTestUpsert(hc), globalConfigTestNamespace); err != nil {
		t.Fatal(err)
	}
	var destination corev1.ConfigMap
	if err := apiClient.Get(ctx, client.ObjectKey{Namespace: globalConfigTestNamespace, Name: source.Name}, &destination); err != nil {
		t.Fatal(err)
	}
	destination.Immutable = ptr.To(true)
	if err := apiClient.Update(ctx, &destination); err != nil {
		t.Fatal(err)
	}
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(source), source); err != nil {
		t.Fatal(err)
	}
	source.Data["ca-bundle.crt"] = "new-trusted-ca"
	if err := apiClient.Update(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.reconcileGlobalConfigSync(ctx, hc, globalConfigTestUpsert(hc), globalConfigTestNamespace); !apierrors.IsInvalid(err) {
		t.Fatalf("immutable destination update should fail without replacement: %v", err)
	}
	var retained corev1.ConfigMap
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(&destination), &retained); err != nil || retained.UID != destination.UID || !reflect.DeepEqual(retained.Data, destination.Data) {
		t.Fatalf("immutable destination was changed or replaced: %v", err)
	}
	hc.Spec.Configuration = nil
	if err := reconciler.reconcileGlobalConfigSync(ctx, hc, globalConfigTestUpsert(hc), globalConfigTestNamespace); err != nil {
		t.Fatalf("obsolete immutable copy should be deletable: %v", err)
	}
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(&destination), &retained); !apierrors.IsNotFound(err) {
		t.Fatalf("obsolete immutable copy was not deleted: %v", err)
	}
	for _, operation := range []string{"update", "replace"} {
		t.Run(fmt.Sprintf("When a candidate is concurrently %sd, it should reject the stale deletion", operation), func(t *testing.T) {
			candidate := globalConfigTestCopy("raced")
			candidate.UID, candidate.ResourceVersion = "", ""
			if err := apiClient.Create(ctx, candidate); err != nil {
				t.Fatal(err)
			}
			racedClient := interceptor.NewClient(apiClient, interceptor.Funcs{
				Delete: func(ctx context.Context, base client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					current := &corev1.ConfigMap{}
					if err := base.Get(ctx, client.ObjectKeyFromObject(obj), current); err != nil {
						return err
					}
					if operation == "replace" {
						if err := base.Delete(ctx, current); err != nil {
							return err
						}
						current.UID, current.ResourceVersion = "", ""
						if err := base.Create(ctx, current); err != nil {
							return err
						}
					} else {
						current.Annotations[globalConfigOwnerUIDAnnotation] = "old-instance"
						if err := base.Update(ctx, current); err != nil {
							return err
						}
					}
					return base.Delete(ctx, obj, opts...)
				},
			})
			reconciler := &HostedClusterReconciler{Client: racedClient}
			if err := reconciler.cleanupGlobalConfigMaps(ctx, hc, globalConfigTestNamespace, sets.New[string]()); !apierrors.IsConflict(err) {
				t.Fatalf("stale deletion should conflict: %v", err)
			}
			current := &corev1.ConfigMap{}
			if err := apiClient.Get(ctx, client.ObjectKeyFromObject(candidate), current); err != nil {
				t.Fatalf("concurrently changed object was deleted: %v", err)
			}
			if err := apiClient.Delete(ctx, current); err != nil {
				t.Fatal(err)
			}
		})
	}
}
