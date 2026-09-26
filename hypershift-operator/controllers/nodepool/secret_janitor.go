package nodepool

import (
	"context"
	"fmt"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/featuregate"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	supportutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/blang/semver"
)

// secretJanitor reconciles secrets and determines which secrets should remain in the cluster and which should be cleaned up.
// Any secret annotated with a nodePool name should only be on the cluster if the nodePool continues to exist
// and if our current calculation for the inputs to the name matches what the secret is named.
type secretJanitor struct {
	*NodePoolReconciler

	now func() time.Time
}

func (r *secretJanitor) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("secret", req.String())

	secret := &corev1.Secret{}
	if err := r.Client.Get(ctx, req.NamespacedName, secret); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("not found", "request", req.String())
			return ctrl.Result{}, nil
		}
		log.Error(err, "error getting secret")
		return ctrl.Result{}, err
	}

	// only handle secrets that are associated with a NodePool
	nodePoolName, annotated := secret.Annotations[nodePoolAnnotation]
	if !annotated {
		return ctrl.Result{}, nil
	}
	log = log.WithValues("nodePool", nodePoolName)

	// only handle secret types that we know about explicitly
	shouldHandle := false
	for _, prefix := range []string{TokenSecretPrefix, UserDataSecrePrefix} {
		if strings.HasPrefix(secret.Name, prefix) {
			shouldHandle = true
			break
		}
	}
	if !shouldHandle {
		return ctrl.Result{}, nil
	}

	nodePool := &hyperv1.NodePool{}
	if err := r.Client.Get(ctx, supportutil.ParseNamespacedName(nodePoolName), nodePool); err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "error getting nodepool")
		return ctrl.Result{}, err
	} else if apierrors.IsNotFound(err) {
		// this is expected, don't delete the secret.
		labels := secret.GetLabels()
		if labels != nil && labels[karpenterutil.ManagedByKarpenterLabel] == "true" {
			return ctrl.Result{}, nil
		}

		log.Info("removing secret as nodePool is missing")
		return ctrl.Result{}, client.IgnoreNotFound(r.Client.Delete(ctx, secret))

	}

	hcluster, err := GetHostedClusterByName(ctx, r.Client, nodePool.GetNamespace(), nodePool.Spec.ClusterName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("removing secret as hosted cluster is missing")
			return ctrl.Result{}, r.cleanupSecretForDeletion(ctx, secret)
		}
		return ctrl.Result{}, err
	}

	if !hcluster.DeletionTimestamp.IsZero() {
		log.Info("removing secret as hosted cluster is being deleted")
		return ctrl.Result{}, r.cleanupSecretForDeletion(ctx, secret)
	}

	shouldKeepOldUserData, err := r.shouldKeepOldUserData(ctx, hcluster, nodePool, secret)
	if err != nil {
		return ctrl.Result{}, err
	}
	if shouldKeepOldUserData {
		log.V(3).Info("Skipping secretJanitor reconciliation and keeping old user data secret")
		return ctrl.Result{}, nil
	}

	releaseImage, err := r.getReleaseImage(ctx, hcluster, nodePool.Status.Version, nodePool.Spec.Release.Image)
	if err != nil {
		return ctrl.Result{}, err
	}
	haproxyRawConfig, err := r.generateHAProxyRawConfig(ctx, nodePool, hcluster, releaseImage)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to generate HAProxy raw config: %w", err)
	}

	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name)
	osStreamsEnabled := featuregate.Gate().Enabled(featuregate.OSStreams)
	resolvedRHELStream, err := GetRHELStreamForBootImage(ctx, r.Client, nodePool, releaseImage, osStreamsEnabled)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to resolve RHEL stream for boot image: %w", err)
	}
	configGenerator, err := NewConfigGenerator(ctx, r.Client, hcluster, nodePool, releaseImage, haproxyRawConfig, controlPlaneNamespace, resolvedRHELStream)
	if err != nil {
		return ctrl.Result{}, err
	}
	cpoCapabilities, err := r.detectCPOCapabilities(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to detect CPO capabilities: %w", err)
	}
	token, err := NewToken(ctx, configGenerator, cpoCapabilities)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create token: %w", err)
	}

	// synchronously deleting the ignition token is unsafe; we need to clean up tokens by annotating them to expire
	synchronousCleanup := func(ctx context.Context, c client.Client, secret *corev1.Secret) error {
		return c.Delete(ctx, secret)
	}
	type nodePoolSecret struct {
		expectedName   string
		matchingPrefix string
		cleanup        func(context.Context, client.Client, *corev1.Secret) error
	}
	valid := false
	options := []nodePoolSecret{
		{
			expectedName:   token.TokenSecret().GetName(),
			matchingPrefix: TokenSecretPrefix,
			cleanup: func(ctx context.Context, c client.Client, secret *corev1.Secret) error {
				return setExpirationTimestampOnToken(ctx, c, secret, r.now)
			},
		},
		{
			expectedName:   token.UserDataSecret().GetName(),
			matchingPrefix: UserDataSecrePrefix,
			cleanup:        synchronousCleanup,
		},
	}
	cleanup := synchronousCleanup
	var names []string
	for _, option := range options {
		names = append(names, option.expectedName)
		if secret.Name == option.expectedName {
			valid = true
		}
		if strings.HasPrefix(secret.Name, option.matchingPrefix) {
			cleanup = option.cleanup
		}
	}

	if valid {
		return ctrl.Result{}, nil
	}

	shouldKeepOldUserData, err = r.shouldKeepStaleKubeVirtUserData(ctx, hcluster, nodePool, secret)
	if err != nil {
		return ctrl.Result{}, err
	}
	if shouldKeepOldUserData {
		log.V(3).Info("Skipping stale user data cleanup because the Secret is still in use")
		return ctrl.Result{}, nil
	}

	log.WithValues("options", names, "valid", valid).Info("removing secret as it does not match the expected set of names")
	return ctrl.Result{}, cleanup(ctx, r.Client, secret)
}

func (r *secretJanitor) shouldKeepStaleKubeVirtUserData(ctx context.Context, hcluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool, secret *corev1.Secret) (bool, error) {
	if hcluster.Spec.Platform.Type != hyperv1.KubevirtPlatform || !strings.HasPrefix(secret.Name, UserDataSecrePrefix) {
		return false, nil
	}
	return r.userDataSecretInUse(ctx, secret.Name, nodePool)
}

// shouldKeepOldUserData determines if the user data Secret should be kept.
// KubeVirt: the Secret is shared by all VMs in the NodePool generation and must
// survive until the rollout completes and all old VMs are gone. This is an
// architectural requirement, not a temporary workaround.
// AWS: deletion fails on CAPA < v2.2.0 (OCP < 4.16) because of
// https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/3805.
// TODO (Alberto): remove the AWS guard when OCP < 4.16 support is dropped.
func (r *NodePoolReconciler) shouldKeepOldUserData(ctx context.Context, hc *hyperv1.HostedCluster, nodePool *hyperv1.NodePool, secret *corev1.Secret) (bool, error) {
	switch hc.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		// Preserve the existing AWS behavior. The reference-aware retention
		// change is intentionally limited to KubeVirt for now.
		return r.shouldKeepOldUserDataAWS(ctx, hc)
	case hyperv1.KubevirtPlatform:
		if strings.HasPrefix(secret.Name, TokenSecretPrefix) {
			// Preserve the existing KubeVirt token behavior. Rollout-aware
			// retention applies only to user-data Secrets.
			return true, nil
		}
		if !strings.HasPrefix(secret.Name, UserDataSecrePrefix) {
			return false, nil
		}
		return r.userDataSecretInUse(ctx, secret.Name, nodePool)
	default:
		return false, nil
	}
}

// userDataSecretInUse determines whether a user-data Secret is still referenced
// by a CAPI Machine or by a MachineSet/MachineDeployment that can create one.
// It deliberately lists all Machines in the HostedControlPlane namespace and
// matches the exact Secret name rather than relying on the NodePool annotation.
// A missing annotation must not make it safe to delete a referenced Secret.
func (r *NodePoolReconciler) userDataSecretInUse(ctx context.Context, secretName string, nodePool *hyperv1.NodePool) (bool, error) {
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(nodePool.Namespace, nodePool.Spec.ClusterName)

	machines := &capiv1.MachineList{}
	if err := r.List(ctx, machines, client.InNamespace(controlPlaneNamespace)); err != nil {
		return true, fmt.Errorf("failed to list Machines while checking user-data Secret %q: %w", secretName, err)
	}
	for i := range machines.Items {
		if ptr.Deref(machines.Items[i].Spec.Bootstrap.DataSecretName, "") == secretName {
			return true, nil
		}
	}

	machineSets := &capiv1.MachineSetList{}
	if err := r.List(ctx, machineSets, client.InNamespace(controlPlaneNamespace)); err != nil {
		return true, fmt.Errorf("failed to list MachineSets while checking user-data Secret %q: %w", secretName, err)
	}
	for i := range machineSets.Items {
		machineSet := &machineSets.Items[i]
		if ptr.Deref(machineSet.Spec.Template.Spec.Bootstrap.DataSecretName, "") != secretName {
			continue
		}
		if bootstrapTemplateCanCreateMachine(machineSet.DeletionTimestamp, machineSet.Spec.Replicas, machineSet.Status.Replicas) {
			return true, nil
		}
	}

	machineDeployments := &capiv1.MachineDeploymentList{}
	if err := r.List(ctx, machineDeployments, client.InNamespace(controlPlaneNamespace)); err != nil {
		return true, fmt.Errorf("failed to list MachineDeployments while checking user-data Secret %q: %w", secretName, err)
	}
	for i := range machineDeployments.Items {
		machineDeployment := &machineDeployments.Items[i]
		if ptr.Deref(machineDeployment.Spec.Template.Spec.Bootstrap.DataSecretName, "") != secretName {
			continue
		}
		if bootstrapTemplateCanCreateMachine(machineDeployment.DeletionTimestamp, machineDeployment.Spec.Replicas, machineDeployment.Status.Replicas) {
			return true, nil
		}
	}

	return false, nil
}

func bootstrapTemplateCanCreateMachine(deletionTimestamp *metav1.Time, specReplicas, statusReplicas *int32) bool {
	if deletionTimestamp != nil && !deletionTimestamp.IsZero() {
		return true
	}
	if specReplicas == nil || ptr.Deref(specReplicas, 0) > 0 {
		return true
	}
	if statusReplicas == nil {
		return true
	}
	return ptr.Deref(statusReplicas, 0) > 0
}

func enqueueUserDataSecretForMachine(obj client.Object) []reconcile.Request {
	machine, ok := obj.(*capiv1.Machine)
	if !ok {
		return nil
	}
	return enqueueUserDataSecret(machine.Namespace, ptr.Deref(machine.Spec.Bootstrap.DataSecretName, ""))
}

func enqueueUserDataSecretForMachineSet(obj client.Object) []reconcile.Request {
	machineSet, ok := obj.(*capiv1.MachineSet)
	if !ok {
		return nil
	}
	return enqueueUserDataSecret(machineSet.Namespace, ptr.Deref(machineSet.Spec.Template.Spec.Bootstrap.DataSecretName, ""))
}

func enqueueUserDataSecretForMachineDeployment(obj client.Object) []reconcile.Request {
	machineDeployment, ok := obj.(*capiv1.MachineDeployment)
	if !ok {
		return nil
	}
	return enqueueUserDataSecret(machineDeployment.Namespace, ptr.Deref(machineDeployment.Spec.Template.Spec.Bootstrap.DataSecretName, ""))
}

// enqueueKubeVirtUserDataSecret maps CAPI events to user-data Secrets only for
// NodePools using KubeVirt. The retention behavior for other platforms is
// intentionally unchanged and should not be re-triggered by these watches.
func (r *NodePoolReconciler) enqueueKubeVirtUserDataSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	nodePoolName, ok := obj.GetAnnotations()[nodePoolAnnotation]
	if !ok {
		return nil
	}

	nodePool := &hyperv1.NodePool{}
	if err := r.Get(ctx, supportutil.ParseNamespacedName(nodePoolName), nodePool); err != nil {
		// The infrastructure reference remains available on CAPI objects, so use it
		// as a narrow fallback for transient NodePool lookup failures.
		if apierrors.IsNotFound(err) || !isKubeVirtCAPIObject(obj) {
			return nil
		}
	} else if nodePool.Spec.Platform.Type != hyperv1.KubevirtPlatform {
		return nil
	}

	switch obj.(type) {
	case *capiv1.Machine:
		return enqueueUserDataSecretForMachine(obj)
	case *capiv1.MachineSet:
		return enqueueUserDataSecretForMachineSet(obj)
	case *capiv1.MachineDeployment:
		return enqueueUserDataSecretForMachineDeployment(obj)
	default:
		return nil
	}
}

func isKubeVirtCAPIObject(obj client.Object) bool {
	const (
		kubeVirtMachineKind         = "KubevirtMachine"
		kubeVirtMachineTemplateKind = "KubevirtMachineTemplate"
	)

	switch obj := obj.(type) {
	case *capiv1.Machine:
		return obj.Spec.InfrastructureRef.Kind == kubeVirtMachineKind
	case *capiv1.MachineSet:
		return obj.Spec.Template.Spec.InfrastructureRef.Kind == kubeVirtMachineTemplateKind
	case *capiv1.MachineDeployment:
		return obj.Spec.Template.Spec.InfrastructureRef.Kind == kubeVirtMachineTemplateKind
	default:
		return false
	}
}

func enqueueUserDataSecret(namespace, secretName string) []reconcile.Request {
	if !strings.HasPrefix(secretName, UserDataSecrePrefix) {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: namespace, Name: secretName}}}
}

func capiDeletionOnlyPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return false },
		UpdateFunc:  func(event.UpdateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

func capiUserDataStateChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(event.CreateEvent) bool { return true },
		UpdateFunc: func(event event.UpdateEvent) bool {
			switch oldObject := event.ObjectOld.(type) {
			case *capiv1.Machine:
				newObject, ok := event.ObjectNew.(*capiv1.Machine)
				return ok && (dataSecretNameChanged(oldObject.Spec.Bootstrap.DataSecretName, newObject.Spec.Bootstrap.DataSecretName) || deletionTimestampStateChanged(oldObject, newObject))
			case *capiv1.MachineSet:
				newObject, ok := event.ObjectNew.(*capiv1.MachineSet)
				return ok && (dataSecretNameChanged(oldObject.Spec.Template.Spec.Bootstrap.DataSecretName, newObject.Spec.Template.Spec.Bootstrap.DataSecretName) ||
					replicaCountChanged(oldObject.Spec.Replicas, newObject.Spec.Replicas) ||
					replicaCountChanged(oldObject.Status.Replicas, newObject.Status.Replicas) ||
					deletionTimestampStateChanged(oldObject, newObject))
			case *capiv1.MachineDeployment:
				newObject, ok := event.ObjectNew.(*capiv1.MachineDeployment)
				return ok && (dataSecretNameChanged(oldObject.Spec.Template.Spec.Bootstrap.DataSecretName, newObject.Spec.Template.Spec.Bootstrap.DataSecretName) ||
					replicaCountChanged(oldObject.Spec.Replicas, newObject.Spec.Replicas) ||
					replicaCountChanged(oldObject.Status.Replicas, newObject.Status.Replicas) ||
					deletionTimestampStateChanged(oldObject, newObject))
			default:
				return false
			}
		},
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

func dataSecretNameChanged(oldName, newName *string) bool {
	return ptr.Deref(oldName, "") != ptr.Deref(newName, "")
}

func replicaCountChanged(oldReplicas, newReplicas *int32) bool {
	if oldReplicas == nil || newReplicas == nil {
		return oldReplicas != nil || newReplicas != nil
	}
	return *oldReplicas != *newReplicas
}

func deletionTimestampStateChanged(oldObject, newObject client.Object) bool {
	oldDeleting := oldObject.GetDeletionTimestamp() != nil && !oldObject.GetDeletionTimestamp().IsZero()
	newDeleting := newObject.GetDeletionTimestamp() != nil && !newObject.GetDeletionTimestamp().IsZero()
	return oldDeleting != newDeleting
}

// shouldKeepOldUserDataAWS checks whether the AWS hosted cluster version is old
// enough to require preserving the previous userdata Secret during rollout.
func (r *NodePoolReconciler) shouldKeepOldUserDataAWS(ctx context.Context, hc *hyperv1.HostedCluster) (bool, error) {
	// If there's a current version in status, be conservative and assume that one is the one running CAPA.
	releaseImage := hc.Spec.Release.Image
	if hc.Status.Version != nil {
		if len(hc.Status.Version.History) > 0 {
			releaseImage = hc.Status.Version.History[0].Image
		}
	}

	pullSecretBytes, err := r.getPullSecretBytes(ctx, hc)
	if err != nil {
		return true, fmt.Errorf("failed to get pull secret bytes: %w", err)
	}

	releaseInfo, err := r.ReleaseProvider.Lookup(ctx, releaseImage, pullSecretBytes)
	if err != nil {
		return true, fmt.Errorf("failed to lookup release image: %w", err)
	}
	hostedClusterVersion, err := semver.Parse(releaseInfo.Version())
	if err != nil {
		return true, err
	}

	if hostedClusterVersion.LT(semver.MustParse("4.16.0")) {
		return true, nil
	}

	return false, nil
}

// cleanupSecretForDeletion handles secret cleanup when the HostedCluster is missing or being deleted.
// Token secrets get an expiration annotation; userdata secrets get deleted directly.
func (r *secretJanitor) cleanupSecretForDeletion(ctx context.Context, secret *corev1.Secret) error {
	if strings.HasPrefix(secret.Name, TokenSecretPrefix) {
		return client.IgnoreNotFound(setExpirationTimestampOnToken(ctx, r.Client, secret, r.now))
	}
	return client.IgnoreNotFound(r.Client.Delete(ctx, secret))
}
