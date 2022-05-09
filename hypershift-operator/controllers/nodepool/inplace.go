package nodepool

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"

	api "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	hyperutil "github.com/openshift/hypershift/hypershift-operator/controllers/util"
	"github.com/openshift/hypershift/support/labelenforcingclient"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	k8sutilspointer "k8s.io/utils/pointer"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
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
	// DrainerStateUncordon is used for drainer annotation as a value to indicate needing an uncordon
	DrainerStateUncordon = "uncordon"
	// TODO (yuqi-zhang): implement drain
	// DrainerStateDrain = "drain"

	TokenSecretPayloadKey = "payload"
)

// reconcileInPlaceUpgrade loops over all Nodes that belong to a NodePool and performs an in place upgrade if necessary.
func (r *NodePoolReconciler) reconcileInPlaceUpgrade(ctx context.Context, hc *hyperv1.HostedCluster, nodePool *hyperv1.NodePool, machineSet *capiv1.MachineSet, targetConfigHash, targetVersion, targetConfigVersionHash string, tokenSecret *corev1.Secret) error {
	log := ctrl.LoggerFrom(ctx)

	// If there's no guest cluster yet return early.
	if hc.Status.KubeConfig == nil {
		log.Info("HostedCluster has no Kubeconfig yet")
		return nil
	}

	hostedClusterAvailable := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.HostedClusterAvailable))
	if hostedClusterAvailable.Status != metav1.ConditionTrue {
		log.Info("HostedCluster is not available yet")
		return nil
	}

	if _, ok := tokenSecret.Data[TokenSecretPayloadKey]; !ok {
		log.Info("TokenSecret has not payload yet")
		return nil
	}

	// Watch hosted cluster Nodes. We track the created caches, so we don't add a watcher on every reconciliation.
	// This cache might be accessed in parallel by multiple reconciliations on the same cache key if
	// a hostedcluster has multiple nodepoool, thus we do not use a RWLock.
	r.hostedClusterCachesTracker.Lock()
	defer r.hostedClusterCachesTracker.Unlock()
	cacheEntry, ok := r.hostedClusterCachesTracker.caches[client.ObjectKeyFromObject(hc)]
	if !ok {
		hostedClusterCache, hostedClusterClient, err := newHostedClusterCacheAndClient(ctx, r.Client, hc)
		if err != nil {
			return fmt.Errorf("failed to create hosted cluster cache: %w", err)
		}

		cacheCtx, cacheCtxCancel := context.WithCancel(ctx)
		// TODO: index by HC here instead?
		if r.hostedClusterCachesTracker.caches == nil {
			r.hostedClusterCachesTracker.caches = make(map[client.ObjectKey]cacheWithCancel)
		}
		// Avoid leaking the ctx if we error out
		defer func() {
			if cacheEntry.cancel == nil {
				cacheCtxCancel()
			}
		}()

		go hostedClusterCache.Start(cacheCtx)
		if !hostedClusterCache.WaitForCacheSync(cacheCtx) {
			return fmt.Errorf("failed waiting for hosted cluster cache to sync: %w", err)
		}

		if err := r.controller.Watch(source.NewKindWithCache(&corev1.Node{}, hostedClusterCache), handler.EnqueueRequestsFromMapFunc(r.nodeToNodePool)); err != nil {
			return fmt.Errorf("error adding watcher for hosted cluster nodes: %w", err)
		}

		cacheEntry = cacheWithCancel{
			cache:  hostedClusterCache,
			client: hostedClusterClient,
			cancel: cacheCtxCancel,
		}
		r.hostedClusterCachesTracker.caches[client.ObjectKeyFromObject(hc)] = cacheEntry
		log.Info("Created hosted cluster cache")

	}

	hostedClusterClient := cacheEntry.client

	nodes, err := getNodesForMachineSet(ctx, r.Client, hostedClusterClient, machineSet)
	if err != nil {
		return err
	}

	// If all Nodes are atVersion
	if inPlaceUpgradeComplete(nodes, nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion], targetConfigVersionHash) {
		if nodePool.Status.Version != targetVersion {
			log.Info("Version upgrade complete",
				"previous", nodePool.Status.Version, "new", targetVersion)
			nodePool.Status.Version = targetVersion
		}

		if nodePool.Annotations == nil {
			nodePool.Annotations = make(map[string]string)
		}
		if nodePool.Annotations[nodePoolAnnotationCurrentConfig] != targetConfigHash {
			log.Info("Config upgrade complete",
				"previous", nodePool.Annotations[nodePoolAnnotationCurrentConfig], "new", targetConfigHash)

			nodePool.Annotations[nodePoolAnnotationCurrentConfig] = targetConfigHash
		}
		nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = targetConfigVersionHash

		// This pool should be at steady state, in which case, let's check and delete the upgrade manifests
		// if any exists
		if err := deleteUpgradeManifests(ctx, hostedClusterClient, nodes, nodePool); err != nil {
			return err
		}
		return nil
	}

	// This check comes after the completion, so if no upgrades are in progress, if a node is degraded for
	// whatever reason, we will not know until the next upgrade, at which point hopefully the MCD is able
	// to reconcile
	// TODO (jerzhang): differenciate between NodePoolUpdatingVersionConditionType and NodePoolUpdatingConfigConditionType
	nodeNeedUpgradeCount := 0
	for _, node := range nodes {
		if node.Annotations[MachineConfigDaemonStateAnnotationKey] == MachineConfigDaemonStateDegraded {
			setStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolUpdatingVersionConditionType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolInplaceUpgradeFailedConditionReason,
				Message:            fmt.Sprintf("Node %s in nodepool degraded: %v", node.Name, node.Annotations[MachineConfigDaemonMessageAnnotationKey]),
				ObservedGeneration: nodePool.Generation,
			})
			return fmt.Errorf("degraded node found, cannot progress in-place upgrade. Degraded reason: %v", node.Annotations[MachineConfigDaemonMessageAnnotationKey])
		}

		if nodeNeedsUpgrade(node, nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion], targetConfigVersionHash) {
			nodeNeedUpgradeCount++
		}
	}
	setStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolUpdatingVersionConditionType,
		Status:             corev1.ConditionTrue,
		Reason:             hyperv1.NodePoolAsExpectedConditionReason,
		Message:            fmt.Sprintf("Updating version in progress. Target version: %s. Total Nodes: %d. Upgraded: %d", targetVersion, len(nodes), len(nodes)-nodeNeedUpgradeCount),
		ObservedGeneration: nodePool.Generation,
	})

	// Create necessary upgrade manifests, if they do not exist
	err = r.reconcileInPlaceUpgradeManifests(ctx, hostedClusterClient, targetConfigVersionHash, tokenSecret.Data[TokenSecretPayloadKey], nodePool)
	if err != nil {
		return fmt.Errorf("failed to create upgrade manifests in hosted cluster: %w", err)
	}

	// Check the nodes to see if any need our help to progress drain
	// TODO (jerzhang): actually implement drain logic, likely as separate goroutines to monitor success
	// TODO (jerzhang): consider what happens if the desiredConfig has changed since the node last upgraded
	for idx := range nodes {
		if _, err := r.CreateOrUpdate(ctx, hostedClusterClient, nodes[idx], func() error {
			// TODO (jerzhang): delete the pod after we uncordon
			// desiredVerb := strings.Split(desiredState, "-")[0]
			// if desiredVerb == DrainerStateUncordon {
			// }

			// TODO (jerzhang): actually implement the node draining. For now, just set the singal and pretend we drained.
			if nodes[idx].Annotations == nil {
				nodes[idx].Annotations = map[string]string{}
			}
			nodes[idx].Annotations[LastAppliedDrainerAnnotationKey] = nodes[idx].Annotations[DesiredDrainerAnnotationKey]
			return nil
		}); err != nil {
			return fmt.Errorf("failed to create upgrade manifests in hosted cluster: %w", err)
		}
		// TODO (jerzhang): in the future, consider exiting here and let future syncs handle post-drain functions
	}

	// Find nodes that can be upgraded
	// TODO (jerzhang): add logic to honor maxUnavailable/maxSurge
	nodesToUpgrade := getNodesToUpgrade(nodes, targetConfigVersionHash, 1)
	err = r.performNodesUpgrade(ctx, hostedClusterClient, nodePool, nodesToUpgrade, targetConfigVersionHash)
	if err != nil {
		return fmt.Errorf("failed to set hosted nodes for inplace upgrade: %w", err)
	}

	return nil
}

func (r *NodePoolReconciler) performNodesUpgrade(ctx context.Context, hostedClusterClient client.Client, nodePool *hyperv1.NodePool, nodes []*corev1.Node, targetConfigVersionHash string) error {
	log := ctrl.LoggerFrom(ctx)

	for idx, node := range nodes {
		// Set the upgrade pod
		// TODO (jerzhang): maybe this can be a daemonset instead, since we are using a state machine MCD now
		// There are also considerations on how to properly handle multiple upgrades, or to force upgrades
		// on degraded nodes, etc.
		namespace := inPlaceUpgradeNamespace(nodePool)
		pod := inPlaceUpgradePod(namespace.Name, node.Name)
		if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, pod, func() error {
			return r.reconcileUpgradePod(
				pod,
				node.Name,
				nodePool,
			)
		}); err != nil {
			return fmt.Errorf("failed to reconcile upgrade pod for node %s: %w", node.Name, err)
		} else {
			log.Info("Reconciled upgrade pod", "result", result)
		}

		if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, nodes[idx], func() error {
			// Set the actual annotation
			nodes[idx].Annotations[DesiredMachineConfigAnnotationKey] = targetConfigVersionHash
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile node drain annotations: %w", err)
		} else {
			log.Info("Reconciled Node drain annotations", "result", result)
		}
	}
	return nil
}

func (r *NodePoolReconciler) reconcileUpgradePod(pod *corev1.Pod, nodeName string, nodePool *hyperv1.NodePool) error {
	// TODO (jerzhang): unhardcode some of this
	configmap := inPlaceUpgradeConfigMap(nodePool, pod.Namespace)
	pod.Spec.Containers = []corev1.Container{
		{
			Name: "machine-config-daemon",
			// TODO (jerzhang): switch this to MCO image once we have it ready
			Image: "quay.io/jerzhang/hypershiftdaemon:latest",
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

	return nil
}

func deleteUpgradeManifests(ctx context.Context, hostedClusterClient client.Client, nodes []*corev1.Node, nodePool *hyperv1.NodePool) error {
	// TODO (jerzhang): maybe add a tracker for pods, so we can also use it to sync status
	// For now attempt to delete all the pods if we are in a done state
	// TODO (jerzhang): properly delete the other manifests. Right now we just delete the pods
	namespace := inPlaceUpgradeNamespace(nodePool)
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
	// get unavailable machines
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

	capacity := maxUnavailable - numUnavailable
	// If we're at capacity, there's nothing to do.
	if capacity < 1 {
		return nil
	}
	// We only look at nodes which aren't already targeting our desired config
	var candidateNodes []*corev1.Node
	for _, node := range nodes {
		if node.Annotations[DesiredMachineConfigAnnotationKey] != targetConfig {
			candidateNodes = append(candidateNodes, node)
		}
	}

	if len(candidateNodes) == 0 {
		return nil
	}

	// Not sure if we need to order this
	return candidateNodes[:capacity]
}

func (r *NodePoolReconciler) reconcileInPlaceUpgradeManifests(ctx context.Context, hostedClusterClient client.Client, targetConfigVersionHash string, payload []byte, nodePool *hyperv1.NodePool) error {
	log := ctrl.LoggerFrom(ctx)

	namespace := inPlaceUpgradeNamespace(nodePool)
	if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, namespace, func() error {
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile upgrade Namespace for hash %s: %w", targetConfigVersionHash, err)
	} else {
		log.Info("Reconciled namespace", "result", result)
	}

	configmap := inPlaceUpgradeConfigMap(nodePool, namespace.Name)
	if result, err := r.CreateOrUpdate(ctx, hostedClusterClient, configmap, func() error {
		return r.reconcileUpgradeConfigmap(
			ctx, configmap, targetConfigVersionHash, payload,
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile upgrade ConfigMap for hash %s: %w", targetConfigVersionHash, err)
	} else {
		log.Info("Reconciled ConfigMap", "result", result)
	}
	return nil
}

func (r *NodePoolReconciler) reconcileUpgradeConfigmap(ctx context.Context, configmap *corev1.ConfigMap, targetConfigVersionHash string, payload []byte) error {
	log := ctrl.LoggerFrom(ctx)

	// TODO (jerzhang): should probably parse the data here to reduce size/compress
	configmap.Data = map[string]string{
		"config": string(payload),
		"hash":   targetConfigVersionHash,
	}

	log.Info("NodePool in place upgrade configmap synced", "target", targetConfigVersionHash)
	return nil
}

func (r *NodePoolReconciler) nodeToNodePool(o client.Object) []reconcile.Request {
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
	if err := r.Client.Get(context.TODO(), client.ObjectKeyFromObject(machine), machine); err != nil {
		return nil
	}

	machineOwner := metav1.GetControllerOf(machine)
	if machineOwner.Kind != "MachineSet" {
		return nil
	}

	machineSet := &capiv1.MachineSet{ObjectMeta: metav1.ObjectMeta{
		Name:      machineOwner.Name,
		Namespace: machineNamespace,
	}}
	if err := r.Client.Get(context.TODO(), client.ObjectKeyFromObject(machineSet), machineSet); err != nil {
		return nil
	}

	nodePoolName := machineSet.GetAnnotations()[nodePoolAnnotation]
	if nodePoolName == "" {
		return nil
	}

	return []reconcile.Request{
		{NamespacedName: hyperutil.ParseNamespacedName(nodePoolName)},
	}
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

func hostedClusterRESTConfig(ctx context.Context, c client.Reader, hc *hyperv1.HostedCluster) (*restclient.Config, error) {
	// TODO (alberto): Use a tailored kubeconfig.
	kubeconfig := hc.Status.KubeConfig
	kubeconfigSecret := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Name: kubeconfig.Name, Namespace: hc.Namespace}, kubeconfigSecret); err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig secret %q: %w", kubeconfigSecret.Name, err)
	}

	kubeConfig, ok := kubeconfigSecret.Data["kubeconfig"]
	if !ok {
		return nil, fmt.Errorf("kubeconfig secret %q does not have 'value' key", kubeconfigSecret.Name)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config kubeconfig from secret %q", kubeconfigSecret.Name)
	}

	restConfig.UserAgent = "nodepool-controller"
	restConfig.Timeout = 30 * time.Second

	return restConfig, nil
}

const (
	cacheLabelSelectorKey = "hypershift.io/managed-by-nodepool-controller"
)

// newHostedClusterCacheAndClient returns a cache and a client for interacting with a guest cluster using the given scheme for encoding and decoding objects.
// The cache needs to be started and synced before either the cache or the client can be used.
func newHostedClusterCacheAndClient(ctx context.Context, c client.Client, hc *hyperv1.HostedCluster) (cache.Cache, client.Client, error) {
	restConfig, err := hostedClusterRESTConfig(ctx, c, hc)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create client: %w", err)
	}
	hostedClusterClient, err := client.New(restConfig, client.Options{Scheme: c.Scheme()})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to construct guest cluster client: %w", err)
	}

	labelSelector, err := labels.ValidatedSelectorFromSet(labels.Set{cacheLabelSelectorKey: labelenforcingclient.CacheLabelSelectorValue})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to construct labelSelector: %w", err)
	}
	hostedClusterCache, err := cache.New(restConfig, cache.Options{
		// Make sure everything other than nodes can only be accessed if we labeled it.
		DefaultSelector: cache.ObjectSelector{Label: labelSelector},
		Mapper:          hostedClusterClient.RESTMapper(),
		Scheme:          hostedClusterClient.Scheme(),
		SelectorsByObject: cache.SelectorsByObject{
			&corev1.Node{}: cache.ObjectSelector{Label: labels.Everything()},
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create cache: %w", err)
	}

	hostedClusterClient, err = client.NewDelegatingClient(client.NewDelegatingClientInput{
		CacheReader: hostedClusterCache,
		Client:      hostedClusterClient,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create cached hostedClusterClient: %w", err)
	}
	// Force everything created through this to be labeled so we can get it from the cache. This is needed
	// because we access pods and we do only want to cache the pods we actualy created, everything
	// else is irrelevant for us.
	hostedClusterClient = labelenforcingclient.New(hostedClusterClient, map[string]string{cacheLabelSelectorKey: labelenforcingclient.CacheLabelSelectorValue})

	return hostedClusterCache, hostedClusterClient, nil
}

func (r *NodePoolReconciler) reconcileMachineSet(ctx context.Context,
	machineSet *capiv1.MachineSet,
	hc *hyperv1.HostedCluster,
	nodePool *hyperv1.NodePool,
	userDataSecret *corev1.Secret,
	machineTemplateCR client.Object,
	CAPIClusterName string,
	targetVersion,
	targetConfigHash, targetConfigVersionHash, machineTemplateSpecJSON string) error {

	log := ctrl.LoggerFrom(ctx)
	// Set annotations and labels
	if machineSet.GetAnnotations() == nil {
		machineSet.Annotations = map[string]string{}
	}
	machineSet.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
	if machineSet.GetLabels() == nil {
		machineSet.Labels = map[string]string{}
	}
	machineSet.Labels[capiv1.ClusterLabelName] = CAPIClusterName

	resourcesName := generateName(CAPIClusterName, nodePool.Spec.ClusterName, nodePool.GetName())
	machineSet.Spec.MinReadySeconds = int32(0)

	gvk, err := apiutil.GVKForObject(machineTemplateCR, api.Scheme)
	if err != nil {
		return err
	}

	// Set selector and template
	machineSet.Spec.ClusterName = CAPIClusterName
	if machineSet.Spec.Selector.MatchLabels == nil {
		machineSet.Spec.Selector.MatchLabels = map[string]string{}
	}
	machineSet.Spec.Selector.MatchLabels[resourcesName] = resourcesName
	machineSet.Spec.Template = capiv1.MachineTemplateSpec{
		ObjectMeta: capiv1.ObjectMeta{
			Labels: map[string]string{
				resourcesName:           resourcesName,
				capiv1.ClusterLabelName: CAPIClusterName,
			},
			Annotations: map[string]string{
				// TODO (alberto): Use conditions to signal an in progress rolling upgrade
				// similar to what we do with nodePoolAnnotationCurrentConfig
				nodePoolAnnotationPlatformMachineTemplate: machineTemplateSpecJSON, // This will trigger a deployment rolling upgrade when its value changes.
			},
		},

		Spec: capiv1.MachineSpec{
			ClusterName: CAPIClusterName,
			Bootstrap: capiv1.Bootstrap{
				// Keep current user data for later check.
				DataSecretName: machineSet.Spec.Template.Spec.Bootstrap.DataSecretName,
			},
			InfrastructureRef: corev1.ObjectReference{
				Kind:       gvk.Kind,
				APIVersion: gvk.GroupVersion().String(),
				Namespace:  machineTemplateCR.GetNamespace(),
				Name:       machineTemplateCR.GetName(),
			},
			// Keep current version for later check.
			Version:          machineSet.Spec.Template.Spec.Version,
			NodeDrainTimeout: nodePool.Spec.NodeDrainTimeout,
		},
	}

	// Propagate version and userData Secret to the MachineSet.
	if userDataSecret.Name != k8sutilspointer.StringPtrDerefOr(machineSet.Spec.Template.Spec.Bootstrap.DataSecretName, "") {
		log.Info("New user data Secret has been generated",
			"current", machineSet.Spec.Template.Spec.Bootstrap.DataSecretName,
			"target", userDataSecret.Name)

		// TODO (alberto): possibly compare with NodePool here instead so we don't rely on impl details to drive decisions.
		if targetVersion != k8sutilspointer.StringPtrDerefOr(machineSet.Spec.Template.Spec.Version, "") {
			log.Info("Starting version upgrade: Propagating new version to the MachineSet",
				"releaseImage", nodePool.Spec.Release.Image, "target", targetVersion)
		}

		if targetConfigHash != nodePool.Annotations[nodePoolAnnotationCurrentConfig] {
			log.Info("Starting config upgrade: Propagating new config to the MachineSet",
				"current", nodePool.Annotations[nodePoolAnnotationCurrentConfig], "target", targetConfigHash)
		}
		machineSet.Spec.Template.Spec.Version = &targetVersion
		machineSet.Spec.Template.Spec.Bootstrap.DataSecretName = k8sutilspointer.StringPtr(userDataSecret.Name)

		// We return early here during a version/config upgrade to persist the resource with new user data Secret,
		// so in the next reconciling loop we get a new machineSet.Generation
		// and we can do a legit MachineSetComplete/MachineSet.Status.ObservedGeneration check.
		// Before persisting, if the NodePool is brand new we want to make sure the replica number is set so the MachineSet controller
		// does not panic.
		if machineSet.Spec.Replicas == nil {
			machineSet.Spec.Replicas = k8sutilspointer.Int32Ptr(k8sutilspointer.Int32PtrDerefOr(nodePool.Spec.Replicas, 0))
		}
		return nil
	}

	setMachineSetReplicas(nodePool, machineSet)

	// Bubble up AvailableReplicas and Ready condition from MachineSet.
	nodePool.Status.Replicas = machineSet.Status.AvailableReplicas
	for _, c := range machineSet.Status.Conditions {
		// This condition should aggregate and summarise readiness from underlying MachineSets and Machines
		// https://github.com/kubernetes-sigs/cluster-api/issues/3486.
		if c.Type == capiv1.ReadyCondition {
			// this is so api server does not complain
			// invalid value: \"\": status.conditions.reason in body should be at least 1 chars long"
			reason := hyperv1.NodePoolAsExpectedConditionReason
			if c.Reason != "" {
				reason = c.Reason
			}

			setStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolReadyConditionType,
				Status:             c.Status,
				ObservedGeneration: nodePool.Generation,
				Message:            c.Message,
				Reason:             reason,
			})
			break
		}
	}

	return nil
}

// setMachineSetReplicas sets wanted replicas:
// If autoscaling is enabled we reconcile min/max annotations and leave replicas untouched.
func setMachineSetReplicas(nodePool *hyperv1.NodePool, machineSet *capiv1.MachineSet) {
	if machineSet.Annotations == nil {
		machineSet.Annotations = make(map[string]string)
	}

	if isAutoscalingEnabled(nodePool) {
		if k8sutilspointer.Int32PtrDerefOr(machineSet.Spec.Replicas, 0) == 0 {
			// if autoscaling is enabled and the MachineSet does not exist yet or it has 0 replicas
			// we set it to 1 replica as the autoscaler does not support scaling from zero yet.
			machineSet.Spec.Replicas = k8sutilspointer.Int32Ptr(int32(1))
		}
		machineSet.Annotations[autoscalerMaxAnnotation] = strconv.Itoa(int(nodePool.Spec.AutoScaling.Max))
		machineSet.Annotations[autoscalerMinAnnotation] = strconv.Itoa(int(nodePool.Spec.AutoScaling.Min))
	}

	// If autoscaling is NOT enabled we reset min/max annotations and reconcile replicas.
	if !isAutoscalingEnabled(nodePool) {
		machineSet.Annotations[autoscalerMaxAnnotation] = "0"
		machineSet.Annotations[autoscalerMinAnnotation] = "0"
		machineSet.Spec.Replicas = k8sutilspointer.Int32Ptr(k8sutilspointer.Int32PtrDerefOr(nodePool.Spec.Replicas, 0))
	}
}
