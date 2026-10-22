//go:build e2ev2

package tests

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestNativeRecoveryFixture(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		mutate    func(*hyperv1.HostedCluster, *hyperv1.NodePool)
		wantError bool
	}{
		{name: "When the fixture is isolated HA with zero workers, it should permit native disruption"},
		{name: "When the dedicated marker is missing, it should reject a shared fixture", mutate: func(cluster *hyperv1.HostedCluster, pool *hyperv1.NodePool) { cluster.Annotations = nil }, wantError: true},
		{name: "When another recovery fixture is selected, it should reject cross-fixture mutation", mutate: func(cluster *hyperv1.HostedCluster, pool *hyperv1.NodePool) {
			cluster.Annotations[nativeRecoveryAnnotation] = "another-fixture"
		}, wantError: true},
		{name: "When the control plane is single replica, it should reject disruption", mutate: func(cluster *hyperv1.HostedCluster, pool *hyperv1.NodePool) {
			cluster.Spec.ControllerAvailabilityPolicy = hyperv1.SingleReplica
		}, wantError: true},
		{name: "When workers are configured, it should reject the fixture", mutate: func(cluster *hyperv1.HostedCluster, pool *hyperv1.NodePool) { pool.Spec.Replicas = ptr.To[int32](1) }, wantError: true},
		{name: "When replicas are unspecified, it should reject ambiguous zero-worker intent", mutate: func(cluster *hyperv1.HostedCluster, pool *hyperv1.NodePool) { pool.Spec.Replicas = nil }, wantError: true},
		{name: "When autoscaling is configured, it should reject unstable zero-worker intent", mutate: func(cluster *hyperv1.HostedCluster, pool *hyperv1.NodePool) {
			pool.Spec.AutoScaling = &hyperv1.NodePoolAutoScaling{}
		}, wantError: true},
		{name: "When an unrelated cluster has workers, it should preserve fixture isolation", mutate: func(cluster *hyperv1.HostedCluster, pool *hyperv1.NodePool) {
			pool.Spec.ClusterName = "another-cluster"
			pool.Spec.Replicas = ptr.To[int32](2)
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "recovery", Annotations: map[string]string{nativeRecoveryAnnotation: "native-etcd-recovery"}}, Spec: hyperv1.HostedClusterSpec{ControllerAvailabilityPolicy: hyperv1.HighlyAvailable}}
			pool := &hyperv1.NodePool{Spec: hyperv1.NodePoolSpec{ClusterName: cluster.Name, Replicas: ptr.To[int32](0)}}
			if testCase.mutate != nil {
				testCase.mutate(cluster, pool)
			}
			if err := nativeRecoveryFixture(cluster, []hyperv1.NodePool{*pool}, "native-etcd-recovery"); (err != nil) != testCase.wantError {
				t.Fatalf("fixture validation error=%v", err)
			}
		})
	}
}

func TestNativeEtcdConverged(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		replicas  *int32
		ready     int32
		wantError bool
	}{
		{name: "When all three HA replicas are ready, it should converge", replicas: ptr.To[int32](3), ready: 3},
		{name: "When a replica is missing, it should reject partial readiness", replicas: ptr.To[int32](3), ready: 2, wantError: true},
		{name: "When desired replicas are absent, it should reject vacuous convergence", ready: 0, wantError: true},
		{name: "When replicas are zero, it should reject vacuous convergence", replicas: ptr.To[int32](0), wantError: true},
		{name: "When single replica etcd is ready, it should reject non-HA convergence", replicas: ptr.To[int32](1), ready: 1, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			sts := &appsv1.StatefulSet{Spec: appsv1.StatefulSetSpec{Replicas: testCase.replicas}, Status: appsv1.StatefulSetStatus{ReadyReplicas: testCase.ready}}
			if err := nativeEtcdConverged(sts); (err != nil) != testCase.wantError {
				t.Fatalf("convergence error=%v", err)
			}
		})
	}
}

func TestNativeEtcdReplaced(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		mutate    func(*[]corev1.Pod)
		wantError bool
	}{
		{name: "When every original member has a new UID, it should confirm replacement"},
		{name: "When no pods are returned, it should reject vacuous success", mutate: func(pods *[]corev1.Pod) { *pods = nil }, wantError: true},
		{name: "When a member is missing, it should reject incomplete replacement", mutate: func(pods *[]corev1.Pod) { *pods = (*pods)[:2] }, wantError: true},
		{name: "When an original UID survives, it should reject incomplete disruption", mutate: func(pods *[]corev1.Pod) { (*pods)[0].UID = "old" }, wantError: true},
		{name: "When a replacement has an empty UID, it should reject an invalid pod", mutate: func(pods *[]corev1.Pod) { (*pods)[0].UID = "" }, wantError: true},
		{name: "When a replacement has another name, it should reject the wrong member", mutate: func(pods *[]corev1.Pod) { (*pods)[0].Name = "other" }, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			before := []corev1.Pod{}
			for _, name := range []string{"etcd-0", "etcd-1", "etcd-2"} {
				before = append(before, corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "control-plane", UID: "old"}})
			}
			after := append([]corev1.Pod{}, before...)
			for index := range after {
				after[index].UID = "new"
			}
			if testCase.mutate != nil {
				testCase.mutate(&after)
			}
			if err := nativeEtcdReplaced(before, after); (err != nil) != testCase.wantError {
				t.Fatalf("replacement error=%v", err)
			}
		})
	}
}

func TestNativeMarkerSurvived(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		actual    map[string]string
		wantError bool
	}{
		{name: "When marker data is unchanged, it should confirm persistence", actual: map[string]string{"value": "recorded-timestamp"}},
		{name: "When marker data changes, it should reject data loss", actual: map[string]string{"value": "different-timestamp"}, wantError: true},
		{name: "When marker data is missing, it should reject data loss", wantError: true},
		{name: "When marker data gains unexpected keys, it should reject unequal data", actual: map[string]string{"value": "recorded-timestamp", "extra": "data"}, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := nativeMarkerSurvived(&corev1.ConfigMap{Data: map[string]string{"value": "recorded-timestamp"}}, &corev1.ConfigMap{Data: testCase.actual}); (err != nil) != testCase.wantError {
				t.Fatalf("marker error=%v", err)
			}
		})
	}
}

func TestNativeEtcdCommandResult(t *testing.T) {
	for _, testCase := range []struct {
		name, output, forbidden string
		commandError            error
		wantError               bool
	}{
		{name: "When a command succeeds without diagnostics, it should permit recovery checks", forbidden: "Error:"},
		{name: "When corruption reports a missing file, it should reject ineffective corruption", output: "No such file or directory", forbidden: "No such file or directory", wantError: true},
		{name: "When removal returns an Error diagnostic, it should reject failed member removal", output: "Error: unavailable", forbidden: "Error:", wantError: true},
		{name: "When pod execution fails, it should preserve the execution error", forbidden: "Error:", commandError: errors.New("exec failed"), wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := nativeEtcdCommandResult(testCase.output, testCase.commandError, testCase.forbidden); (err != nil) != testCase.wantError {
				t.Fatalf("command result error=%v", err)
			}
		})
	}
}

func TestNativeEtcdMemberID(t *testing.T) {
	for _, testCase := range []struct {
		name, output, expected string
		wantError              bool
	}{
		{name: "When discovery returns a member ID, it should trim whitespace", output: "  a12bc34\n", expected: "a12bc34"},
		{name: "When discovery returns no ID, it should reject a no-op removal", wantError: true},
		{name: "When discovery returns only whitespace, it should reject a no-op removal", output: " \n", wantError: true},
		{name: "When discovery returns multiple IDs, it should reject ambiguous removal", output: "abc\ndef", wantError: true},
		{name: "When discovery returns an error message, it should reject it", output: "Error: unavailable", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			actual, err := nativeEtcdMemberID(testCase.output)
			if (err != nil) != testCase.wantError || actual != testCase.expected {
				t.Fatalf("ID=%q error=%v", actual, err)
			}
		})
	}
}

func TestNativeCapturePullSecret(t *testing.T) {
	for _, testCase := range []struct {
		name                                      string
		wrongName, wrongNamespace, shared, absent bool
		wantError                                 bool
	}{
		{name: "When the referenced Secret is isolated, it should snapshot bytes and owned keys"},
		{name: "When the key was absent, it should snapshot absence for exact restoration", absent: true},
		{name: "When the Secret is not referenced, it should refuse mutation", wrongName: true, wantError: true},
		{name: "When the Secret is in another namespace, it should refuse mutation", wrongNamespace: true, wantError: true},
		{name: "When another cluster shares the Secret, it should refuse mutation", shared: true, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "recovery", Namespace: "clusters", UID: "cluster-uid", Annotations: map[string]string{hyperv1.RequestServingNodeAdditionalSelectorAnnotation: "original-annotation"}}, Spec: hyperv1.HostedClusterSpec{PullSecret: corev1.LocalObjectReference{Name: "pull-secret"}, NodeSelector: map[string]string{nativeRecoverySelector: "original-selector"}}}
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "clusters", UID: "secret-uid"}, Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("synthetic-registry-config")}}
			if testCase.wrongName {
				secret.Name = "other"
			}
			if testCase.wrongNamespace {
				secret.Namespace = "other"
			}
			if testCase.absent {
				secret.Data = nil
			}
			clusters := []hyperv1.HostedCluster{*cluster}
			if testCase.shared {
				other := cluster.DeepCopy()
				other.Name = "another-cluster"
				clusters = append(clusters, *other)
			}
			snapshot, err := nativeCapturePullSecret(cluster, secret, clusters)
			if (err != nil) != testCase.wantError {
				t.Fatalf("snapshot error=%v", err)
			}
			if err != nil {
				return
			}
			if snapshot.hadDockerConfig == testCase.absent || snapshot.selector != "original-selector" || !snapshot.hadSelector || snapshot.annotation != "original-annotation" || !snapshot.hadAnnotation {
				t.Fatal("snapshot did not retain owned key values and presence")
			}
			if !testCase.absent {
				secret.Data[corev1.DockerConfigJsonKey][0] = 'X'
				if string(snapshot.dockerConfig) != "synthetic-registry-config" {
					t.Fatal("snapshot aliases mutable Secret bytes")
				}
			}
		})
	}
}

func TestNativeRemovePullSecretKey(t *testing.T) {
	for _, testCase := range []struct {
		name                                     string
		missing, replaced, updateError, conflict bool
		wantError                                bool
	}{
		{name: "When the referenced Secret is valid, it should remove only dockerconfigjson"},
		{name: "When the Secret is missing, it should preserve the read error", missing: true, wantError: true},
		{name: "When the Secret was replaced, it should refuse mutation", replaced: true, wantError: true},
		{name: "When the update fails, it should preserve the error", updateError: true, wantError: true},
		{name: "When the first update conflicts, it should retry without dropping unrelated keys", conflict: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "clusters", UID: "secret-uid"}, Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte("synthetic-registry-config"), "unrelated": []byte("retain")}}
			snapshot := nativePullSecretSnapshot{secretKey: crclient.ObjectKeyFromObject(secret), secretUID: secret.UID}
			if testCase.replaced {
				secret.UID = "replacement"
			}
			objects := []crclient.Object{}
			if !testCase.missing {
				objects = append(objects, secret)
			}
			attempts := 0
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
				Update: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, opts ...crclient.UpdateOption) error {
					attempts++
					if testCase.updateError {
						return errors.New("synthetic update failure")
					}
					if testCase.conflict && attempts == 1 {
						return apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, object.GetName(), errors.New("synthetic conflict"))
					}
					return client.Update(ctx, object, opts...)
				},
			}).Build()
			err := nativeRemovePullSecretKey(t.Context(), client, snapshot)
			if (err != nil) != testCase.wantError {
				t.Fatalf("mutation error=%v", err)
			}
			if err != nil {
				if !testCase.missing {
					actual := &corev1.Secret{}
					if readErr := client.Get(t.Context(), snapshot.secretKey, actual); readErr != nil {
						t.Fatal(readErr)
					}
					if string(actual.Data[corev1.DockerConfigJsonKey]) != "synthetic-registry-config" || string(actual.Data["unrelated"]) != "retain" {
						t.Fatal("failed mutation changed Secret data")
					}
				}
				return
			}
			actual := &corev1.Secret{}
			if err := client.Get(t.Context(), snapshot.secretKey, actual); err != nil {
				t.Fatal(err)
			}
			if _, present := actual.Data[corev1.DockerConfigJsonKey]; present || string(actual.Data["unrelated"]) != "retain" {
				t.Fatal("mutation removed the wrong keys")
			}
			if testCase.conflict && attempts != 2 {
				t.Fatalf("conflict attempts=%d", attempts)
			}
		})
	}
}

func TestNativeMutateRecoverySelectors(t *testing.T) {
	for _, testCase := range []struct {
		name                                    string
		nilMaps, missing, replaced, updateError bool
		wantError                               bool
	}{
		{name: "When selectors exist, it should mutate only owned keys"},
		{name: "When selectors are absent, it should initialize both maps", nilMaps: true},
		{name: "When the HostedCluster is missing, it should preserve the read error", missing: true, wantError: true},
		{name: "When the HostedCluster was replaced, it should refuse mutation", replaced: true, wantError: true},
		{name: "When the update fails, it should preserve the error", updateError: true, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "recovery", Namespace: "clusters", UID: "cluster-uid", Annotations: map[string]string{"unrelated": "retain"}}, Spec: hyperv1.HostedClusterSpec{NodeSelector: map[string]string{"unrelated": "retain"}}}
			snapshot := nativePullSecretSnapshot{clusterKey: crclient.ObjectKeyFromObject(cluster), clusterUID: cluster.UID}
			if testCase.replaced {
				cluster.UID = "replacement"
			}
			if testCase.nilMaps {
				cluster.Spec.NodeSelector = nil
				cluster.Annotations = nil
			}
			objects := []crclient.Object{}
			if !testCase.missing {
				objects = append(objects, cluster)
			}
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
				Update: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, opts ...crclient.UpdateOption) error {
					if testCase.updateError {
						return errors.New("synthetic update failure")
					}
					return client.Update(ctx, object, opts...)
				},
			}).Build()
			err := nativeMutateRecoverySelectors(t.Context(), client, snapshot)
			if (err != nil) != testCase.wantError {
				t.Fatalf("selector mutation error=%v", err)
			}
			if err != nil {
				if !testCase.missing {
					actual := &hyperv1.HostedCluster{}
					if readErr := client.Get(t.Context(), snapshot.clusterKey, actual); readErr != nil {
						t.Fatal(readErr)
					}
					if _, present := actual.Spec.NodeSelector[nativeRecoverySelector]; present {
						t.Fatal("failed mutation changed the cluster selector")
					}
					if _, present := actual.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation]; present {
						t.Fatal("failed mutation changed the cluster annotation")
					}
				}
				return
			}
			actual := &hyperv1.HostedCluster{}
			if err := client.Get(t.Context(), snapshot.clusterKey, actual); err != nil {
				t.Fatal(err)
			}
			if actual.Spec.NodeSelector[nativeRecoverySelector] != "true" || actual.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation] != nativeRecoveryAdditionalSelector {
				t.Fatal("exact selector mutations were not applied")
			}
			if !testCase.nilMaps && (actual.Spec.NodeSelector["unrelated"] != "retain" || actual.Annotations["unrelated"] != "retain") {
				t.Fatal("unrelated selector or annotation lost")
			}
		})
	}
}

func TestNativeRecoverySelectorsPropagated(t *testing.T) {
	for _, testCase := range []struct {
		name, selector, annotation string
		wantError                  bool
	}{
		{name: "When both exact selector values propagate, it should accept recovery", selector: "true", annotation: nativeRecoveryAdditionalSelector},
		{name: "When only the annotation propagates, it should reject partial recovery", annotation: nativeRecoveryAdditionalSelector, wantError: true},
		{name: "When only the selector propagates, it should reject partial recovery", selector: "true", wantError: true},
		{name: "When the annotation differs, it should reject incorrect propagation", selector: "true", annotation: `{"other":"true"}`, wantError: true},
		{name: "When neither value propagates, it should reject a no-op reconcile", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{hyperv1.RequestServingNodeAdditionalSelectorAnnotation: testCase.annotation}}, Spec: hyperv1.HostedControlPlaneSpec{NodeSelector: map[string]string{nativeRecoverySelector: testCase.selector}}}
			if err := nativeRecoverySelectorsPropagated(hcp); (err != nil) != testCase.wantError {
				t.Fatalf("propagation error=%v", err)
			}
		})
	}
}

func TestNativeRestorePullSecret(t *testing.T) {
	for _, testCase := range []struct {
		name                                                                                                string
		absent, empty, nilMaps, missingSecret, replacementSecret, replacementCluster, updateError, canceled bool
		wantError                                                                                           bool
	}{
		{name: "When owned keys existed, it should restore exact bytes and values while retaining unrelated keys"},
		{name: "When owned keys were absent, it should restore absence rather than invent values", absent: true},
		{name: "When owned keys were present but empty, it should restore their presence", empty: true},
		{name: "When the maps became nil, it should recreate only the original owned keys", nilMaps: true},
		{name: "When the Secret disappeared, it should still restore cluster keys and report the error", missingSecret: true, wantError: true},
		{name: "When the Secret was replaced, it should not overwrite it but still restore cluster keys", replacementSecret: true, wantError: true},
		{name: "When the cluster was replaced, it should not overwrite it but still restore Secret bytes", replacementCluster: true, wantError: true},
		{name: "When the Secret update fails, it should still restore cluster keys and report the error", updateError: true, wantError: true},
		{name: "When cleanup context is canceled, it should propagate the error", canceled: true, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "recovery", Namespace: "clusters", UID: "cluster-uid", Annotations: map[string]string{hyperv1.RequestServingNodeAdditionalSelectorAnnotation: nativeRecoveryAdditionalSelector, "unrelated": "retain"}}, Spec: hyperv1.HostedClusterSpec{NodeSelector: map[string]string{nativeRecoverySelector: "true", "unrelated": "retain"}}}
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "clusters", UID: "secret-uid"}, Data: map[string][]byte{"unrelated": []byte("retain")}}
			snapshot := nativePullSecretSnapshot{clusterKey: crclient.ObjectKeyFromObject(cluster), clusterUID: cluster.UID, secretKey: crclient.ObjectKeyFromObject(secret), secretUID: secret.UID, dockerConfig: []byte("synthetic-original-config"), hadDockerConfig: !testCase.absent, selector: "original", hadSelector: !testCase.absent, annotation: "original-annotation", hadAnnotation: !testCase.absent}
			if testCase.absent {
				secret.Data[corev1.DockerConfigJsonKey] = []byte("transient-value")
			}
			if testCase.empty {
				snapshot.dockerConfig = []byte{}
				snapshot.selector = ""
				snapshot.annotation = ""
			}
			if testCase.nilMaps {
				secret.Data = nil
				cluster.Spec.NodeSelector = nil
				cluster.Annotations = nil
			}
			if testCase.replacementSecret {
				secret.UID = "replacement"
			}
			if testCase.replacementCluster {
				cluster.UID = "replacement"
			}
			objects := []crclient.Object{cluster}
			if !testCase.missingSecret {
				objects = append(objects, secret)
			}
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
					if err := ctx.Err(); err != nil {
						return err
					}
					return client.Get(ctx, key, object, opts...)
				},
				Update: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, opts ...crclient.UpdateOption) error {
					if _, isSecret := object.(*corev1.Secret); isSecret && testCase.updateError {
						return errors.New("synthetic Secret update failure")
					}
					return client.Update(ctx, object, opts...)
				},
			}).Build()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if testCase.canceled {
				cancel()
			}
			err := nativeRestorePullSecret(ctx, client, snapshot)
			if (err != nil) != testCase.wantError {
				t.Fatalf("restore error=%v", err)
			}
			if testCase.canceled {
				if !strings.Contains(err.Error(), "context canceled") {
					t.Fatalf("missing cancellation: %v", err)
				}
				return
			}
			actualCluster := &hyperv1.HostedCluster{}
			if err := client.Get(t.Context(), snapshot.clusterKey, actualCluster); err != nil {
				t.Fatal(err)
			}
			assertNativeRecoveryClusterRestored(t, actualCluster, snapshot, testCase.absent, testCase.replacementCluster, testCase.nilMaps)
			if testCase.missingSecret {
				return
			}
			actualSecret := &corev1.Secret{}
			if err := client.Get(t.Context(), snapshot.secretKey, actualSecret); err != nil {
				t.Fatal(err)
			}
			if !testCase.replacementSecret && !testCase.updateError {
				value, present := actualSecret.Data[corev1.DockerConfigJsonKey]
				if present != snapshot.hadDockerConfig || (present && !bytes.Equal(value, snapshot.dockerConfig)) {
					t.Fatal("Secret bytes or key presence were not restored")
				}
			} else if _, present := actualSecret.Data[corev1.DockerConfigJsonKey]; present {
				t.Fatal("failed or refused Secret restoration changed the key")
			}
			if !testCase.nilMaps && string(actualSecret.Data["unrelated"]) != "retain" {
				t.Fatal("unrelated Secret data was lost")
			}
		})
	}
}

func assertNativeRecoveryClusterRestored(t *testing.T, actualCluster *hyperv1.HostedCluster, snapshot nativePullSecretSnapshot, absent, replacementCluster, nilMaps bool) {
	t.Helper()
	if !replacementCluster {
		selector, hadSelector := actualCluster.Spec.NodeSelector[nativeRecoverySelector]
		annotation, hadAnnotation := actualCluster.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation]
		if hadSelector != snapshot.hadSelector || hadAnnotation != snapshot.hadAnnotation || (!absent && (selector != snapshot.selector || annotation != snapshot.annotation)) {
			t.Fatal("owned cluster keys were not restored after cleanup")
		}
	} else if actualCluster.Spec.NodeSelector[nativeRecoverySelector] != "true" || actualCluster.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation] != nativeRecoveryAdditionalSelector {
		t.Fatal("cleanup overwrote a replacement HostedCluster")
	}
	if !nilMaps && actualCluster.Spec.NodeSelector["unrelated"] != "retain" {
		t.Fatal("unrelated cluster key was lost")
	}
}

func TestNativeRestorePullSecretBounded(t *testing.T) {
	t.Run("When the scenario context has been canceled, it should restore with a detached two-minute deadline", func(t *testing.T) {
		cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "recovery", Namespace: "clusters", UID: "cluster-uid"}}
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "clusters", UID: "secret-uid"}}
		snapshot := nativePullSecretSnapshot{clusterKey: crclient.ObjectKeyFromObject(cluster), clusterUID: cluster.UID, secretKey: crclient.ObjectKeyFromObject(secret), secretUID: secret.UID, dockerConfig: []byte("synthetic-original-config"), hadDockerConfig: true}
		checked := 0
		client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(cluster, secret).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, object crclient.Object, opts ...crclient.GetOption) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				deadline, bounded := ctx.Deadline()
				if !bounded || time.Until(deadline) <= 0 || time.Until(deadline) > 2*time.Minute {
					t.Fatal("cleanup context did not preserve the two-minute bound")
				}
				checked++
				return client.Get(ctx, key, object, opts...)
			},
		}).Build()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := nativeRestorePullSecretBounded(ctx, client, snapshot); err != nil {
			t.Fatal(err)
		}
		if checked != 2 {
			t.Fatalf("cleanup performed %d ownership reads, want both Secret and HostedCluster", checked)
		}
	})
}
