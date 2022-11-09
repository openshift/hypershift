package inplaceupgrader

import (
	"context"
	"fmt"
	"strconv"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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
	// MachineConfigDaemonSchedulingKey is used to indicate that a node should schedule MCD pods during the upgrade
	MachineConfigDaemonSchedulingKey = "machineconfiguration.openshift.io/scheduleDaemon"

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

		// Remove the daemon schedule labelling for any remaining nodes that have it
		for _, node := range nodes {
			if _, hasLabel := node.Labels[MachineConfigDaemonSchedulingKey]; hasLabel {
				if result, err := r.CreateOrUpdate(ctx, r.guestClusterClient, node, func() error {
					delete(node.Labels, MachineConfigDaemonSchedulingKey)
					return nil
				}); err != nil {
					return fmt.Errorf("failed to remove MCD scheduling annotations: %w", err)
				} else {
					log.Info("Removed MCD scheduling annotations", "result", result)
				}
			}
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
		machineSet.Annotations[nodePoolAnnotationUpgradeInProgressTrue] = fmt.Sprintf("Updating version in progress. Target version: %q. Total Nodes: %d. Upgraded: %d", *machineSet.Spec.Template.Spec.Version, len(nodes), len(nodes)-nodeNeedUpgradeCount)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile MachineSet: %w", err)
	} else {
		log.Info("Reconciled MachineSet", "result", result)
	}

	// Create necessary upgrade manifests, if they do not exist
	err = r.reconcileInPlaceUpgradeManifests(ctx, r.guestClusterClient, targetConfigVersionHash, tokenSecret.Data[TokenSecretPayloadKey], nodePoolUpgradeAPI.spec.poolRef.GetName(), mcoImage)
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
	err = r.setNodesDesiredConfig(ctx, r.guestClusterClient, nodesToUpgrade, targetConfigVersionHash, mcoImage)
	if err != nil {
		return fmt.Errorf("failed to set hosted nodes for inplace upgrade: %w", err)
	}

	// Update the nodes that require MCD pods to schedule on them
	err = r.scheduleMachineConfigDaemonPods(ctx, r.guestClusterClient, nodes)
	if err != nil {
		return fmt.Errorf("failed to schedule daemon pods for inplace upgrade: %w", err)
	}
	return nil
}

func (r *Reconciler) setNodesDesiredConfig(ctx context.Context, hostedClusterClient client.Client, nodes []*corev1.Node, targetConfigVersionHash, mcoImage string) error {
	log := ctrl.LoggerFrom(ctx)

	for idx := range nodes {
		if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, nodes[idx], func() error {
			// Set the actual annotation
			nodes[idx].Annotations[DesiredMachineConfigAnnotationKey] = targetConfigVersionHash
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile node desired config annotations: %w", err)
		} else {
			log.Info("Reconciled Node desired config annotations", "result", result)
		}
	}
	return nil
}

// scheduleMachineConfigDaemonPods dynamically adds and removes the MachineConfigDaemonSchedulingKey from the node's labels,
// which the daemonset uses as a nodeSelector. This makes it so nodes that are not currently marked for updating do not have
// an idle pod running on them.
func (r *Reconciler) scheduleMachineConfigDaemonPods(ctx context.Context, hostedClusterClient client.Client, nodes []*corev1.Node) error {
	log := ctrl.LoggerFrom(ctx)

	for _, node := range nodes {
		if _, hasLabel := node.Labels[MachineConfigDaemonSchedulingKey]; hasLabel {
			if node.Annotations[CurrentMachineConfigAnnotationKey] == node.Annotations[DesiredMachineConfigAnnotationKey] &&
				node.Annotations[DesiredDrainerAnnotationKey] == node.Annotations[LastAppliedDrainerAnnotationKey] {
				// if it's current set to scheduling, we can unschedule if the node has the desired config, and has the desired drain/uncordon
				if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, node, func() error {
					delete(node.Labels, MachineConfigDaemonSchedulingKey)
					return nil
				}); err != nil {
					return fmt.Errorf("failed to remove MCD scheduling annotations: %w", err)
				} else {
					log.Info("Removed MCD scheduling annotations", "result", result)
				}
			}
		} else {
			if node.Annotations[CurrentMachineConfigAnnotationKey] != node.Annotations[DesiredMachineConfigAnnotationKey] ||
				node.Annotations[DesiredDrainerAnnotationKey] != node.Annotations[LastAppliedDrainerAnnotationKey] {
				// Schedule the pod
				if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, node, func() error {
					node.Labels[MachineConfigDaemonSchedulingKey] = ""
					return nil
				}); err != nil {
					return fmt.Errorf("failed to add MCD scheduling annotations: %w", err)
				} else {
					log.Info("Added MCD scheduling annotations", "result", result)
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

func deleteUpgradeManifests(ctx context.Context, hostedClusterClient client.Client, nodes []*corev1.Node, poolName string) error {
	// TODO (jerzhang): properly delete the other manifests. Right now we just delete the daemonset
	namespace := inPlaceUpgradeNamespace(poolName)
	daemonset := inPlaceUpgradeDaemonset(namespace.Name)
	if err := hostedClusterClient.Get(ctx, client.ObjectKeyFromObject(daemonset), daemonset); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("error getting upgrade daemonset: %w", err)
		}
	}
	if daemonset.DeletionTimestamp != nil {
		return nil
	}
	if err := hostedClusterClient.Delete(ctx, daemonset); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("error deleting upgrade daemonset: %w", err)
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

func (r *Reconciler) reconcileInPlaceUpgradeManifests(ctx context.Context, hostedClusterClient client.Client, targetConfigVersionHash string, payload []byte, poolName string, mcoImage string) error {
	log := ctrl.LoggerFrom(ctx)

	namespace := inPlaceUpgradeNamespace(poolName)
	if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, namespace, func() error {
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile upgrade Namespace for hash %s: %w", targetConfigVersionHash, err)
	} else {
		log.Info("Reconciled namespace", "result", result)
	}

	sa := inPlaceUpgradeDaemonServiceAccount(namespace.Name)
	if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, sa, func() error {
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile upgrade ServiceAccount for hash %s: %w", targetConfigVersionHash, err)
	} else {
		log.Info("Reconciled ServiceAccount", "result", result)
	}

	clusterRole := inPlaceUpgradeDaemonClusterRole(namespace.Name)
	if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, clusterRole, func() error {
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile upgrade ClusterRole for hash %s: %w", targetConfigVersionHash, err)
	} else {
		log.Info("Reconciled ClusterRole", "result", result)
	}

	clusterRoleBinding := inPlaceUpgradeDaemonClusterRoleBinding(namespace.Name)
	if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, clusterRoleBinding, func() error {
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile upgrade ClusterRoleBinding for hash %s: %w", targetConfigVersionHash, err)
	} else {
		log.Info("Reconciled ClusterRoleBinding", "result", result)
	}

	configmap := inPlaceUpgradeConfigMap(poolName, namespace.Name)
	if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, configmap, func() error {
		return r.reconcileUpgradeConfigmap(
			ctx, configmap, targetConfigVersionHash, payload,
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile upgrade ConfigMap for hash %s: %w", targetConfigVersionHash, err)
	} else {
		log.Info("Reconciled ConfigMap", "result", result)
	}

	// We will deploy the machine-config-daemon daemonset here, so it can be running for future operations
	daemonset := inPlaceUpgradeDaemonset(namespace.Name)
	if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, daemonset, func() error {
		return r.reconcileDaemonset(
			daemonset,
			poolName,
			mcoImage,
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile upgrade daemonset: %w", err)
	} else {
		log.Info("Reconciled upgrade daemonset", "result", result)
	}

	return nil
}

func (r *Reconciler) reconcileDaemonset(daemonset *appsv1.DaemonSet, poolName, mcoImage string) error {
	configmap := inPlaceUpgradeConfigMap(poolName, daemonset.Namespace)
	hostPathType := corev1.HostPathUnset
	daemonset.Spec.Template.ObjectMeta = metav1.ObjectMeta{
		Name: "machine-config-daemon",
		Labels: map[string]string{
			"k8s-app": "machine-config-daemon",
		},
	}
	daemonset.Spec.Template.Spec.Containers = []corev1.Container{
		{
			Name:  "machine-config-daemon",
			Image: mcoImage,
			Command: []string{
				"/usr/bin/machine-config-daemon",
			},
			Args: []string{
				"start",
				"--root-mount=/rootfs",
				"--kubeconfig=/var/lib/kubelet/kubeconfig",
				"--desired-configmap=/etc/machine-config-daemon-desired-config",
			},
			Env: []corev1.EnvVar{
				{
					Name: "NODE_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "spec.nodeName",
						},
					},
				},
			},
			TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			SecurityContext: &corev1.SecurityContext{
				Privileged: k8sutilspointer.BoolPtr(true),
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
	daemonset.Spec.Template.Spec.HostNetwork = true
	daemonset.Spec.Template.Spec.HostPID = true
	daemonset.Spec.Template.Spec.Tolerations = []corev1.Toleration{
		{
			Operator: corev1.TolerationOpExists,
		},
	}
	daemonset.Spec.Template.Spec.ServiceAccountName = "machine-config-daemon"
	daemonset.Spec.Template.Spec.NodeSelector = map[string]string{
		hyperv1.NodePoolLabel:            poolName,
		MachineConfigDaemonSchedulingKey: "",
	}
	daemonset.Spec.Template.Spec.Volumes = []corev1.Volume{
		{
			Name: "rootfs",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/",
					Type: &hostPathType,
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

	return nil
}

func (r *Reconciler) reconcileUpgradeConfigmap(ctx context.Context, configmap *corev1.ConfigMap, targetConfigVersionHash string, payload []byte) error {
	log := ctrl.LoggerFrom(ctx)

	// Base64-encode and gzip the payload to allow larger overall payload sizes.
	compressedPayload, err := util.CompressAndEncode(payload)
	if err != nil {
		return fmt.Errorf("could not compress payload: %w", err)
	}

	configmap.Data = map[string]string{
		"config": compressedPayload.String(),
		"hash":   targetConfigVersionHash,
	}

	log.Info("NodePool in place upgrade configmap synced", "target", targetConfigVersionHash)
	return nil
}

func (r *Reconciler) nodeToMachineSet(o client.Object) []reconcile.Request {
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
	if err := r.client.Get(context.TODO(), client.ObjectKeyFromObject(machine), machine); err != nil {
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

func inPlaceUpgradeDaemonset(namespace string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      fmt.Sprintf("machine-config-daemon-%s", namespace),
		},
		// This can't be mutated after creation
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"k8s-app": "machine-config-daemon",
				},
			},
		},
	}
}

func inPlaceUpgradeNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-upgrade", name),
			Labels: map[string]string{
				"pod-security.kubernetes.io/enforce": "privileged",
				"pod-security.kubernetes.io/audit":   "privileged",
				"pod-security.kubernetes.io/warn":    "privileged",
			},
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

func inPlaceUpgradeDaemonServiceAccount(namespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      "machine-config-daemon",
		},
	}
}

func inPlaceUpgradeDaemonClusterRole(namespace string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("machine-config-daemon-%s", namespace),
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups:     []string{"security.openshift.io"},
				ResourceNames: []string{"privileged"},
				Resources:     []string{"securitycontextconstraints"},
				Verbs:         []string{"use"},
			},
		},
	}
}

func inPlaceUpgradeDaemonClusterRoleBinding(namespace string) *rbacv1.ClusterRoleBinding {
	clusterRole := inPlaceUpgradeDaemonClusterRole(namespace)
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("machine-config-daemon-%s", namespace),
		},
		RoleRef: rbacv1.RoleRef{
			Kind:     "ClusterRole",
			APIGroup: "rbac.authorization.k8s.io",
			Name:     clusterRole.Name,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "machine-config-daemon",
				Namespace: namespace,
			},
		},
	}
}
