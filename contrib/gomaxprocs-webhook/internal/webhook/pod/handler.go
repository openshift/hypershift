package pod

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/go-logr/logr"
	"github.com/openshift/hypershift/contrib/gomaxprocs-webhook/internal/config"
)

const (
	controlPlaneNamespaceLabel = "hypershift.openshift.io/hosted-control-plane"
	// maxOwnerTraversalDepth limits how deep we traverse owner references to prevent infinite loops
	maxOwnerTraversalDepth = 5
)

type Handler struct {
	log     logr.Logger
	client  client.Client
	decoder admission.Decoder
	cfg     config.Loader
}

var _ admission.Handler = &Handler{}

func NewHandler(log logr.Logger, c client.Client, decoder admission.Decoder, cfg config.Loader) *Handler {
	return &Handler{log: log.WithName("gomaxprocs-webhook"), client: c, decoder: decoder, cfg: cfg}
}

func (h *Handler) Handle(ctx context.Context, req admission.Request) admission.Response {
	h.log.V(2).Info("Processing admission request", "namespace", req.Namespace, "name", req.Name, "kind", req.Kind.Kind, "operation", req.Operation, "uid", req.UID)

	if req.Kind.Group != "" || req.Kind.Kind != "Pod" {
		h.log.V(2).Info("Skipping non-Pod resource", "group", req.Kind.Group, "kind", req.Kind.Kind)
		return admission.Allowed("")
	}

	// Only mutate CREATE
	if req.Operation != admissionv1.Create {
		h.log.V(2).Info("Skipping non-CREATE operation", "operation", req.Operation)
		return admission.Allowed("")
	}

	// Restrict to control plane namespaces; additionally enforced by namespaceSelector in MWC
	ns := &corev1.Namespace{}
	if err := h.client.Get(ctx, types.NamespacedName{Name: req.Namespace}, ns); err != nil {
		if apierrors.IsNotFound(err) {
			h.log.V(2).Info("Namespace not found, allowing", "namespace", req.Namespace)
			return admission.Allowed("")
		}
		h.log.Error(err, "Failed to get namespace", "namespace", req.Namespace)
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed to get namespace %s: %w", req.Namespace, err))
	}

	h.log.V(2).Info("Checking namespace labels", "namespace", req.Namespace, "labels", ns.Labels)
	if ns.Labels == nil || ns.Labels[controlPlaneNamespaceLabel] != "true" {
		h.log.V(2).Info("Namespace missing control plane label, allowing", "namespace", req.Namespace, "expectedLabel", controlPlaneNamespaceLabel)
		return admission.Allowed("")
	}

	pod := &corev1.Pod{}
	if err := h.decoder.Decode(req, pod); err != nil {
		h.log.Error(err, "Failed to decode pod")
		return admission.Errored(http.StatusBadRequest, err)
	}

	podName := effectivePodName(pod)
	h.log.V(1).Info("Processing pod for GOMAXPROCS injection", "pod", podName, "namespace", pod.Namespace)
	mutated := pod.DeepCopy()
	changed := h.injectForPod(ctx, mutated)
	if !changed {
		h.log.V(1).Info("No changes made to pod", "pod", podName, "namespace", pod.Namespace)
		return admission.Allowed("")
	}

	mutatedRaw, err := json.Marshal(mutated)
	if err != nil {
		h.log.Error(err, "Failed to marshal patched pod")
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed to marshal patched pod: %w", err))
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, mutatedRaw)
}

func (h *Handler) injectForPod(ctx context.Context, pod *corev1.Pod) bool {
	// Resolve top-level workload
	kind, name := h.resolveTopOwner(ctx, pod)
	h.log.V(2).Info("Resolved top-level workload", "kind", kind, "name", name)

	mutated := false
	podName := effectivePodName(pod)
	// initContainers
	for i := range pod.Spec.InitContainers {
		if h.injectForContainer(ctx, kind, name, podName, pod.Namespace, &pod.Spec.InitContainers[i]) {
			mutated = true
		}
	}
	for i := range pod.Spec.Containers {
		if h.injectForContainer(ctx, kind, name, podName, pod.Namespace, &pod.Spec.Containers[i]) {
			mutated = true
		}
	}
	return mutated
}

func (h *Handler) injectForContainer(ctx context.Context, workloadKind, workloadName, podName, podNamespace string, c *corev1.Container) bool {
	h.log.V(2).Info("Checking container for GOMAXPROCS injection", "container", c.Name, "workloadKind", workloadKind, "workloadName", workloadName)

	// Respect existing env var
	for i := range c.Env {
		if c.Env[i].Name == "GOMAXPROCS" {
			h.log.V(1).Info("Container already has GOMAXPROCS, skipping", "container", c.Name, "existingValue", c.Env[i].Value)
			return false
		}
	}

	value, excluded, ok := h.cfg.Resolve(ctx, workloadKind, workloadName, c.Name)
	h.log.V(2).Info("Configuration resolution result", "container", c.Name, "value", value, "excluded", excluded, "ok", ok)

	if excluded {
		h.log.V(1).Info("Container excluded from GOMAXPROCS injection", "container", c.Name)
		return false
	}
	if !ok || value == "" {
		h.log.V(1).Info("No GOMAXPROCS value resolved for container", "container", c.Name, "ok", ok, "value", value)
		return false
	}

	h.log.Info("Injected GOMAXPROCS into container", "container", c.Name, "value", value, "pod", podName, "namespace", podNamespace)
	c.Env = append(c.Env, corev1.EnvVar{Name: "GOMAXPROCS", Value: value})
	return true
}

// resolveTopOwner attempts to find the top-level owner kind/name for the Pod.
// It follows common chains like ReplicaSet->Deployment and Job->CronJob with safety limits.
func (h *Handler) resolveTopOwner(ctx context.Context, pod *corev1.Pod) (string, string) {
	if len(pod.OwnerReferences) == 0 {
		return "", ""
	}

	// Track visited UIDs to detect cycles
	visited := make(map[string]bool)
	currentKind := "Pod"
	currentName := pod.Name
	currentUID := string(pod.UID)
	visited[currentUID] = true

	// Start with the pod's first owner
	owner := pod.OwnerReferences[0]
	depth := 0

traversalLoop:
	for depth < maxOwnerTraversalDepth {
		// Prevent infinite loops by checking if we've seen this UID before
		ownerUID := string(owner.UID)
		if visited[ownerUID] {
			h.log.V(1).Info("Detected owner reference cycle, stopping traversal", "kind", owner.Kind, "name", owner.Name, "uid", ownerUID, "depth", depth)
			break traversalLoop
		}
		visited[ownerUID] = true

		currentKind = owner.Kind
		currentName = owner.Name

		// If the owner of the pod is a Deployment, or StatefulSet, that is the top-level workload.
		// However, if the owner is a ReplicaSet then its owner will likely be a Deployment, so we need to follow that chain.
		// Similarly, if the owner is a Job, then its owner could be a CronJob, so we need to follow that chain.
		var nextOwner *metav1.OwnerReference
		switch owner.Kind {
		case "ReplicaSet":
			rs := &appsv1.ReplicaSet{}
			if err := h.client.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: owner.Name}, rs); err != nil {
				h.log.V(2).Info("Failed to get ReplicaSet, stopping traversal", "name", owner.Name, "error", err)
				break traversalLoop
			}
			if len(rs.OwnerReferences) > 0 {
				// Allow ReplicaSet -> Deployment (common) or ReplicaSet -> ReplicaSet (for testing/edge cases)
				if rs.OwnerReferences[0].Kind == "Deployment" || rs.OwnerReferences[0].Kind == "ReplicaSet" {
					nextOwner = &rs.OwnerReferences[0]
				}
			}
		case "Job":
			job := &batchv1.Job{}
			if err := h.client.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: owner.Name}, job); err != nil {
				h.log.V(2).Info("Failed to get Job, stopping traversal", "name", owner.Name, "error", err)
				break traversalLoop
			}
			if len(job.OwnerReferences) > 0 && job.OwnerReferences[0].Kind == "CronJob" {
				nextOwner = &job.OwnerReferences[0]
			}
		default:
			// For other kinds (StatefulSet, DaemonSet, etc.), stop here as they're typically top-level
			break traversalLoop
		}

		if nextOwner == nil {
			break traversalLoop
		}

		owner = *nextOwner
		depth++
	}

	if depth >= maxOwnerTraversalDepth {
		h.log.V(1).Info("Reached maximum owner traversal depth, stopping", "maxDepth", maxOwnerTraversalDepth, "finalKind", currentKind, "finalName", currentName)
	}

	h.log.V(2).Info("Owner resolution completed", "finalKind", currentKind, "finalName", currentName, "depth", depth)
	return currentKind, currentName
}

// effectivePodName returns a non-empty identifier for the pod for logging purposes.
// When a pod is created by a controller, the name may be empty in admission requests; in that case
// we fall back to GenerateName. If both are empty, return a placeholder.
func effectivePodName(pod *corev1.Pod) string {
	if pod == nil {
		return "<unknown>"
	}
	if pod.Name != "" {
		return pod.Name
	}
	if pod.GenerateName != "" {
		return pod.GenerateName
	}
	return "<unknown>"
}
