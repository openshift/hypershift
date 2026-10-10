//go:build e2ev2 && backuprestore

package backuprestore

import (
	"context"
	"testing"

	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetReadyHypershiftPluginImage(t *testing.T) {
	readyPod := func(name string, image string, ready bool) corev1.Pod {
		status := corev1.ConditionFalse
		if ready {
			status = corev1.ConditionTrue
		}
		return corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: DefaultOADPNamespace,
				Labels: map[string]string{"deploy": "velero", "component": "velero"},
			},
			Spec: corev1.PodSpec{InitContainers: []corev1.Container{{
				Name: "hypershift-oadp-plugin", Image: image,
			}}},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}},
			},
		}
	}

	tests := []struct {
		name      string
		pods      []corev1.Pod
		wantImage string
	}{
		{
			name:      "When a ready Velero pod loaded the HyperShift plugin, it should return its image",
			pods:      []corev1.Pod{readyPod("velero-ready", "quay.io/oadp/hypershift-oadp-plugin:v1", true)},
			wantImage: "quay.io/oadp/hypershift-oadp-plugin:v1",
		},
		{
			name: "When standalone Velero names the init container from its image path, it should return the image",
			pods: func() []corev1.Pod {
				pod := readyPod("velero-standalone", "quay.io/konveyor/hypershift-oadp-plugin:v1", true)
				pod.Spec.InitContainers[0].Name = "konveyor-hypershift-oadp-plugin"
				return []corev1.Pod{pod}
			}(),
			wantImage: "quay.io/konveyor/hypershift-oadp-plugin:v1",
		},
		{
			name: "When an unready Velero pod has the plugin, it should fail",
			pods: []corev1.Pod{readyPod("velero-unready", "quay.io/oadp/hypershift-oadp-plugin:v1", false)},
		},
		{
			name: "When the only plugin pod is terminating, it should fail",
			pods: func() []corev1.Pod {
				pod := readyPod("velero-terminating", "quay.io/oadp/hypershift-oadp-plugin:v1", true)
				now := metav1.Now()
				pod.DeletionTimestamp = &now
				pod.Finalizers = []string{"test.cleanup/finalizer"}
				return []corev1.Pod{pod}
			}(),
		},
		{
			name: "When a ready Velero pod has no HyperShift plugin, it should fail",
			pods: func() []corev1.Pod {
				pod := readyPod("velero-no-plugin", "", true)
				pod.Spec.InitContainers = nil
				return []corev1.Pod{pod}
			}(),
		},
		{
			name: "When no Velero pod exists, it should fail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			objects := make([]crclient.Object, 0, len(tt.pods))
			for i := range tt.pods {
				objects = append(objects, &tt.pods[i])
			}
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			testCtx := &internal.TestContext{Context: context.Background(), MgmtClient: client}
			image, err := GetReadyHypershiftPluginImage(testCtx)
			if tt.wantImage == "" {
				if err == nil {
					t.Fatalf("expected missing plugin error, got image %q", image)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if image != tt.wantImage {
				t.Fatalf("got plugin image %q, want %q", image, tt.wantImage)
			}
		})
	}
}
