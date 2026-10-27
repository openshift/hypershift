package util

import (
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/blang/semver"
	"github.com/onsi/gomega"
)

func TestWaitForNodePoolVersion(t *testing.T) {
	const version = "5.0.0-0.ci-2026-10-26-121613"

	scheme := runtime.NewScheme()
	if err := hyperv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to build scheme: %v", err)
	}
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "np", Namespace: "clusters"},
		Status:     hyperv1.NodePoolStatus{Version: version},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nodePool).Build()
	key := crclient.ObjectKeyFromObject(nodePool)

	t.Run("returns once status.version matches", func(t *testing.T) {
		WaitForNodePoolVersion(gomega.NewWithT(t), t.Context(), client, key, semver.MustParse(version), 2*time.Second, 10*time.Millisecond)
	})

	t.Run("times out when status.version never matches", func(t *testing.T) {
		var failure string
		g := gomega.NewGomega(func(message string, callerSkip ...int) {
			failure = message
		})
		WaitForNodePoolVersion(g, t.Context(), client, key, semver.MustParse("4.22.0"), 100*time.Millisecond, 10*time.Millisecond)
		if failure == "" {
			t.Fatal("expected the wait to fail for a non-matching version")
		}
	})
}
