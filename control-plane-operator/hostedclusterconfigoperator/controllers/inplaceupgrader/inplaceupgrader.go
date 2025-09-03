package inplaceupgrader

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sutilspointer "k8s.io/utils/pointer"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// CurrentMachineConfigAnnotationKey is used to fetch current targetConfigVersionHash
	CurrentMachineConfigAnnotationKey = "machineconfiguration.openshift.io/currentConfig"
	// DesiredMachineConfigAnnotationKey is used to indicate the version a node should be updating to
	DesiredMachineConfigAnnotationKey = "machineconfiguration.openshift.io/desiredConfig"
	// MachineConfigDaemonStateAnnotationKey is used to fetch the state of the daemon on the machine.
	MachineConfigDaemonStateAnnotationKey = "machineconfiguration.openshift.io/state"
	// MachineConfigDaemonStateDegraded is set by daemon when an error not caused by a bad MachineConfig
	// is thrown during an upgrade.
	MachineConfigDaemonStateDegraded = "Degraded"
	// MachineConfigDaemonStateDone is set by daemon when the upgrade is done.
	MachineConfigDaemonStateDone = "Done"
	// MachineConfigDaemonMessageAnnotationKey is set by the daemon when it needs to report a human readable reason for its state. E.g. when state flips to degraded/unreconcilable.
	MachineConfigDaemonMessageAnnotationKey = "machineconfiguration.openshift.io/reason"
	// DesiredDrainerAnnotationKey is set by the MCD to indicate drain/uncordon requests
	DesiredDrainerAnnotationKey = "machineconfiguration.openshift.io/desiredDrain"
	// LastAppliedDrainerAnnotationKey is set by the controller to indicate the last request applied
	LastAppliedDrainerAnnotationKey = "machineconfiguration.openshift.io/lastAppliedDrain"
	// MachineConfigOperatorImage is the MCO image reference in the release payload
	MachineConfigOperatorImage = "machine-config-operator"

	// TODO (alberto): MachineSet CR annotations are used to communicate between the NodePool controller and the in-place upgrade controller.
	// This might eventually become a CRD equivalent to the struct nodePoolUpgradeAPI defined below.
	nodePoolAnnotationTargetConfigVersion    = "hypershift.openshift.io/nodePoolTargetConfigVersion"
	nodePoolAnnotationCurrentConfigVersion   = "hypershift.openshift.io/nodePoolCurrentConfigVersion"
	nodePoolAnnotationUpgradeInProgressTrue  = "hypershift.openshift.io/nodePoolUpgradeInProgressTrue"
	nodePoolAnnotationUpgradeInProgressFalse = "hypershift.openshift.io/nodePoolUpgradeInProgressFalse"
	nodePoolAnnotationMaxUnavailable         = "hypershift.openshift.io/nodePoolMaxUnavailable"

	TokenSecretPayloadKey = "payload"
	TokenSecretReleaseKey = "release"
)

type Reconciler struct {
	client             client.Client
	guestClusterClient client.Client
	releaseProvider    releaseinfo.Provider
	hcpName            string
	hcpNamespace       string
	upsert.CreateOrUpdateProvider
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	log.Info("Reconciling")

	// Fetch the MachineSet.
	machineSet := &capiv1.MachineSet{}
	err := r.client.Get(ctx, req.NamespacedName, machineSet)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("not found", "request", req.String())
			return ctrl.Result{}, nil
		}
		log.Error(err, "error getting MachineSet")
		return ctrl.Result{}, err
	}

	// Only in-place NodePool sets nodePoolAnnotationTargetConfigVersion on MachineSet.
	// Otherwise, we no-op.
	// TODO (alberto): add controller predicate to drop MachineSets owned by MachineDeployment.
	if _, ok := machineSet.Annotations[nodePoolAnnotationTargetConfigVersion]; !ok {
		log.V(3).Info("MachineSet has no target configVersion. No-op")
		return ctrl.Result{}, nil
	}

	if machineSet.Annotations[nodePoolAnnotationTargetConfigVersion] == machineSet.Annotations[nodePoolAnnotationCurrentConfigVersion] {
		log.V(3).Info("MachineSet is at configVersion. No-op", "configVersion", machineSet.Annotations[nodePoolAnnotationCurrentConfigVersion])
		return ctrl.Result{}, nil
	}

	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("token-%s-%s", machineSet.GetName(), machineSet.Annotations[nodePoolAnnotationTargetConfigVersion]),
			Namespace: machineSet.GetNamespace(),
		},
	}
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(tokenSecret), tokenSecret); err != nil {
		return ctrl.Result{}, err
	}
	if _, ok := tokenSecret.Data[TokenSecretPayloadKey]; !ok {
		log.V(3).Info("TokenSecret has no payload available yet for target configVersion. No-op", "configVersion", machineSet.Annotations[nodePoolAnnotationTargetConfigVersion])
		// TODO (alberto): Let controller watch token secrets?
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	hcp := manifests.HostedControlPlane(r.hcpNamespace, r.hcpName)
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(hcp), hcp); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get hosted control plane %q: %w", client.ObjectKeyFromObject(hcp), err)
	}
	proxy := globalconfig.ProxyConfig()
	globalconfig.ReconcileProxyConfigWithStatus(proxy, hcp)

	nodePoolUpgradeAPI := &nodePoolUpgradeAPI{
		spec: struct {
			targetConfigVersion string
			poolRef             *capiv1.MachineSet
		}{
			targetConfigVersion: machineSet.Annotations[nodePoolAnnotationTargetConfigVersion],
			poolRef:             machineSet,
		},
		status: struct {
			currentConfigVersion string
		}{
			currentConfigVersion: machineSet.Annotations[nodePoolAnnotationCurrentConfigVersion],
		},
		proxy: proxy,
	}

	mcoImage, err := r.getPayloadImage(ctx, MachineConfigOperatorImage)
	if err != nil {
		return ctrl.Result{}, err
	}
	log.Info("discovered mco image", "image", mcoImage)

	return ctrl.Result{}, r.reconcileInPlaceUpgrade(ctx, nodePoolUpgradeAPI, tokenSecret, mcoImage)
}

type nodePoolUpgradeAPI struct {
	spec struct {
		targetConfigVersion string
		poolRef             *capiv1.MachineSet
	}
	status struct {
		currentConfigVersion string
	}
	proxy *configv1.Proxy
}

// reconcileInPlaceUpgrade loops over all Nodes that belong to a NodePool and performs an in place upgrade if necessary.
func (r *Reconciler) reconcileInPlaceUpgrade(ctx context.Context, nodePoolUpgradeAPI *nodePoolUpgradeAPI, tokenSecret *corev1.Secret, mcoImage string) error {
	log := ctrl.LoggerFrom(ctx)

	currentConfigVersionHash := nodePoolUpgradeAPI.status.currentConfigVersion
	targetConfigVersionHash := nodePoolUpgradeAPI.spec.targetConfigVersion
	if targetConfigVersionHash == currentConfigVersionHash {
		return nil
	}
	machineSet := nodePoolUpgradeAPI.spec.poolRef

	nodes, err := getNodesForMachineSet(ctx, r.client, r.guestClusterClient, machineSet)
	if err != nil {
		return err
	}

	// If all Nodes are atVersion.
	if inPlaceUpgradeComplete(nodes, currentConfigVersionHash, targetConfigVersionHash) {
		// This pool should be at steady state, in which case, let's check and delete the upgrade manifests
		// if any exists
		if err := deleteUpgradeManifests(ctx, r.guestClusterClient, nodes, nodePoolUpgradeAPI.spec.poolRef.GetName()); err != nil {
			return err
		}

		// Signal in-place upgrade complete.
		result, err := r.CreateOrUpdate(ctx, r.client, machineSet, func() error {
			machineSet.Annotations[nodePoolAnnotationCurrentConfigVersion] = targetConfigVersionHash
			delete(machineSet.Annotations, nodePoolAnnotationUpgradeInProgressTrue)
			delete(machineSet.Annotations, nodePoolAnnotationUpgradeInProgressFalse)
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile MachineSet: %w", err)
		} else {
			log.Info("Reconciled MachineSet", "result", result)
		}

		return nil
	}

	// This check comes after the completion, so if no upgrades are in progress, if a node is degraded for
	// whatever reason, we will not know until the next upgrade, at which point hopefully the MCD is able
	// to reconcile
	// TODO (jerzhang): differentiate between NodePoolUpdatingVersionConditionType and NodePoolUpdatingConfigConditionType
	nodeNeedUpgradeCount := 0
	for _, node := range nodes {
		if node.Annotations[MachineConfigDaemonStateAnnotationKey] == MachineConfigDaemonStateDegraded {
			// Signal in-place upgrade degraded.
			result, err := r.CreateOrUpdate(ctx, r.client, machineSet, func() error {
				delete(machineSet.Annotations, nodePoolAnnotationUpgradeInProgressTrue)
				machineSet.Annotations[nodePoolAnnotationUpgradeInProgressFalse] = fmt.Sprintf("Node %s in nodepool degraded: %v", node.Name, node.Annotations[MachineConfigDaemonMessageAnnotationKey])
				return nil
			})
			if err != nil {
				return fmt.Errorf("failed to reconcile MachineSet: %w", err)
			} else {
				log.Info("Reconciled MachineSet", "result", result)
			}

			return fmt.Errorf("degraded node found, cannot progress in-place upgrade. Degraded reason: %v", node.Annotations[MachineConfigDaemonMessageAnnotationKey])
		}

		if nodeNeedsUpgrade(node, currentConfigVersionHash, targetConfigVersionHash) {
			nodeNeedUpgradeCount++
		}
	}

	// Signal in-place upgrade progress.
	result, err := r.CreateOrUpdate(ctx, r.client, machineSet, func() error {
		delete(machineSet.Annotations, nodePoolAnnotationUpgradeInProgressFalse)
		machineSet.Annotations[nodePoolAnnotationUpgradeInProgressTrue] = fmt.Sprintf("Nodepool update in progress. Target Config version: %s. Total Nodes: %d. Upgraded: %d", targetConfigVersionHash, len(nodes), len(nodes)-nodeNeedUpgradeCount)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile MachineSet: %w", err)
	} else {
		log.Info("Reconciled MachineSet", "result", result)
	}

	// Create necessary upgrade manifests, if they do not exist
	err = r.reconcileInPlaceUpgradeManifests(ctx, r.guestClusterClient, targetConfigVersionHash, tokenSecret.Data[TokenSecretPayloadKey], nodePoolUpgradeAPI.spec.poolRef.GetName())
	if err != nil {
		return fmt.Errorf("failed to create upgrade manifests in hosted cluster: %w", err)
	}

	// Find nodes that can be upgraded
	maxUnavail := 1
	if maxUnavailAnno, ok := machineSet.Annotations[nodePoolAnnotationMaxUnavailable]; ok {
		maxUnavail, err = strconv.Atoi(maxUnavailAnno)
		if err != nil {
			return fmt.Errorf("error getting max unavailable count from MachineSet annotation: %w", err)
		}
	}
	nodesToUpgrade := getNodesToUpgrade(nodes, targetConfigVersionHash, maxUnavail)
	err = r.setNodesDesiredConfig(ctx, r.guestClusterClient, nodePoolUpgradeAPI.spec.poolRef.GetName(), nodesToUpgrade, targetConfigVersionHash)
	if err != nil {
		return fmt.Errorf("failed to set hosted nodes for inplace upgrade: %w", err)
	}

	err = r.reconcileUpgradePods(ctx, r.guestClusterClient, nodes, nodePoolUpgradeAPI.spec.poolRef.GetName(), mcoImage, nodePoolUpgradeAPI.proxy)
	if err != nil {
		return fmt.Errorf("failed to delete idle upgrade pods: %w", err)
	}
	return nil
}

func (r *Reconciler) setNodesDesiredConfig(ctx context.Context, hostedClusterClient client.Client, poolName string, nodes []*corev1.Node, targetConfigVersionHash string) error {
	log := ctrl.LoggerFrom(ctx)

	for _, node := range nodes {
		if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, node, func() error {
			// Set the actual annotation
			node.Annotations[DesiredMachineConfigAnnotationKey] = targetConfigVersionHash
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile node desired config annotations: %w", err)
		} else {
			log.Info("Reconciled Node desired config annotations", "result", result)
		}
	}
	return nil
}

// reconcileUpgradePods checks if any Machine Config Daemon pods are running on nodes that are not currently performing an update
func (r *Reconciler) reconcileUpgradePods(ctx context.Context, hostedClusterClient client.Client, nodes []*corev1.Node, poolName, mcoImage string, proxy *configv1.Proxy) error {
	log := ctrl.LoggerFrom(ctx)

	for _, node := range nodes {
		// Set the upgrade pod
		// TODO (jerzhang): maybe this can be a daemonset instead, since we are using a state machine MCD now
		// There are also considerations on how to properly handle multiple upgrades, or to force upgrades
		// on degraded nodes, etc.
		namespace := inPlaceUpgradeNamespace(poolName)
		pod := inPlaceUpgradePod(namespace.Name, node.Name)

		if node.Annotations[CurrentMachineConfigAnnotationKey] == node.Annotations[DesiredMachineConfigAnnotationKey] &&
			node.Annotations[DesiredDrainerAnnotationKey] == node.Annotations[LastAppliedDrainerAnnotationKey] {
			// the node is updated and does not require a MCD running
			if err := hostedClusterClient.Get(ctx, client.ObjectKeyFromObject(pod), pod); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				return fmt.Errorf("error getting upgrade MCD pod: %w", err)
			}
			if pod.DeletionTimestamp != nil {
				continue
			}
			if err := hostedClusterClient.Delete(ctx, pod); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				return fmt.Errorf("error deleting upgrade MCD pod: %w", err)
			}
			log.Info("Deleted idle upgrade pod")
		} else {
			if err := hostedClusterClient.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}, pod); err != nil {
				if !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to get upgrade pod for node %s: %w", node.Name, err)
				}
				pod := inPlaceUpgradePod(namespace.Name, node.Name)
				if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, pod, func() error {
					return r.createUpgradePod(
						pod,
						node.Name,
						poolName,
						mcoImage,
						proxy,
					)
				}); err != nil {
					return fmt.Errorf("failed to create upgrade pod for node %s: %w", node.Name, err)
				} else {
					log.Info("create upgrade pod", "result", result)
				}
			}
		}
	}
	return nil
}

// getPayloadImage gets the specified image reference from the payload
func (r *Reconciler) getPayloadImage(ctx context.Context, imageName string) (string, error) {
	hcp := manifests.HostedControlPlane(r.hcpNamespace, r.hcpName)
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(hcp), hcp); err != nil {
		return "", fmt.Errorf("failed to get hosted control plane %s/%s: %w", r.hcpNamespace, r.hcpName, err)
	}

	pullSecret := manifests.PullSecret(hcp.Namespace)
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(pullSecret), pullSecret); err != nil {
		return "", fmt.Errorf("failed to get pull secret: %w", err)
	}

	releaseImage, err := r.releaseProvider.Lookup(ctx, hcp.Spec.ReleaseImage, pullSecret.Data[corev1.DockerConfigJsonKey])
	if err != nil {
		return "", fmt.Errorf("failed to get lookup release image %s: %w", hcp.Spec.ReleaseImage, err)
	}

	image, hasImage := releaseImage.ComponentImages()[imageName]
	if !hasImage {
		return "", fmt.Errorf("release image does not contain %s (images: %v)", imageName, releaseImage.ComponentImages())
	}
	return image, nil
}

func (r *Reconciler) createUpgradePod(pod *corev1.Pod, nodeName, poolName, mcoImage string, proxy *configv1.Proxy) error {
	configmap := inPlaceUpgradeConfigMap(poolName, pod.Namespace)
	pod.Spec.Containers = []corev1.Container{
		{
			Name:  "machine-config-daemon",
			Image: mcoImage,
			Command: []string{
				"/usr/bin/machine-config-daemon",
			},
			Args: []string{
				"start",
				"--node-name=" + nodeName,
				"--root-mount=/rootfs",
				"--kubeconfig=/var/lib/kubelet/kubeconfig",
				"--desired-configmap=/etc/machine-config-daemon-desired-config",
			},
			TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			SecurityContext: &corev1.SecurityContext{
				Privileged: k8sutilspointer.Bool(true),
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "rootfs",
					MountPath: "/rootfs",
				},
				{
					Name:      "desired-config-mount",
					MountPath: "/rootfs/etc/machine-config-daemon-desired-config",
				},
			},
		},
	}
	pod.Spec.HostNetwork = true
	pod.Spec.HostPID = true
	pod.Spec.Tolerations = []corev1.Toleration{
		{
			Operator: corev1.TolerationOpExists,
		},
	}
	pod.Spec.NodeSelector = map[string]string{
		"kubernetes.io/hostname": nodeName,
	}
	pod.Spec.Volumes = []corev1.Volume{
		{
			Name: "rootfs",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/",
				},
			},
		},
		{
			Name: "desired-config-mount",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: configmap.Name,
					},
				},
			},
		},
	}
	pod.Spec.RestartPolicy = corev1.RestartPolicyOnFailure

	// Add proxy environment variables if they are set
	proxyEnvVars := proxyToEnvVars(proxy)
	if len(proxyEnvVars) > 0 && len(pod.Spec.Containers) > 0 {
		pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, proxyEnvVars...)
	}

	return nil
}

func proxyToEnvVars(proxy *configv1.Proxy) []corev1.EnvVar {
	envVars := []corev1.EnvVar{}
	if proxy != nil {
		if proxy.Status.HTTPProxy != "" {
			envVars = append(envVars, corev1.EnvVar{
				Name:  "HTTP_PROXY",
				Value: proxy.Status.HTTPProxy,
			})
		}
		if proxy.Status.HTTPSProxy != "" {
			envVars = append(envVars, corev1.EnvVar{
				Name:  "HTTPS_PROXY",
				Value: proxy.Status.HTTPSProxy,
			})
		}
		if proxy.Status.NoProxy != "" {
			envVars = append(envVars, corev1.EnvVar{
				Name:  "NO_PROXY",
				Value: proxy.Status.NoProxy,
			})
		}
	}
	return envVars
}

func deleteUpgradeManifests(ctx context.Context, hostedClusterClient client.Client, nodes []*corev1.Node, poolName string) error {
	// TODO (jerzhang): maybe add a tracker for pods, so we can also use it to sync status
	// For now attempt to delete all the pods if we are in a done state
	// TODO (jerzhang): properly delete the other manifests. Right now we just delete the pods
	namespace := inPlaceUpgradeNamespace(poolName)
	for _, node := range nodes {
		pod := inPlaceUpgradePod(namespace.Name, node.Name)
		if err := hostedClusterClient.Get(ctx, client.ObjectKeyFromObject(pod), pod); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("error getting upgrade MCD pod: %w", err)
		}
		if pod.DeletionTimestamp != nil {
			continue
		}
		if err := hostedClusterClient.Delete(ctx, pod); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("error deleting upgrade MCD pod: %w", err)
		}
	}
	return nil
}

func getNodesToUpgrade(nodes []*corev1.Node, targetConfig string, maxUnavailable int) []*corev1.Node {
	// First, get nodes depending on how much capacity we have for additional updates
	capacity := getCapacity(nodes, targetConfig, maxUnavailable)
	availableCandidates := getAvailableCandidates(nodes, targetConfig, capacity)

	// Next, we get the currently updating candidates, that aren't targetting the latest config
	alreadyUnavailableNodes := getAlreadyUnavailableCandidates(nodes, targetConfig)

	return append(availableCandidates, alreadyUnavailableNodes...)
}

func getCapacity(nodes []*corev1.Node, targetConfig string, maxUnavailable int) int {
	// get how many machines we can update based on maxUnavailable
	// In the MCO logic, unavailable is defined as any of:
	// - config does not match
	// - MCD is failing
	// - Node is unscheduleable
	// - NodeReady condition status is ConditionTrue,
	// - NodeDiskPressure condition status is ConditionFalse,
	// - NodeNetworkUnavailable condition status is ConditionFalse.
	// TODO (jerzhang): consider what we want to do with node status here
	// For now, we will just check current/desired config to see if any nodes is already updating
	var numUnavailable int
	for _, node := range nodes {
		if node.Annotations[CurrentMachineConfigAnnotationKey] != node.Annotations[DesiredMachineConfigAnnotationKey] {
			numUnavailable++
		}
	}
	return maxUnavailable - numUnavailable
}

// getAlreadyUnavailableCandidates returns nodes that are updating, but don't have the latest config.
// Compared to self-driving OCP, there is an additional scenario to consider here.
// Since the ConfigMap contents are synced separately, those will change on the fly in the pod.
// Meaning that we could have a scenario where there are multiple queue'ed updates, in which case
// the MCD will just jump straight to the latest version.
// This will cause the MCD to softlock, so let's make sure for those unavailable nodes, we are also
// update their desired configuration. The MCD should be able to reconcile these changes on the fly.
func getAlreadyUnavailableCandidates(nodes []*corev1.Node, targetConfig string) []*corev1.Node {
	var candidateNodes []*corev1.Node
	for _, node := range nodes {
		if node.Annotations[CurrentMachineConfigAnnotationKey] != node.Annotations[DesiredMachineConfigAnnotationKey] &&
			node.Annotations[DesiredMachineConfigAnnotationKey] != targetConfig {
			candidateNodes = append(candidateNodes, node)
		}
	}
	return candidateNodes
}

func getAvailableCandidates(nodes []*corev1.Node, targetConfig string, capacity int) []*corev1.Node {
	if capacity < 1 {
		return nil
	}

	// We only look at nodes which aren't already targeting our desired config,
	// and do not have an ongoing update
	var candidateNodes []*corev1.Node
	for _, node := range nodes {
		if node.Annotations[DesiredMachineConfigAnnotationKey] != targetConfig &&
			node.Annotations[CurrentMachineConfigAnnotationKey] == node.Annotations[DesiredMachineConfigAnnotationKey] {
			candidateNodes = append(candidateNodes, node)
		}
	}

	if len(candidateNodes) == 0 {
		return nil
	}

	// TODO(jerzhang): do some ordering here
	return candidateNodes[:capacity]
}

func (r *Reconciler) reconcileInPlaceUpgradeManifests(ctx context.Context, hostedClusterClient client.Client, targetConfigVersionHash string, compressedAndEncodedPayload []byte, poolName string) error {
	log := ctrl.LoggerFrom(ctx)

	namespace := inPlaceUpgradeNamespace(poolName)
	if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, namespace, func() error {
		if namespace.Labels == nil {
			namespace.Labels = map[string]string{}
		}
		namespace.Labels["security.openshift.io/scc.podSecurityLabelSync"] = "false"
		namespace.Labels["pod-security.kubernetes.io/enforce"] = "privileged"
		namespace.Labels["pod-security.kubernetes.io/audit"] = "privileged"
		namespace.Labels["pod-security.kubernetes.io/warn"] = "privileged"
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile upgrade Namespace for hash %s: %w", targetConfigVersionHash, err)
	} else {
		log.Info("Reconciled namespace", "result", result)
	}

	configmap := inPlaceUpgradeConfigMap(poolName, namespace.Name)
	if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, configmap, func() error {
		return r.reconcileUpgradeConfigmap(
			ctx, configmap, targetConfigVersionHash, compressedAndEncodedPayload,
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile upgrade ConfigMap for hash %s: %w", targetConfigVersionHash, err)
	} else {
		log.Info("Reconciled ConfigMap", "result", result)
	}
	return nil
}

func (r *Reconciler) reconcileUpgradeConfigmap(ctx context.Context, configmap *corev1.ConfigMap, targetConfigVersionHash string, compressedAndEncodedPayload []byte) error {
	log := ctrl.LoggerFrom(ctx)

	configmap.Data = map[string]string{
		"config": string(compressedAndEncodedPayload),
		"hash":   targetConfigVersionHash,
	}

	log.Info("NodePool in place upgrade configmap synced", "target", targetConfigVersionHash)
	return nil
}

func (r *Reconciler) nodeToMachineSet(ctx context.Context, o client.Object) []reconcile.Request {
	node, ok := o.(*corev1.Node)
	if !ok {
		panic(fmt.Sprintf("Expected a Node but got a %T", o))
	}

	machineName, ok := node.GetAnnotations()[capiv1.MachineAnnotation]
	if !ok {
		return nil
	}

	// Match by namespace when the node has the annotation.
	machineNamespace, ok := node.GetAnnotations()[capiv1.ClusterNamespaceAnnotation]
	if !ok {
		return nil
	}

	// Match by nodeName and status.nodeRef.name.
	machine := &capiv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineNamespace,
			Name:      machineName,
		},
	}
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(machine), machine); err != nil {
		return nil
	}

	machineOwner := metav1.GetControllerOf(machine)
	if machineOwner == nil {
		return nil
	}
	if machineOwner.Kind != "MachineSet" {
		return nil
	}

	return []reconcile.Request{{NamespacedName: types.NamespacedName{
		Name:      machineOwner.Name,
		Namespace: machineNamespace,
	}}}
}

func getNodesForMachineSet(ctx context.Context, c client.Reader, hostedClusterClient client.Client, machineSet *capiv1.MachineSet) ([]*corev1.Node, error) {
	selectorMap, err := metav1.LabelSelectorAsMap(&machineSet.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("failed to convert MachineSet %q label selector to a map: %w", machineSet.Name, err)
	}

	// Get all Machines linked to this MachineSet.
	allMachines := &capiv1.MachineList{}
	if err = c.List(ctx,
		allMachines,
		client.InNamespace(machineSet.Namespace),
		client.MatchingLabels(selectorMap),
	); err != nil {
		return nil, fmt.Errorf("failed to list machines: %w", err)
	}

	var machineSetOwnedMachines []capiv1.Machine
	for i, machine := range allMachines.Items {
		if metav1.GetControllerOf(&machine) != nil && metav1.IsControlledBy(&machine, machineSet) {
			machineSetOwnedMachines = append(machineSetOwnedMachines, allMachines.Items[i])
		}
	}

	var nodes []*corev1.Node
	for _, machine := range machineSetOwnedMachines {
		if machine.Status.NodeRef != nil {
			node := &corev1.Node{}
			if err := hostedClusterClient.Get(ctx, client.ObjectKey{Name: machine.Status.NodeRef.Name}, node); err != nil {
				return nil, fmt.Errorf("error getting node: %w", err)
			}
			nodes = append(nodes, node)
		}
	}

	return nodes, nil
}

func nodeNeedsUpgrade(node *corev1.Node, currentConfigVersion, targetConfigVersion string) bool {
	if node.Annotations[DesiredDrainerAnnotationKey] != node.Annotations[LastAppliedDrainerAnnotationKey] {
		// Node needs drain/cordon (last node not yet cordoned, but versions are all upgraded)
		return true
	}

	if node.Annotations[CurrentMachineConfigAnnotationKey] == "" && currentConfigVersion == targetConfigVersion {
		// No previous upgrade and no upgrade required
		return false
	}

	if node.Annotations[CurrentMachineConfigAnnotationKey] != targetConfigVersion {
		return true
	}

	return node.Annotations[MachineConfigDaemonStateAnnotationKey] != MachineConfigDaemonStateDone
}

// This tracks annotations written by the MCD pod
func inPlaceUpgradeComplete(nodes []*corev1.Node, currentConfigVersion string, targetConfigVersion string) bool {
	// TODO (Alberto): account for number of expected Nodes here otherwise a brand new NodePool which yet has no Nodes
	// reports complete.
	for _, node := range nodes {
		if nodeNeedsUpgrade(node, currentConfigVersion, targetConfigVersion) {
			return false
		}
	}

	return true
}

func inPlaceUpgradePod(namespace, nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      fmt.Sprintf("machine-config-daemon-%s", nodeName),
		},
	}
}

func inPlaceUpgradeNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-upgrade", name),
		},
	}
}

func inPlaceUpgradeConfigMap(poolName string, namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      fmt.Sprintf("%s-upgrade", poolName),
		},
	}
}
