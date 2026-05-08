package util

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"

	"github.com/openshift/hypershift/cmd/util"
)

// streamHCPPodLogs starts background goroutines that follow logs from
// karpenter-related pods in the given HCP namespace. This captures the
// teardown window — the period between HC deletion and pod termination —
// which point-in-time dumps miss because the pods are dead by the time
// the retry dump runs.
//
// Returns a stop function that cancels all streams and waits for the
// goroutines to exit. The caller must invoke it (typically via defer).
func streamHCPPodLogs(ctx context.Context, t *testing.T, namespace, artifactDir string) func() {
	streamCtx, cancel := context.WithCancel(ctx)
	noop := func() { cancel() }

	cfg, err := util.GetConfig()
	if err != nil {
		t.Logf("Teardown log streaming: failed to get rest config: %v", err)
		return noop
	}

	kc, err := kubeclient.NewForConfig(cfg)
	if err != nil {
		t.Logf("Teardown log streaming: failed to create kubeclient: %v", err)
		return noop
	}

	logDir := filepath.Join(artifactDir, "teardown-logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Logf("Teardown log streaming: failed to create directory: %v", err)
		return noop
	}

	selectors := []string{"app=karpenter", "app=karpenter-operator"}

	var wg sync.WaitGroup
	started := 0
	for _, sel := range selectors {
		pods, err := kc.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: sel,
		})
		if err != nil {
			t.Logf("Teardown log streaming: failed to list pods (selector=%s): %v", sel, err)
			continue
		}
		for i := range pods.Items {
			pod := &pods.Items[i]
			for _, container := range pod.Spec.Containers {
				wg.Add(1)
				started++
				go followContainerLog(streamCtx, t, &wg, kc, namespace, pod.Name, container.Name, logDir)
			}
		}
	}

	if started == 0 {
		t.Logf("Teardown log streaming: no karpenter pods found in %s, skipping", namespace)
		return noop
	}

	t.Logf("Teardown log streaming: following %d container(s) in %s", started, namespace)
	return func() {
		cancel()
		wg.Wait()
	}
}

func followContainerLog(ctx context.Context, t *testing.T, wg *sync.WaitGroup, kc kubeclient.Interface, namespace, podName, containerName, logDir string) {
	defer wg.Done()

	fileName := filepath.Join(logDir, fmt.Sprintf("%s-%s.log", podName, containerName))
	f, err := os.Create(fileName)
	if err != nil {
		t.Logf("Teardown log streaming: failed to create log file %s: %v", fileName, err)
		return
	}
	defer f.Close()

	stream, err := kc.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: containerName,
		Follow:    true,
	}).Stream(ctx)
	if err != nil {
		t.Logf("Teardown log streaming: failed to stream logs for %s/%s: %v", podName, containerName, err)
		return
	}
	defer stream.Close()

	io.Copy(f, stream)
}
