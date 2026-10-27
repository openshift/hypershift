package hostedcluster

import (
	"context"
	"errors"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/k8sutil"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestCleanupGlobalConfigMapsPersistedReferences(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name          string
		readError     bool
		configuration *hyperv1.ClusterConfiguration
		keepOld       bool
	}{
		{name: "When the HCP still references the old copy, it should retain it", configuration: globalConfigTestProxy("old-ca"), keepOld: true},
		{name: "When the HCP references the new copy, it should retire the old copy", configuration: globalConfigTestProxy("new-ca")},
		{name: "When HCP configuration is removed, it should retire the old copy"},
		{name: "When the persisted HCP cannot be read, it should defer all deletion", readError: true, keepOld: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			hc := globalConfigTestHostedCluster()
			hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: globalConfigTestNamespace, Name: hc.Name}}
			hcp.Spec.Configuration = testCase.configuration
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcp, globalConfigTestCopy("old-ca"), globalConfigTestCopy("user-ca-bundle")).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, base client.WithWatch, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
					if _, isHCP := object.(*hyperv1.HostedControlPlane); isHCP && testCase.readError {
						return errors.New("HCP read failed")
					}
					return base.Get(ctx, key, object, options...)
				},
			}).Build()
			reconciler := &HostedClusterReconciler{Client: fakeClient}
			desired := sets.New("new-ca")
			err := reconciler.cleanupGlobalConfigMaps(ctx, hc, globalConfigTestNamespace, desired)
			if (err != nil) != testCase.readError {
				t.Fatalf("unexpected cleanup error: %v", err)
			}
			for name, keep := range map[string]bool{"old-ca": testCase.keepOld, "user-ca-bundle": true} {
				err := fakeClient.Get(ctx, client.ObjectKey{Namespace: globalConfigTestNamespace, Name: name}, &corev1.ConfigMap{})
				if keep && err != nil || !keep && !apierrors.IsNotFound(err) {
					t.Fatalf("copy %s: keep=%v, get error=%v", name, keep, err)
				}
			}
			if desired.Has("old-ca") || desired.Has("user-ca-bundle") {
				t.Fatal("cleanup mutated the caller's reference set")
			}
		})
	}
}

func TestReconcileGlobalConfigSyncAdoption(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name      string
		legacy    bool
		reserved  bool
		malformed bool
	}{
		{name: "When an active copy belongs to an old instance, it should adopt and later prune it"},
		{name: "When an active legacy copy matches the cluster, it should track and later prune it", legacy: true},
		{name: "When the fixed user CA bundle is active, it should synchronize without deletion authority", reserved: true},
		{name: "When the provenance label is invalid, it should reject adoption", malformed: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			hc := globalConfigTestHostedCluster()
			name := "ca-a"
			if testCase.reserved {
				name = "user-ca-bundle"
			}
			hc.Spec.Configuration = globalConfigTestProxy(name)
			destination := globalConfigTestCopy(name)
			destination.Annotations[globalConfigOwnerUIDAnnotation] = "previous-instance"
			if testCase.legacy {
				destination.Labels = nil
				delete(destination.Annotations, globalConfigOwnerUIDAnnotation)
			}
			if testCase.malformed {
				destination.Labels[globalConfigCopyLabel] = "false"
			}
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(globalConfigTestSource(name), destination).Build()
			reconciler := &HostedClusterReconciler{Client: fakeClient}
			err := reconciler.reconcileGlobalConfigSync(ctx, hc, globalConfigTestUpsert(hc), globalConfigTestNamespace)
			if (err != nil) != testCase.malformed {
				t.Fatalf("unexpected sync error: %v", err)
			}
			if testCase.malformed {
				return
			}
			if err := fakeClient.Get(ctx, client.ObjectKeyFromObject(destination), destination); err != nil {
				t.Fatal(err)
			}
			if globalConfigMapTracked(destination, hc) == testCase.reserved {
				t.Fatalf("unexpected tracking: labels=%v annotations=%v", destination.Labels, destination.Annotations)
			}
			if destination.Annotations[k8sutil.HostedClusterAnnotation] != client.ObjectKeyFromObject(hc).String() {
				t.Fatal("logical cluster identity was lost")
			}
			hc.Spec.Configuration = nil
			if err := reconciler.reconcileGlobalConfigSync(ctx, hc, globalConfigTestUpsert(hc), globalConfigTestNamespace); err != nil {
				t.Fatal(err)
			}
			err = fakeClient.Get(ctx, client.ObjectKeyFromObject(destination), &corev1.ConfigMap{})
			if testCase.reserved && err != nil || !testCase.reserved && !apierrors.IsNotFound(err) {
				t.Fatalf("unexpected retirement result: %v", err)
			}
		})
	}
}

func TestGlobalConfigMapTracking(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name      string
		mutate    func(*corev1.ConfigMap)
		wantTrack bool
		wantError bool
	}{
		{name: "When provenance matches, it should preserve tracking", wantTrack: true},
		{name: "When the destination is new, it should enable tracking", mutate: func(configMap *corev1.ConfigMap) {
			configMap.ResourceVersion, configMap.Labels, configMap.Annotations = "", nil, nil
		}, wantTrack: true},
		{name: "When an active copy belongs to an old instance, it should enable tracking", mutate: func(configMap *corev1.ConfigMap) {
			configMap.Annotations[globalConfigOwnerUIDAnnotation] = "previous-instance"
		}, wantTrack: true},
		{name: "When an active legacy copy matches, it should enable tracking", mutate: func(configMap *corev1.ConfigMap) {
			configMap.Labels = nil
			delete(configMap.Annotations, globalConfigOwnerUIDAnnotation)
		}, wantTrack: true},
		{name: "When the destination is reserved, it should not enable tracking", mutate: func(configMap *corev1.ConfigMap) {
			configMap.Name = "user-ca-bundle"
		}},
		{name: "When an existing destination has no logical identity, it should reject tracking", mutate: func(configMap *corev1.ConfigMap) {
			configMap.Labels, configMap.Annotations = nil, nil
		}, wantError: true},
		{name: "When the logical cluster differs, it should reject tracking", mutate: func(configMap *corev1.ConfigMap) {
			configMap.Annotations[k8sutil.HostedClusterAnnotation] = "clusters/other"
		}, wantError: true},
		{name: "When tracking provenance is partial, it should reject tracking", mutate: func(configMap *corev1.ConfigMap) {
			delete(configMap.Annotations, globalConfigOwnerUIDAnnotation)
		}, wantError: true},
		{name: "When the owner UID is empty, it should reject tracking", mutate: func(configMap *corev1.ConfigMap) {
			configMap.Annotations[globalConfigOwnerUIDAnnotation] = ""
		}, wantError: true},
		{name: "When another ownerReference exists, it should reject tracking", mutate: func(configMap *corev1.ConfigMap) {
			configMap.OwnerReferences = []metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: "other", UID: "other"}}
		}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			configMap := globalConfigTestCopy("ca-a")
			if testCase.mutate != nil {
				testCase.mutate(configMap)
			}
			track, err := globalConfigMapTracking(configMap, globalConfigTestHostedCluster())
			if track != testCase.wantTrack || (err != nil) != testCase.wantError {
				t.Fatalf("tracking=%v error=%v; want tracking=%v error=%v", track, err, testCase.wantTrack, testCase.wantError)
			}
		})
	}
}
