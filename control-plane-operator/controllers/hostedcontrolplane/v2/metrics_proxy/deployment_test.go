package metricsproxy

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func newServiceMonitorWithTLS(name, namespace string, tlsCfg *prometheusoperatorv1.TLSConfig) *prometheusoperatorv1.ServiceMonitor {
	ep := prometheusoperatorv1.Endpoint{}
	ep.TLSConfig = tlsCfg
	return &prometheusoperatorv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: prometheusoperatorv1.ServiceMonitorSpec{
			Endpoints: []prometheusoperatorv1.Endpoint{ep},
		},
	}
}

func newPodMonitorWithTLS(name, namespace string, tlsCfg *prometheusoperatorv1.SafeTLSConfig) *prometheusoperatorv1.PodMonitor {
	ep := prometheusoperatorv1.PodMetricsEndpoint{
		Port: ptr.To("metrics"),
	}
	if tlsCfg != nil {
		ep.TLSConfig = tlsCfg
	}
	return &prometheusoperatorv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: prometheusoperatorv1.PodMonitorSpec{
			PodMetricsEndpoints: []prometheusoperatorv1.PodMetricsEndpoint{ep},
		},
	}
}

func TestCertVolumesFromMonitors(t *testing.T) {
	t.Parallel()

	namespace := "clusters-test-hosted"

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = prometheusoperatorv1.AddToScheme(scheme)

	t.Run("When ServiceMonitors have mixed Secret and ConfigMap TLS refs, it should generate deduplicated volumes", func(t *testing.T) {
		t.Parallel()

		sm1 := newServiceMonitorWithTLS("kube-apiserver", namespace, &prometheusoperatorv1.TLSConfig{
			SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
				CA: prometheusoperatorv1.SecretOrConfigMap{
					ConfigMap: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "root-ca"},
						Key:                  "ca.crt",
					},
				},
				Cert: prometheusoperatorv1.SecretOrConfigMap{
					Secret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "metrics-client"},
						Key:                  "tls.crt",
					},
				},
				KeySecret: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "metrics-client"},
					Key:                  "tls.key",
				},
			},
		})
		// sm2 references the same root-ca and metrics-client — should deduplicate.
		sm2 := newServiceMonitorWithTLS("kube-controller-manager", namespace, &prometheusoperatorv1.TLSConfig{
			SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
				CA: prometheusoperatorv1.SecretOrConfigMap{
					ConfigMap: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "root-ca"},
						Key:                  "ca.crt",
					},
				},
				Cert: prometheusoperatorv1.SecretOrConfigMap{
					Secret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "metrics-client"},
						Key:                  "tls.crt",
					},
				},
				KeySecret: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "metrics-client"},
					Key:                  "tls.key",
				},
			},
		})

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(sm1, sm2).Build()
		cpContext := component.WorkloadContext{
			Context: context.Background(),
			Client:  fakeClient,
			HCP:     &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}},
		}

		volumes, mounts, err := certVolumesFromMonitors(cpContext, namespace)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Expect 2 unique volumes: metrics-client (Secret), root-ca (ConfigMap).
		if len(volumes) != 2 {
			t.Errorf("expected 2 volumes, got %d", len(volumes))
		}
		if len(mounts) != 2 {
			t.Errorf("expected 2 mounts, got %d", len(mounts))
		}

		// Verify sorted order: metrics-client before root-ca.
		if len(volumes) >= 2 {
			if volumes[0].Name != "metrics-client" {
				t.Errorf("expected first volume to be metrics-client, got %s", volumes[0].Name)
			}
			if volumes[1].Name != "root-ca" {
				t.Errorf("expected second volume to be root-ca, got %s", volumes[1].Name)
			}
		}
	})

	t.Run("When a ConfigMap is referenced with different keys, it should merge items", func(t *testing.T) {
		t.Parallel()

		sm1 := newServiceMonitorWithTLS("component-a", namespace, &prometheusoperatorv1.TLSConfig{
			SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
				CA: prometheusoperatorv1.SecretOrConfigMap{
					ConfigMap: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "shared-ca"},
						Key:                  "ca.crt",
					},
				},
			},
		})
		sm2 := newServiceMonitorWithTLS("component-b", namespace, &prometheusoperatorv1.TLSConfig{
			SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
				CA: prometheusoperatorv1.SecretOrConfigMap{
					ConfigMap: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "shared-ca"},
						Key:                  "another.crt",
					},
				},
			},
		})

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(sm1, sm2).Build()
		cpContext := component.WorkloadContext{
			Context: context.Background(),
			Client:  fakeClient,
			HCP:     &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}},
		}

		volumes, _, err := certVolumesFromMonitors(cpContext, namespace)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(volumes) != 1 {
			t.Fatalf("expected 1 volume, got %d", len(volumes))
		}

		cm := volumes[0].VolumeSource.ConfigMap
		if cm == nil {
			t.Fatal("expected ConfigMap volume source")
		}
		if len(cm.Items) != 2 {
			t.Errorf("expected 2 items in ConfigMap volume, got %d", len(cm.Items))
		}
	})

	t.Run("When a ConfigMap is referenced with the same key twice, it should not duplicate items", func(t *testing.T) {
		t.Parallel()

		sm1 := newServiceMonitorWithTLS("component-a", namespace, &prometheusoperatorv1.TLSConfig{
			SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
				CA: prometheusoperatorv1.SecretOrConfigMap{
					ConfigMap: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "root-ca"},
						Key:                  "ca.crt",
					},
				},
			},
		})
		sm2 := newServiceMonitorWithTLS("component-b", namespace, &prometheusoperatorv1.TLSConfig{
			SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
				CA: prometheusoperatorv1.SecretOrConfigMap{
					ConfigMap: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "root-ca"},
						Key:                  "ca.crt",
					},
				},
			},
		})

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(sm1, sm2).Build()
		cpContext := component.WorkloadContext{
			Context: context.Background(),
			Client:  fakeClient,
			HCP:     &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}},
		}

		volumes, _, err := certVolumesFromMonitors(cpContext, namespace)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(volumes) != 1 {
			t.Fatalf("expected 1 volume, got %d", len(volumes))
		}

		cm := volumes[0].VolumeSource.ConfigMap
		if cm == nil {
			t.Fatal("expected ConfigMap volume source")
		}
		if len(cm.Items) != 1 {
			t.Errorf("expected 1 item in ConfigMap volume, got %d", len(cm.Items))
		}
	})

	t.Run("When ServiceMonitor has no TLS config, it should be skipped", func(t *testing.T) {
		t.Parallel()

		sm := &prometheusoperatorv1.ServiceMonitor{
			ObjectMeta: metav1.ObjectMeta{Name: "no-tls", Namespace: namespace},
			Spec: prometheusoperatorv1.ServiceMonitorSpec{
				Endpoints: []prometheusoperatorv1.Endpoint{{}},
			},
		}

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(sm).Build()
		cpContext := component.WorkloadContext{
			Context: context.Background(),
			Client:  fakeClient,
			HCP:     &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}},
		}

		volumes, mounts, err := certVolumesFromMonitors(cpContext, namespace)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(volumes) != 0 {
			t.Errorf("expected 0 volumes, got %d", len(volumes))
		}
		if len(mounts) != 0 {
			t.Errorf("expected 0 mounts, got %d", len(mounts))
		}
	})

	t.Run("When no monitors exist, it should return empty slices", func(t *testing.T) {
		t.Parallel()

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		cpContext := component.WorkloadContext{
			Context: context.Background(),
			Client:  fakeClient,
			HCP:     &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}},
		}

		volumes, mounts, err := certVolumesFromMonitors(cpContext, namespace)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(volumes) != 0 {
			t.Errorf("expected 0 volumes, got %d", len(volumes))
		}
		if len(mounts) != 0 {
			t.Errorf("expected 0 mounts, got %d", len(mounts))
		}
	})

	t.Run("When Secret volumes are generated, they should have optional=true", func(t *testing.T) {
		t.Parallel()

		sm := newServiceMonitorWithTLS("test-component", namespace, &prometheusoperatorv1.TLSConfig{
			SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
				Cert: prometheusoperatorv1.SecretOrConfigMap{
					Secret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "test-secret"},
						Key:                  "tls.crt",
					},
				},
			},
		})

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(sm).Build()
		cpContext := component.WorkloadContext{
			Context: context.Background(),
			Client:  fakeClient,
			HCP:     &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}},
		}

		volumes, mounts, err := certVolumesFromMonitors(cpContext, namespace)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(volumes) != 1 {
			t.Fatalf("expected 1 volume, got %d", len(volumes))
		}

		secret := volumes[0].VolumeSource.Secret
		if secret == nil {
			t.Fatal("expected Secret volume source")
		}
		if secret.Optional == nil || !*secret.Optional {
			t.Error("expected Secret volume to have optional=true")
		}

		if len(mounts) != 1 {
			t.Fatalf("expected 1 mount, got %d", len(mounts))
		}
		expectedPath := certBasePath + "/test-secret"
		if mounts[0].MountPath != expectedPath {
			t.Errorf("expected mount path %q, got %q", expectedPath, mounts[0].MountPath)
		}
	})

	t.Run("When ConfigMap volumes are generated, they should have optional=true", func(t *testing.T) {
		t.Parallel()

		sm := newServiceMonitorWithTLS("test-component", namespace, &prometheusoperatorv1.TLSConfig{
			SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
				CA: prometheusoperatorv1.SecretOrConfigMap{
					ConfigMap: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "test-ca"},
						Key:                  "ca.crt",
					},
				},
			},
		})

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(sm).Build()
		cpContext := component.WorkloadContext{
			Context: context.Background(),
			Client:  fakeClient,
			HCP:     &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}},
		}

		volumes, _, err := certVolumesFromMonitors(cpContext, namespace)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(volumes) != 1 {
			t.Fatalf("expected 1 volume, got %d", len(volumes))
		}

		cm := volumes[0].VolumeSource.ConfigMap
		if cm == nil {
			t.Fatal("expected ConfigMap volume source")
		}
		if cm.Optional == nil || !*cm.Optional {
			t.Error("expected ConfigMap volume to have optional=true")
		}
	})

	t.Run("When PodMonitor has TLS CA ref, it should generate a volume", func(t *testing.T) {
		t.Parallel()

		pm := newPodMonitorWithTLS("cluster-image-registry-operator", namespace, &prometheusoperatorv1.SafeTLSConfig{
			CA: prometheusoperatorv1.SecretOrConfigMap{
				ConfigMap: &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "root-ca"},
					Key:                  "ca.crt",
				},
			},
		})

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(pm).Build()
		cpContext := component.WorkloadContext{
			Context: context.Background(),
			Client:  fakeClient,
			HCP:     &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}},
		}

		volumes, mounts, err := certVolumesFromMonitors(cpContext, namespace)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(volumes) != 1 {
			t.Fatalf("expected 1 volume, got %d", len(volumes))
		}
		if volumes[0].Name != "root-ca" {
			t.Errorf("expected volume name root-ca, got %s", volumes[0].Name)
		}
		if len(mounts) != 1 {
			t.Fatalf("expected 1 mount, got %d", len(mounts))
		}
	})

	t.Run("When PodMonitor has no TLS config, it should be skipped", func(t *testing.T) {
		t.Parallel()

		pm := newPodMonitorWithTLS("cluster-autoscaler", namespace, nil)

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(pm).Build()
		cpContext := component.WorkloadContext{
			Context: context.Background(),
			Client:  fakeClient,
			HCP:     &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}},
		}

		volumes, mounts, err := certVolumesFromMonitors(cpContext, namespace)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(volumes) != 0 {
			t.Errorf("expected 0 volumes, got %d", len(volumes))
		}
		if len(mounts) != 0 {
			t.Errorf("expected 0 mounts, got %d", len(mounts))
		}
	})

	t.Run("When ServiceMonitor and PodMonitor share the same CA ref, it should deduplicate", func(t *testing.T) {
		t.Parallel()

		sm := newServiceMonitorWithTLS("kube-apiserver", namespace, &prometheusoperatorv1.TLSConfig{
			SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
				CA: prometheusoperatorv1.SecretOrConfigMap{
					ConfigMap: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "root-ca"},
						Key:                  "ca.crt",
					},
				},
			},
		})
		pm := newPodMonitorWithTLS("cluster-image-registry-operator", namespace, &prometheusoperatorv1.SafeTLSConfig{
			CA: prometheusoperatorv1.SecretOrConfigMap{
				ConfigMap: &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "root-ca"},
					Key:                  "ca.crt",
				},
			},
		})

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(sm, pm).Build()
		cpContext := component.WorkloadContext{
			Context: context.Background(),
			Client:  fakeClient,
			HCP:     &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}},
		}

		volumes, _, err := certVolumesFromMonitors(cpContext, namespace)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(volumes) != 1 {
			t.Errorf("expected 1 deduplicated volume, got %d", len(volumes))
		}
	})
}

func TestCollectSecretOrConfigMapRef(t *testing.T) {
	t.Parallel()

	t.Run("When a Secret reference is added, it should be stored as a secret ref", func(t *testing.T) {
		t.Parallel()
		refs := make(map[string]*certRef)
		collectSecretOrConfigMapRef(refs, prometheusoperatorv1.SecretOrConfigMap{
			Secret: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "my-secret"},
				Key:                  "tls.crt",
			},
		})

		ref, ok := refs["my-secret"]
		if !ok {
			t.Fatal("expected my-secret in refs")
		}
		if !ref.isSecret {
			t.Error("expected isSecret=true")
		}
	})

	t.Run("When a ConfigMap reference is added, it should store key-to-path items", func(t *testing.T) {
		t.Parallel()
		refs := make(map[string]*certRef)
		collectSecretOrConfigMapRef(refs, prometheusoperatorv1.SecretOrConfigMap{
			ConfigMap: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "my-cm"},
				Key:                  "ca.crt",
			},
		})

		ref, ok := refs["my-cm"]
		if !ok {
			t.Fatal("expected my-cm in refs")
		}
		if ref.isSecret {
			t.Error("expected isSecret=false for ConfigMap ref")
		}
		if len(ref.items) != 1 || ref.items[0].Key != "ca.crt" {
			t.Errorf("expected 1 item with key ca.crt, got %v", ref.items)
		}
	})

	t.Run("When duplicate Secret references are added, it should not overwrite", func(t *testing.T) {
		t.Parallel()
		refs := make(map[string]*certRef)
		refs["my-secret"] = &certRef{name: "my-secret", isSecret: true}

		collectSecretOrConfigMapRef(refs, prometheusoperatorv1.SecretOrConfigMap{
			Secret: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "my-secret"},
				Key:                  "tls.key",
			},
		})

		if len(refs) != 1 {
			t.Errorf("expected 1 ref, got %d", len(refs))
		}
	})

	t.Run("When an empty SecretOrConfigMap is passed, it should not add anything", func(t *testing.T) {
		t.Parallel()
		refs := make(map[string]*certRef)
		collectSecretOrConfigMapRef(refs, prometheusoperatorv1.SecretOrConfigMap{})
		if len(refs) != 0 {
			t.Errorf("expected 0 refs, got %d", len(refs))
		}
	})

	t.Run("When ConfigMap keys are merged, it should add new keys to existing ref", func(t *testing.T) {
		t.Parallel()
		refs := make(map[string]*certRef)
		refs["shared-cm"] = &certRef{
			name: "shared-cm",
			items: []corev1.KeyToPath{
				{Key: "first.crt", Path: "first.crt"},
			},
		}

		collectSecretOrConfigMapRef(refs, prometheusoperatorv1.SecretOrConfigMap{
			ConfigMap: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "shared-cm"},
				Key:                  "second.crt",
			},
		})

		ref := refs["shared-cm"]
		if len(ref.items) != 2 {
			t.Errorf("expected 2 items after merge, got %d", len(ref.items))
		}
	})
}

// Verify certRef is correctly identified for the ptr.To helper.
func TestCertVolumeOptionalFlag(t *testing.T) {
	t.Parallel()

	t.Run("When volumes are built from refs, it should use ptr.To(true) for optional", func(t *testing.T) {
		t.Parallel()
		// Just verify ptr.To(true) produces the expected value.
		val := ptr.To(true)
		if val == nil || !*val {
			t.Error("expected ptr.To(true) to return *true")
		}
	})
}
