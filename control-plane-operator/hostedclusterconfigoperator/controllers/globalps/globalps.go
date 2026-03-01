package globalps

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/thirdparty/kubernetes/pkg/credentialprovider"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	crreconcile "sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ControllerName                     = "globalps"
	configSeedLabelKey                 = "hypershift.openshift.io/globalps-config-hash"
	globalPSLabelKey                   = "hypershift.openshift.io/nodepool-globalps-enabled"
	openshiftUserCriticalPriorityClass = "openshift-user-critical"
)

// GlobalPullSecretPodConfig encapsulates the configuration for GlobalPullSecret DaemonSet pods
type GlobalPullSecretPodConfig struct {
	VolumeMounts []corev1.VolumeMount
	Volumes      []corev1.Volume
}

type Reconciler struct {
	cpClient               crclient.Client
	kubeSystemSecretClient crclient.Client
	nodeClient             crclient.Client
	hcUncachedClient       crclient.Client
	hcpNamespace           string
	hccoImage              string

	upsert.CreateOrUpdateProvider
}

func (r *Reconciler) Reconcile(ctx context.Context, req crreconcile.Request) (crreconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling global pull secret")

	// Reconcile GlobalPullSecret
	if err := r.reconcileGlobalPullSecret(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile global pull secret: %w", err)
	}

	return ctrl.Result{}, nil
}

// reconcileGlobalPullSecret reconciles the original pull secret given by HCP and merges it with a new pull secret provided by the user.
// The new pull secret is only stored in the DataPlane side so, it's not exposed in the API. It lives in the kube-system namespace of the DataPlane.
// - If that PS is created, the HCCO deploys a DaemonSet which mounts the node's kubeconfig's file, and merges the new PS with the original one.
// - If the PS doesn't exist, the HCCO doesn't do anything.
// - If at some point the user deletes the additional pull secret, the daemonSet will not be removed
// If the PS doesn't exist, the HCCO doesn't do anything.
//
// IMPORTANT: The DaemonSet is ONLY deployed to nodes that are explicitly labeled as eligible.
// Nodes belonging to NodePools using InPlace upgrade strategy are NOT labeled, preventing
// conflicts between the DaemonSet's kubelet config modifications and Machine Config Daemon operations.
func (r *Reconciler) reconcileGlobalPullSecret(ctx context.Context) error {
	var (
		userProvidedPullSecretBytes []byte
		originalPullSecretBytes     []byte
		globalPullSecretBytes       []byte
		err                         error
		ok                          bool
	)
	log := ctrl.LoggerFrom(ctx)
	log.Info("reconciling global pull secret")

	// Create ServiceAccount for global-pull-secret-syncer
	serviceAccount := manifests.GlobalPullSecretServiceAccount()
	if _, err := r.CreateOrUpdate(ctx, r.hcUncachedClient, serviceAccount, func() error {
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile global pull secret service account: %w", err)
	}

	// Get the original pull secret once at the beginning (used in both scenarios)
	originalPullSecret := manifests.PullSecret(r.hcpNamespace)
	if err := r.cpClient.Get(ctx, crclient.ObjectKeyFromObject(originalPullSecret), originalPullSecret); err != nil {
		return fmt.Errorf("failed to get original pull secret: %w", err)
	}

	originalPullSecretBytes, ok = originalPullSecret.Data[corev1.DockerConfigJsonKey]
	if !ok || len(originalPullSecretBytes) == 0 {
		return fmt.Errorf("original pull secret does not contain %s key", corev1.DockerConfigJsonKey)
	}

	// Get the user provided pull secret
	exists, additionalPullSecret, err := additionalPullSecretExists(ctx, r.kubeSystemSecretClient)
	if err != nil {
		return fmt.Errorf("failed to check if user provided pull secret exists: %w", err)
	}

	if !exists || additionalPullSecret.Data == nil {
		// Delete global pull secret if it exists
		secret := manifests.GlobalPullSecret()
		if err := r.kubeSystemSecretClient.Delete(ctx, secret); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete global pull secret: %w", err)
			}
		}

		// Create/Update original pull secret in the DataPlane's kube-system namespace
		originalSecret := manifests.OriginalPullSecret()
		if _, err := r.CreateOrUpdate(ctx, r.kubeSystemSecretClient, originalSecret, func() error {
			originalSecret.Data = map[string][]byte{
				corev1.DockerConfigJsonKey: originalPullSecretBytes,
			}
			return nil
		}); err != nil {
			return fmt.Errorf("failed to create original pull secret: %w", err)
		}

		// Generate a hash of the original pull secret content to trigger pod recreation
		configSeed := util.HashSimple(originalPullSecretBytes)

		// Label nodes that should have GlobalPullSecret DaemonSet (non-InPlace nodes)
		// This is critical to prevent DaemonSet deployment conflicts with Machine Config Daemon
		log.Info("labeling nodes eligible for GlobalPullSecret DaemonSet")
		if err := r.labelNodesForGlobalPullSecret(ctx); err != nil {
			return fmt.Errorf("failed to label nodes for GlobalPullSecret: %w", err)
		}

		// Reconcile DaemonSet with only original pull secret (global-pull-secret will be optional and empty)
		daemonSet := manifests.GlobalPullSecretDaemonSet()
		if err := reconcileDaemonSet(ctx, daemonSet, "", originalSecret.Name, configSeed, r.hcUncachedClient, r.CreateOrUpdate, r.hccoImage); err != nil {
			return fmt.Errorf("failed to reconcile global pull secret daemon set: %w", err)
		}

		return nil
	}

	// Label nodes that should have GlobalPullSecret DaemonSet (non-InPlace nodes)
	// This is critical to prevent DaemonSet deployment conflicts with Machine Config Daemon
	log.Info("labeling nodes eligible for GlobalPullSecret DaemonSet")
	if err := r.labelNodesForGlobalPullSecret(ctx); err != nil {
		return fmt.Errorf("failed to label nodes for GlobalPullSecret: %w", err)
	}

	if userProvidedPullSecretBytes, err = validateAdditionalPullSecret(additionalPullSecret); err != nil {
		return fmt.Errorf("failed to validate user provided pull secret: %w", err)
	}

	log.Info("Valid additional pull secret found in the DataPlane, reconciling global pull secret")

	// Merge the additional pull secret with the original pull secret
	if globalPullSecretBytes, err = mergePullSecrets(ctx, originalPullSecretBytes, userProvidedPullSecretBytes); err != nil {
		return fmt.Errorf("failed to merge pull secrets: %w", err)
	}

	// Create original pull secret in the DataPlane's kube-system namespace
	originalSecret := manifests.OriginalPullSecret()
	if _, err := r.CreateOrUpdate(ctx, r.kubeSystemSecretClient, originalSecret, func() error {
		originalSecret.Data = map[string][]byte{
			corev1.DockerConfigJsonKey: originalPullSecretBytes,
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create original pull secret: %w", err)
	}

	// Create global pull secret in the DataPlane
	secret := manifests.GlobalPullSecret()
	if _, err := r.CreateOrUpdate(ctx, r.kubeSystemSecretClient, secret, func() error {
		secret.Data = map[string][]byte{
			corev1.DockerConfigJsonKey: globalPullSecretBytes,
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to create global pull secret: %w", err)
	}

	// Generate a hash of the global pull secret content to trigger pod recreation when content changes
	configSeed := util.HashSimple(globalPullSecretBytes)
	daemonSet := manifests.GlobalPullSecretDaemonSet()
	if err := reconcileDaemonSet(ctx, daemonSet, secret.Name, originalSecret.Name, configSeed, r.hcUncachedClient, r.CreateOrUpdate, r.hccoImage); err != nil {
		return fmt.Errorf("failed to reconcile global pull secret daemon set: %w", err)
	}

	return nil
}

// labelNodesForGlobalPullSecret labels nodes that should receive the GlobalPullSecret DaemonSet.
// Only nodes that do NOT belong to InPlace NodePools are labeled with hypershift.openshift.io/nodepool-globalps-enabled.
// This ensures the DaemonSet only deploys on nodes where it won't conflict with Machine Config Daemon.
func (r *Reconciler) labelNodesForGlobalPullSecret(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)

	// Get all nodes from the hosted cluster
	nodeList := &corev1.NodeList{}
	if err := r.nodeClient.List(ctx, nodeList); err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	// Get all MachineSets to identify which use InPlace upgrade strategy
	machineSetList := &capiv1.MachineSetList{}
	if err := r.cpClient.List(ctx, machineSetList, &crclient.ListOptions{
		Namespace: r.hcpNamespace,
	}); err != nil {
		return fmt.Errorf("failed to list MachineSets: %w", err)
	}

	// Create set of nodes that should be labeled (from Replace NodePools)
	nodesToLabel := make(map[string]bool)

	for _, ms := range machineSetList.Items {
		// Check if this MachineSet belongs to a NodePool with InPlace strategy
		// This can be identified by the presence of InPlace-specific annotations
		_, hasTargetConfig := ms.Annotations["hypershift.openshift.io/nodePoolTargetConfigVersion"]
		_, hasCurrentConfig := ms.Annotations["hypershift.openshift.io/nodePoolCurrentConfigVersion"]

		if hasTargetConfig || hasCurrentConfig {
			// This is InPlace MachineSet - skip its nodes
			continue
		}

		// This is Replace MachineSet - include its nodes for labeling
		machines := &capiv1.MachineList{}
		if err := r.cpClient.List(ctx, machines, &crclient.ListOptions{
			Namespace:     ms.Namespace,
			LabelSelector: labels.SelectorFromSet(ms.Spec.Selector.MatchLabels),
		}); err != nil {
			return fmt.Errorf("failed to list machines for Replace MachineSet %s: %w", ms.Name, err)
		}

		// Mark nodes from this Replace MachineSet for labeling
		for _, machine := range machines.Items {
			if machine.Status.NodeRef != nil {
				nodesToLabel[machine.Status.NodeRef.Name] = true
			}
		}
	}

	// Update labels only on nodes from Replace NodePools
	// These nodes are eligible for GlobalPullSecret DaemonSet scheduling
	for _, node := range nodeList.Items {
		if nodesToLabel[node.Name] {
			// Node belongs to a Replace NodePool, so it's eligible for GlobalPS
			nodeCopy := node.DeepCopy()

			if nodeCopy.Labels == nil {
				nodeCopy.Labels = make(map[string]string)
			}

			currentLabel := nodeCopy.Labels[globalPSLabelKey]

			if currentLabel != "true" {
				nodeCopy.Labels[globalPSLabelKey] = "true"
				log.Info("labeling node as eligible for GlobalPullSecret DaemonSet", "node", node.Name)

				if err := r.nodeClient.Update(ctx, nodeCopy); err != nil {
					return fmt.Errorf("failed to update node labels for GlobalPullSecret eligibility on node %s: %w", node.Name, err)
				}
			}
		}
	}

	return nil
}

func reconcileDaemonSet(ctx context.Context, daemonSet *appsv1.DaemonSet, globalPullSecretName string, originalPullSecretName string, configSeed string, c crclient.Client, createOrUpdate upsert.CreateOrUpdateFN, hccoImage string) error {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling global pull secret daemon set")

	if _, err := createOrUpdate(ctx, c, daemonSet, func() error {
		daemonSet.Spec = appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": manifests.GlobalPullSecretDSName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name":             manifests.GlobalPullSecretDSName,
						configSeedLabelKey: configSeed,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName:           manifests.GlobalPullSecretDSName,
					AutomountServiceAccountToken: ptr.To(false),
					SecurityContext:              &corev1.PodSecurityContext{},
					DNSPolicy:                    corev1.DNSDefault,
					PriorityClassName:            openshiftUserCriticalPriorityClass,
					Tolerations:                  []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					// Use nodeSelector to only include nodes that are explicitly enabled for GlobalPullSecret
					NodeSelector: map[string]string{
						globalPSLabelKey: "true",
					},
					Containers: []corev1.Container{
						{
							Name:            manifests.GlobalPullSecretDSName,
							Image:           hccoImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command: []string{
								"/usr/bin/control-plane-operator",
							},
							Args: []string{
								"sync-global-pullsecret",
							},
							SecurityContext: &corev1.SecurityContext{
								// Privileged mode is required for the following operations:
								// 1. Write access to /var/lib/kubelet/config.json (kubelet configuration file)
								// 2. DBus connection to systemd for kubelet service management
								// 3. Restart kubelet.service via systemd (requires root privileges)
								// These operations cannot be performed with specific capabilities due to
								// the combination of file system access and systemd service management.
								Privileged: ptr.To(true),
							},
							VolumeMounts:             buildGlobalPSVolumeMounts(globalPullSecretName),
							TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("35Mi"),
									corev1.ResourceCPU:    resource.MustParse("5m"),
								},
							},
						},
					},
					Volumes: buildGlobalPSVolumes(globalPullSecretName, originalPullSecretName),
				},
			},
		}

		return nil
	}); err != nil {
		return fmt.Errorf("failed to create global pull secret daemon set: %w", err)
	}

	return nil
}

func validateAdditionalPullSecret(pullSecret *corev1.Secret) ([]byte, error) {
	var dockerConfigJSON credentialprovider.DockerConfigJSON

	// Validate that the pull secret contains the dockerConfigJson key
	if _, ok := pullSecret.Data[corev1.DockerConfigJsonKey]; !ok {
		return nil, fmt.Errorf("pull secret data is not a valid docker config json")
	}

	// Validate that the pull secret is a valid DockerConfigJSON
	pullSecretBytes := pullSecret.Data[corev1.DockerConfigJsonKey]
	if err := json.Unmarshal(pullSecretBytes, &dockerConfigJSON); err != nil {
		return nil, fmt.Errorf("invalid docker config json format: %w", err)
	}

	// Validate that the pull secret contains at least one auth entry
	if len(dockerConfigJSON.Auths) == 0 {
		return nil, fmt.Errorf("docker config json must contain at least one auth entry")
	}

	return pullSecretBytes, nil
}

// MergePullSecrets merges two pull secrets into a single pull secret.
// The additional pull secret is merged with the original pull secret.
// If an auth entry already exists, the original pull secret will be kept.
// If there is somekind on conflict with the original pull secret, the user could
// try to use a namespaced entry, to avoid the limitation on the original pull secret.
// Not using credentialprovider.DockerConfigJSON because it does not support
// marshaling the auth field.
func mergePullSecrets(ctx context.Context, originalPullSecret, userProvidedPullSecret []byte) ([]byte, error) {
	var (
		originalAuths         map[string]any
		userProvidedAuths     map[string]any
		finalAuths            map[string]any
		originalJSON          map[string]any
		userProvidedJSON      map[string]any
		globalPullSecretBytes []byte
		err                   error
	)

	log := ctrl.LoggerFrom(ctx)
	// Unmarshal original pull secret
	if err = json.Unmarshal(originalPullSecret, &originalJSON); err != nil {
		return nil, fmt.Errorf("invalid original pull secret format: %w", err)
	}
	originalAuths = originalJSON["auths"].(map[string]any)

	// Unmarshal additional pull secret
	if err = json.Unmarshal(userProvidedPullSecret, &userProvidedJSON); err != nil {
		return nil, fmt.Errorf("invalid user provided pull secret format: %w", err)
	}
	userProvidedAuths = userProvidedJSON["auths"].(map[string]any)

	for k, v := range originalAuths {
		if _, ok := userProvidedAuths[k]; ok {
			log.Info("The registry provided in the additional-pull-secret secret already exists in the original pull secret, this is not allowed. Keeping the original pull secret registry authentication", "registry", k)
		}
		userProvidedAuths[k] = v
	}
	finalAuths = userProvidedAuths

	// Create final JSON
	finalJSON := map[string]any{
		"auths": finalAuths,
	}

	globalPullSecretBytes, err = json.Marshal(finalJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal merged pull secret: %w", err)
	}

	return globalPullSecretBytes, nil
}

func additionalPullSecretExists(ctx context.Context, c crclient.Client) (bool, *corev1.Secret, error) {
	additionalPullSecret := manifests.AdditionalPullSecret()
	if err := c.Get(ctx, crclient.ObjectKeyFromObject(additionalPullSecret), additionalPullSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil, nil
		}
		return false, nil, err
	}
	return true, additionalPullSecret, nil
}

// Volume build functions for GlobalPullSecret DaemonSet
// buildGlobalPSVolumeMounts builds the volume mounts for the GlobalPullSecret DaemonSet
func buildGlobalPSVolumeMounts(globalPullSecretName string) []corev1.VolumeMount {
	var volumeMounts []corev1.VolumeMount

	volumeMounts = append(volumeMounts, globalPSVolumeMountKubeletConfig())
	volumeMounts = append(volumeMounts, globalPSVolumeMountDbus())
	volumeMounts = append(volumeMounts, globalPSVolumeMountOriginalPullSecret())

	if globalPullSecretName != "" {
		volumeMounts = append(volumeMounts, globalPSVolumeMountGlobalPullSecret())
	}

	return volumeMounts
}

// buildGlobalPSVolumes creates volumes for the GlobalPullSecret DaemonSet using util.BuildVolume pattern
func buildGlobalPSVolumes(globalPullSecretName string, originalPullSecretName string) []corev1.Volume {
	var volumes []corev1.Volume

	volumes = append(volumes, util.BuildVolume(globalPSVolumeKubeletConfig(), buildGlobalPSVolumeKubeletConfig))
	volumes = append(volumes, util.BuildVolume(globalPSVolumeDbus(), buildGlobalPSVolumeDbus))
	volumes = append(volumes, util.BuildVolume(globalPSVolumeOriginalPullSecret(), buildGlobalPSVolumeOriginalPullSecret(originalPullSecretName)))

	if globalPullSecretName != "" {
		volumes = append(volumes, util.BuildVolume(globalPSVolumeGlobalPullSecret(), buildGlobalPSVolumeGlobalPullSecret(globalPullSecretName)))
	}

	return volumes
}

func globalPSVolumeKubeletConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubelet-config",
	}
}

func globalPSVolumeDbus() *corev1.Volume {
	return &corev1.Volume{
		Name: "dbus",
	}
}

func globalPSVolumeOriginalPullSecret() *corev1.Volume {
	return &corev1.Volume{
		Name: "original-pull-secret",
	}
}

func globalPSVolumeGlobalPullSecret() *corev1.Volume {
	return &corev1.Volume{
		Name: "global-pull-secret",
	}
}

// Volume builder functions
func buildGlobalPSVolumeKubeletConfig(v *corev1.Volume) {
	v.HostPath = &corev1.HostPathVolumeSource{
		Path: "/var/lib/kubelet",
		Type: ptr.To(corev1.HostPathDirectory),
	}
}

func buildGlobalPSVolumeDbus(v *corev1.Volume) {
	v.HostPath = &corev1.HostPathVolumeSource{
		Path: "/var/run/dbus",
		Type: ptr.To(corev1.HostPathDirectory),
	}
}

func buildGlobalPSVolumeOriginalPullSecret(secretName string) func(v *corev1.Volume) {
	return func(v *corev1.Volume) {
		v.Secret = &corev1.SecretVolumeSource{
			SecretName: secretName,
		}
	}
}

func buildGlobalPSVolumeGlobalPullSecret(secretName string) func(v *corev1.Volume) {
	return func(v *corev1.Volume) {
		v.Secret = &corev1.SecretVolumeSource{
			SecretName: secretName,
			Optional:   ptr.To(true),
		}
	}
}

// Volume mount functions for GlobalPullSecret DaemonSet
func globalPSVolumeMountKubeletConfig() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      globalPSVolumeKubeletConfig().Name,
		MountPath: "/var/lib/kubelet",
	}
}

func globalPSVolumeMountDbus() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      globalPSVolumeDbus().Name,
		MountPath: "/var/run/dbus",
	}
}

func globalPSVolumeMountOriginalPullSecret() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      globalPSVolumeOriginalPullSecret().Name,
		MountPath: "/etc/original-pull-secret",
		ReadOnly:  true,
	}
}

func globalPSVolumeMountGlobalPullSecret() corev1.VolumeMount {
	return corev1.VolumeMount{
		Name:      globalPSVolumeGlobalPullSecret().Name,
		MountPath: "/etc/global-pull-secret",
		ReadOnly:  true,
	}
}
