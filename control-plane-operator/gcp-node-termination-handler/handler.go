package gcpnodeterminationhandler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"

	"github.com/go-logr/logr"
)

const (
	componentName       = "gcp-node-termination-handler"
	preemptedTaintKey   = componentName + "/preempted"
	preemptionSignalKey = "hypershift.openshift.io/gcp-preemption-signal"
)

type drainer interface {
	Drain(ctx context.Context, nodeName string, timeout time.Duration) error
}

type kubectlDrainer struct {
	client kubernetes.Interface
	log    logr.Logger
}

func (d *kubectlDrainer) Drain(ctx context.Context, nodeName string, timeout time.Duration) error {
	drainer := &drain.Helper{
		Client:              d.client,
		Force:               true,
		IgnoreAllDaemonSets: true,
		DeleteEmptyDirData:  true,
		GracePeriodSeconds:  -1,
		Timeout:             timeout,
		Out:                 io.Discard,
		ErrOut:              io.Discard,
		Ctx:                 ctx,
	}
	if err := drain.RunNodeDrain(drainer, nodeName); err != nil {
		return fmt.Errorf("failed to drain node")
	}
	return nil
}

type handler struct {
	nodeName     string
	metadataURL  string
	pollInterval time.Duration
	drainTimeout time.Duration
	kubeClient   kubernetes.Interface
	httpClient   *http.Client
	drainer      drainer
	log          logr.Logger
}

func (h *handler) run(ctx context.Context) error {
	for {
		preempted, err := h.isPreempted(ctx)
		if err != nil {
			h.log.Error(err, "Failed to check GCP preemption metadata")
		} else if preempted {
			h.log.Info("GCP preemption detected")
			if err := h.handlePreemption(ctx); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(h.pollInterval):
		}
	}
}

func (h *handler) isPreempted(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.metadataURL, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("metadata request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("metadata server returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read metadata response")
	}

	return strings.EqualFold(strings.TrimSpace(string(body)), "TRUE"), nil
}

func (h *handler) handlePreemption(ctx context.Context) error {
	nodes := h.kubeClient.CoreV1().Nodes()
	node, err := nodes.Get(ctx, h.nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node")
	}

	updated := node.DeepCopy()
	changed := false
	if !updated.Spec.Unschedulable {
		updated.Spec.Unschedulable = true
		changed = true
	}
	if !hasTaint(updated, preemptedTaintKey) {
		updated.Spec.Taints = append(updated.Spec.Taints, corev1.Taint{
			Key:    preemptedTaintKey,
			Effect: corev1.TaintEffectNoSchedule,
		})
		changed = true
	}
	if updated.Annotations == nil {
		updated.Annotations = map[string]string{}
	}
	if _, ok := updated.Annotations[preemptionSignalKey]; !ok {
		updated.Annotations[preemptionSignalKey] = time.Now().UTC().Format(time.RFC3339)
		changed = true
	}

	if changed {
		if _, err := nodes.Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
			if apierrors.IsConflict(err) {
				return fmt.Errorf("failed to update node due to conflict")
			}
			return fmt.Errorf("failed to mark node preempted")
		}
	}

	if err := h.drainer.Drain(ctx, h.nodeName, h.drainTimeout); err != nil {
		return err
	}

	h.log.Info("Node drain completed")
	return nil
}

func hasTaint(node *corev1.Node, key string) bool {
	for _, taint := range node.Spec.Taints {
		if taint.Key == key {
			return true
		}
	}
	return false
}
