package auditlogpersistence

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	auditlogpersistencev1alpha1 "github.com/openshift/hypershift/api/auditlogpersistence/v1alpha1"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/storage/names"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/go-logr/logr"
)

const (
	controlPlaneNamespaceLabel  = "hypershift.openshift.io/hosted-control-plane"
	kubeAPIServerDeploymentName = "kube-apiserver"
	kubeAPIServerLabel          = "app"
	kubeAPIServerLabelValue     = "kube-apiserver"
	logsVolumeName              = "logs"
	pvcNamePrefix               = "kas-audit-logs-"
)

type PodWebhookHandler struct {
	log     logr.Logger
	client  client.Client
	decoder admission.Decoder
}

var _ admission.Handler = &PodWebhookHandler{}

func NewPodWebhookHandler(log logr.Logger, c client.Client, decoder admission.Decoder) *PodWebhookHandler {
	return &PodWebhookHandler{
		log:     log.WithName("audit-log-persistence-pod-webhook"),
		client:  c,
		decoder: decoder,
	}
}

func (h *PodWebhookHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	// Only handle Pod resources
	if req.Kind.Group != "" || req.Kind.Kind != "Pod" {
		return admission.Allowed("")
	}

	// Only mutate CREATE operations
	if req.Operation != admissionv1.Create {
		return admission.Allowed("")
	}

	// Check if namespace is a control plane namespace
	ns := &corev1.Namespace{}
	if err := h.client.Get(ctx, types.NamespacedName{Name: req.Namespace}, ns); err != nil {
		if apierrors.IsNotFound(err) {
			return admission.Allowed("")
		}
		h.log.Error(err, "Failed to get namespace", "namespace", req.Namespace)
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed to get namespace %s: %w", req.Namespace, err))
	}

	if ns.Labels == nil || ns.Labels[controlPlaneNamespaceLabel] != "true" {
		return admission.Allowed("")
	}

	// Decode the pod first to check both name and generateName
	pod := &corev1.Pod{}
	if err := h.decoder.Decode(req, pod); err != nil {
		h.log.Error(err, "Failed to decode pod")
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Check if pod name or generateName starts with kube-apiserver deployment name prefix
	// Pods created by the kube-apiserver deployment follow the pattern: kube-apiserver-<replicaset-hash>-<random-suffix>
	// When created by ReplicaSet, they use generateName which is kube-apiserver-<replicaset-hash>-
	podNameToCheck := pod.Name
	if podNameToCheck == "" {
		podNameToCheck = pod.GenerateName
	}
	if !strings.HasPrefix(podNameToCheck, kubeAPIServerDeploymentName+"-") {
		return admission.Allowed("")
	}

	// Verify this is a kube-apiserver pod by label (defensive check)
	if !isKubeAPIServerPod(pod) {
		return admission.Allowed("")
	}

	// Get the AuditLogPersistenceConfig
	config := &auditlogpersistencev1alpha1.AuditLogPersistenceConfig{}
	if err := h.client.Get(ctx, types.NamespacedName{Name: "cluster"}, config); err != nil {
		if apierrors.IsNotFound(err) {
			return admission.Allowed("")
		}
		h.log.Error(err, "Failed to get AuditLogPersistenceConfig")
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed to get AuditLogPersistenceConfig: %w", err))
	}

	// Apply defaults to a copy of the spec to avoid modifying the original
	spec := config.Spec.DeepCopy()
	ApplyDefaults(spec)

	// Check if feature is enabled
	if !IsEnabled(spec) {
		return admission.Allowed("")
	}

	// Mutate the pod
	mutated := pod.DeepCopy()
	if err := h.mutatePod(ctx, mutated, spec); err != nil {
		h.log.Error(err, "Failed to mutate pod for audit log persistence")
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed to mutate pod: %w", err))
	}

	mutatedRaw, err := json.Marshal(mutated)
	if err != nil {
		h.log.Error(err, "Failed to marshal mutated pod")
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf("failed to marshal mutated pod: %w", err))
	}

	h.log.Info("Successfully mutated pod for audit log persistence", "pod", mutated.Name, "namespace", pod.Namespace, "pvc", pvcNamePrefix+mutated.Name)
	return admission.PatchResponseFromRaw(req.Object.Raw, mutatedRaw)
}

func (h *PodWebhookHandler) mutatePod(ctx context.Context, pod *corev1.Pod, spec *auditlogpersistencev1alpha1.AuditLogPersistenceConfigSpec) error {
	// If pod has generateName but no name, generate a final name
	// This ensures we have a stable name for PVC creation
	// Use the same name generator that Kubernetes uses internally
	if pod.Name == "" && pod.GenerateName != "" {
		generatedName := names.SimpleNameGenerator.GenerateName(pod.GenerateName)
		h.log.V(1).Info("Generating pod name from generateName", "generateName", pod.GenerateName, "generatedName", generatedName)
		pod.Name = generatedName
		pod.GenerateName = ""
	}

	// Verify pod has a name
	if pod.Name == "" {
		return fmt.Errorf("pod has no name or generateName")
	}

	// Create PVC name using the pod's final name
	pvcName := pvcNamePrefix + pod.Name

	// Find the ReplicaSet owner to set as PVC owner
	var replicaSetOwner *metav1.OwnerReference
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "ReplicaSet" && owner.APIVersion == "apps/v1" {
			replicaSetOwner = &owner
			break
		}
	}

	// Create the PVC
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: pod.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: spec.Storage.Size,
				},
			},
		},
	}

	if spec.Storage.StorageClassName != "" {
		pvc.Spec.StorageClassName = &spec.Storage.StorageClassName
	}

	// Set ReplicaSet as owner if found
	if replicaSetOwner != nil {
		pvc.OwnerReferences = []metav1.OwnerReference{
			{
				APIVersion: replicaSetOwner.APIVersion,
				Kind:       replicaSetOwner.Kind,
				Name:       replicaSetOwner.Name,
				UID:        replicaSetOwner.UID,
				Controller: replicaSetOwner.Controller,
			},
		}
	}

	// Create or update the PVC
	if err := h.client.Create(ctx, pvc); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			h.log.Error(err, "Failed to create PVC", "pvcName", pvcName)
			return fmt.Errorf("failed to create PVC %s: %w", pvcName, err)
		}
		// PVC already exists, update owner references if needed
		existingPVC := &corev1.PersistentVolumeClaim{}
		if err := h.client.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: pod.Namespace}, existingPVC); err != nil {
			h.log.Error(err, "Failed to get existing PVC", "pvcName", pvcName)
			return fmt.Errorf("failed to get existing PVC %s: %w", pvcName, err)
		}
		if replicaSetOwner != nil && len(existingPVC.OwnerReferences) == 0 {
			existingPVC.OwnerReferences = pvc.OwnerReferences
			if err := h.client.Update(ctx, existingPVC); err != nil {
				h.log.Error(err, "Failed to update PVC owner references", "pvcName", pvcName)
				return fmt.Errorf("failed to update PVC %s: %w", pvcName, err)
			}
		}
	}

	// Replace the logs volume with PVC volume
	found := false
	for i, volume := range pod.Spec.Volumes {
		if volume.Name == logsVolumeName {
			pod.Spec.Volumes[i] = corev1.Volume{
				Name: logsVolumeName,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvcName,
					},
				},
			}
			found = true
			break
		}
	}

	if !found {
		// Add the volume if it doesn't exist
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: logsVolumeName,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvcName,
				},
			},
		})
	}

	return nil
}
