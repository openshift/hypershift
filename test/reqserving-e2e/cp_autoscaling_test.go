//go:build reqserving
// +build reqserving

package reqservinge2e

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/util/reqserving"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	vpaautoscalingv1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	"k8s.io/client-go/kubernetes"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	loadWorkerCount      = 600
	configMapPayloadSize = 1048576
	loadWorkerDelay      = 100 * time.Millisecond
)

func TestControlPlaneAutoscalingIncreasesSize(t *testing.T) {
	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	// Ensure VPA operator is installed on the management cluster before creating the HostedCluster
	mgmtClient, err := e2eutil.GetClient()
	if err != nil {
		t.Fatalf("failed to get management client: %v", err)
	}
	if err := reqserving.EnsureVPAOperatorInstalled(ctx, t, mgmtClient); err != nil {
		t.Fatalf("failed to ensure VPA operator installed: %v", err)
	}

	// Prepare cluster options based on the request-serving template, plus autoscaling annotation
	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)
	clusterOpts.RawCreateOptions.RedactBaseDomain = true
	clusterOpts.Annotations = append(clusterOpts.Annotations,
		"hypershift.openshift.io/topology=dedicated-request-serving-components",
		fmt.Sprintf("%s=true", hyperv1.ResourceBasedControlPlaneAutoscalingAnnotation),
	)
	clusterOpts.AWSPlatform.Zones = []string(globalOpts.ConfigurableClusterOptions.Zone)
	if clusterOpts.NodeSelector == nil {
		clusterOpts.NodeSelector = make(map[string]string)
	}
	clusterOpts.NodeSelector[reqserving.ControlPlaneNodeLabel] = "true"

	// Execute lifecycle for the HostedCluster and run validations
	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Baseline: wait for kube-apiserver VPA RecommendationProvided=True
		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		waitForKASVPAReady(t, ctx, mgtClient, hcpNamespace)

		// Capture baseline recommended cluster size (may be empty briefly; wait for presence)
		baseline := waitForRecommendedSizeAnnotation(t, ctx, mgtClient, hostedCluster)
		t.Logf("Observed baseline recommended cluster size: %s", baseline)

		// Fetch cluster sizing configuration to validate size increases by memory
		var orderedSizes []string
		csc := &schedulingv1alpha1.ClusterSizingConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}}
		if err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(csc), csc); err == nil && len(csc.Spec.Sizes) > 0 {
			orderedSizes = sizesInOrderByMemory(csc)
			t.Logf("Found ClusterSizingConfiguration with %d sizes", len(orderedSizes))
		} else {
			t.Log("ClusterSizingConfiguration not available or empty; will accept any size change")
		}

		// Generate sustained kube-apiserver load in the guest cluster
		guestCfg := e2eutil.WaitForGuestRestConfig(t, ctx, mgtClient, hostedCluster)
		guestKubeClient := kubernetes.NewForConfigOrDie(guestCfg)
		t.Log("Starting sustained kube-apiserver load")
		loadCtx, loadCancel := context.WithCancel(ctx)
		defer loadCancel()
		go generateKubeAPIServerLoad(loadCtx, t, guestKubeClient)

		// Wait up to 30 minutes for recommended size to increase
		t.Log("Waiting for recommended cluster size to increase")
		err := waitForRecommendedSizeIncrease(t, ctx, mgtClient, hostedCluster, baseline, orderedSizes)
		// Stop the load regardless of outcome
		loadCancel()
		g.Expect(err).NotTo(HaveOccurred(), "expected the recommended cluster size to increase under sustained load")

		// Optional: verify VPA object still present and KAS deployment not crashing
		g.Expect(verifyKASDeploymentHealthy(ctx, mgtClient, hcpNamespace)).To(Succeed())
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "create-cluster", globalOpts.ServiceAccountSigningKey)
}

func waitForKASVPAReady(t *testing.T, ctx context.Context, c crclient.Client, hcpNamespace string) {
	t.Helper()
	err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 20*time.Minute, true, func(ctx context.Context) (bool, error) {
		vpa := &vpaautoscalingv1.VerticalPodAutoscaler{}
		err := c.Get(ctx, crclient.ObjectKey{Namespace: hcpNamespace, Name: "kube-apiserver"}, vpa)
		if err != nil {
			return false, crclient.IgnoreNotFound(err)
		}
		if vpa.Status.Recommendation == nil {
			return false, nil
		}
		for _, cond := range vpa.Status.Conditions {
			if cond.Type == vpaautoscalingv1.RecommendationProvided && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("kube-apiserver VPA did not become ready: %v", err)
	}
}

func waitForRecommendedSizeAnnotation(t *testing.T, ctx context.Context, c crclient.Client, hc *hyperv1.HostedCluster) string {
	t.Helper()
	var value string
	err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 20*time.Minute, true, func(ctx context.Context) (bool, error) {
		current := &hyperv1.HostedCluster{}
		if err := c.Get(ctx, crclient.ObjectKeyFromObject(hc), current); err != nil {
			return false, err
		}
		if current.Annotations != nil {
			value = current.Annotations[hyperv1.RecommendedClusterSizeAnnotation]
		}
		return value != "", nil
	})
	if err != nil {
		t.Fatalf("timed out waiting for %s annotation: %v", hyperv1.RecommendedClusterSizeAnnotation, err)
	}
	return value
}

func waitForRecommendedSizeIncrease(t *testing.T, ctx context.Context, c crclient.Client, hc *hyperv1.HostedCluster, baseline string, orderedSizes []string) error {
	t.Helper()
	// Try to validate the increase using ClusterSizingConfiguration memory values when available.
	return wait.PollUntilContextTimeout(ctx, 20*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
		current := &hyperv1.HostedCluster{}
		if err := c.Get(ctx, crclient.ObjectKeyFromObject(hc), current); err != nil {
			return false, err
		}
		cur := ""
		if current.Annotations != nil {
			cur = current.Annotations[hyperv1.RecommendedClusterSizeAnnotation]
		}
		if cur == "" || cur == baseline {
			return false, nil
		}
		// If sizing order is available, ensure that cur is >= baseline by memory
		if len(orderedSizes) > 0 {
			ibase := slices.Index(orderedSizes, baseline)
			icur := slices.Index(orderedSizes, cur)
			if ibase == -1 || icur == -1 {
				// Unknown names; accept change
				return true, nil
			}
			return icur >= ibase, nil
		}
		// Without size ordering available, accept any change
		return true, nil
	})
}

func sizesInOrderByMemory(csc *schedulingv1alpha1.ClusterSizingConfiguration) []string {
	type sizeWithMem struct {
		name string
		mem  int64
	}
	list := make([]sizeWithMem, 0, len(csc.Spec.Sizes))
	for _, s := range csc.Spec.Sizes {
		if s.Capacity == nil || s.Capacity.Memory == nil {
			continue
		}
		list = append(list, sizeWithMem{name: s.Name, mem: s.Capacity.Memory.Value()})
	}
	slices.SortFunc(list, func(a, b sizeWithMem) int {
		if a.mem < b.mem {
			return -1
		}
		if a.mem > b.mem {
			return 1
		}
		return 0
	})
	out := make([]string, 0, len(list))
	for _, it := range list {
		out = append(out, it.name)
	}
	return out
}

func generateKubeAPIServerLoad(ctx context.Context, t *testing.T, kc *kubernetes.Clientset) {
	t.Helper()
	ns := "kas-load"
	// Create namespace for load generation (best-effort)
	if _, err := kc.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Logf("failed to create namespace for load: %v", err)
		return
	}

	// Cleanup namespace when done
	defer func() {
		if err := kc.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{}); err != nil {
			t.Logf("failed to cleanup namespace %s: %v", ns, err)
		}
	}()

	var wg sync.WaitGroup
	wg.Add(loadWorkerCount)

	for i := 0; i < loadWorkerCount; i++ {
		go func(id int) {
			defer wg.Done()
			// Per-worker start jitter to avoid thundering herd
			jitter := time.Duration(rand.Intn(200)) * time.Millisecond
			select {
			case <-ctx.Done():
				return
			case <-time.After(jitter):
			}
			for {
				select {
				case <-ctx.Done():
					return
				default:
					name := fmt.Sprintf("cm-load-%d-%d", id, time.Now().UnixNano())
					cm := &corev1.ConfigMap{
						ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
						Data: map[string]string{
							"payload": strings.Repeat("x", configMapPayloadSize),
						},
					}
					if _, err := kc.CoreV1().ConfigMaps(ns).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
						t.Logf("failed to create configmap %s: %v", name, err)
					}
					if _, err := kc.CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{}); err != nil {
						t.Logf("failed to get configmap %s: %v", name, err)
					}
					if err := kc.CoreV1().ConfigMaps(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
						t.Logf("failed to delete configmap %s: %v", name, err)
					}
					// Small delay to avoid overwhelming the API server
					select {
					case <-ctx.Done():
						return
					case <-time.After(loadWorkerDelay):
					}
				}
			}
		}(i)
	}

	// Wait for context cancellation
	<-ctx.Done()
	t.Log("Load generation cancelled due to context")

	// Wait for all workers to finish
	wg.Wait()
}

func verifyKASDeploymentHealthy(ctx context.Context, c crclient.Client, hcpNamespace string) error {
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, crclient.ObjectKey{Namespace: hcpNamespace, Name: "kube-apiserver"}, dep); err != nil {
		return err
	}
	for _, cond := range dep.Status.Conditions {
		if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
			return nil
		}
	}
	return fmt.Errorf("kube-apiserver deployment not Available")
}
