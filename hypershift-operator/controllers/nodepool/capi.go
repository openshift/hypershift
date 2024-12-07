package nodepool

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	agentv1 "github.com/openshift/cluster-api-provider-agent/api/v1beta1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/kubevirt"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/openstack"
	"github.com/openshift/hypershift/support/api"
	supportutil "github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capipowervs "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiopenstackv1beta1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// CAPI Knows how to reconcile all the CAPI resources for a unique token.
// TODO(alberto): consider stronger decoupling from Token by making it an interface
// and let nodepool, hostedcluster, and client be fields of CAPI / interface methods.
type CAPI struct {
	*Token
	capiClusterName string
}

func newCAPI(token *Token, capiClusterName string) (*CAPI, error) {
	if token == nil {
		return nil, fmt.Errorf("token can not be nil")
	}

	if capiClusterName == "" {
		return nil, fmt.Errorf("capiClusterName can not be empty")
	}

	return &CAPI{
		Token:           token,
		capiClusterName: capiClusterName,
	}, nil
}

func (c *CAPI) Reconcile(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)

	nodePool := c.nodePool
	if err := c.cleanupMachineTemplates(ctx, log, nodePool, c.controlplaneNamespace); err != nil {
		return err
	}

	//  Reconcile (Platform)MachineTemplate.
	template, mutateTemplate, _, err := c.machineTemplateBuilders()
	if err != nil {
		return err
	}
	if result, err := c.CreateOrUpdate(ctx, c.Client, template, func() error {
		return mutateTemplate(template)
	}); err != nil {
		return err
	} else {
		log.Info("Reconciled Machine template", "result", result)
	}

	// Check if platform machine template needs to be updated.
	targetMachineTemplate := template.GetName()
	if isUpdatingMachineTemplate(nodePool, targetMachineTemplate) {
		// TODO (alberto): deocuple all conditions handling from this file into nodepool_controller.go dedicated function.
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			Message:            fmt.Sprintf("platform machine template update in progress. Target template: %s", targetMachineTemplate),
			ObservedGeneration: nodePool.Generation,
		})
		log.Info("NodePool machine template is updating",
			"current", nodePool.GetAnnotations()[nodePoolAnnotationPlatformMachineTemplate],
			"target", targetMachineTemplate)
	} else {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: nodePool.Generation,
		})
	}

	if nodePool.Spec.Management.UpgradeType == hyperv1.UpgradeTypeInPlace {
		ms := c.machineSet()
		if result, err := controllerutil.CreateOrPatch(ctx, c.Client, ms, func() error {
			return c.reconcileMachineSet(
				ctx,
				ms,
				template)
		}); err != nil {
			return fmt.Errorf("failed to reconcile MachineSet %q: %w",
				client.ObjectKeyFromObject(ms).String(), err)
		} else {
			log.Info("Reconciled MachineSet", "result", result)
		}
	}

	if nodePool.Spec.Management.UpgradeType == hyperv1.UpgradeTypeReplace {
		md := c.machineDeployment()
		if result, err := controllerutil.CreateOrPatch(ctx, c.Client, md, func() error {
			return c.reconcileMachineDeployment(
				ctx,
				log,
				md,
				template)
		}); err != nil {
			return fmt.Errorf("failed to reconcile MachineDeployment %q: %w",
				client.ObjectKeyFromObject(md).String(), err)
		} else {
			log.Info("Reconciled MachineDeployment", "result", result)
		}
	}

	mhc := c.machineHealthCheck()
	if nodePool.Spec.Management.AutoRepair {
		if c := FindStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolReachedIgnitionEndpoint); c == nil || c.Status != corev1.ConditionTrue {
			log.Info("ReachedIgnitionEndpoint is false, MachineHealthCheck won't be created until this is true")
			return nil
		}

		if result, err := ctrl.CreateOrUpdate(ctx, c.Client, mhc, func() error {
			return c.reconcileMachineHealthCheck(ctx, mhc)
		}); err != nil {
			return fmt.Errorf("failed to reconcile MachineHealthCheck %q: %w",
				client.ObjectKeyFromObject(mhc).String(), err)
		} else {
			log.Info("Reconciled MachineHealthCheck", "result", result)
		}
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolAutorepairEnabledConditionType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: nodePool.Generation,
		})
	} else {
		err := c.Get(ctx, client.ObjectKeyFromObject(mhc), mhc)
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		if err == nil {
			if err := c.Delete(ctx, mhc); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolAutorepairEnabledConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.AsExpectedReason,
			ObservedGeneration: nodePool.Generation,
		})
	}
	return nil
}

func (c *CAPI) cleanupMachineTemplates(ctx context.Context, log logr.Logger, nodePool *hyperv1.NodePool, controlPlaneNamespace string) error {
	// list machineSets
	machineSets := &capiv1.MachineSetList{}
	if err := c.Client.List(ctx, machineSets, client.InNamespace(controlPlaneNamespace)); err != nil {
		return fmt.Errorf("failed to list machineSets: %w", err)
	}

	// filter machineSets owned by this nodePool.
	nodePoolKey := client.ObjectKeyFromObject(nodePool).String()
	filtered := make([]*capiv1.MachineSet, 0, len(machineSets.Items))
	for idx := range machineSets.Items {
		ms := &machineSets.Items[idx]
		// skip if machineSet doesn't belong to the nodePool
		if ms.Annotations[nodePoolAnnotation] != nodePoolKey {
			continue
		}

		filtered = append(filtered, ms)
	}

	if len(filtered) == 0 {
		// initial machineSet has not been created.
		log.Info("initial machineSet has not been created.")
		return nil
	}

	ref := filtered[0].Spec.Template.Spec.InfrastructureRef
	machineTemplates := new(unstructured.UnstructuredList)
	machineTemplates.SetAPIVersion(ref.APIVersion)
	machineTemplates.SetKind(ref.Kind)
	if err := c.Client.List(ctx, machineTemplates, client.InNamespace(ref.Namespace)); err != nil {
		return fmt.Errorf("failed to list MachineTemplates: %w", err)
	}

	// delete old machine templates not currently referenced by any machineSet.
	for _, mt := range machineTemplates.Items {
		// skip if MachineTempalte doesn't belong to the nodePool.
		if mt.GetAnnotations()[nodePoolAnnotation] != nodePoolKey {
			continue
		}

		shouldDelete := true
		for _, ms := range filtered {
			if mt.GetName() == ms.Spec.Template.Spec.InfrastructureRef.Name {
				shouldDelete = false
				break
			}
		}

		if shouldDelete {
			log.Info("deleting machineTemplate", "name", mt.GetName())
			if err := c.Client.Delete(ctx, &mt); err != nil {
				return fmt.Errorf("failed to delete MachineTemplate %s: %w", mt.GetName(), err)
			}
		}
	}

	return nil
}

func deleteMachineDeployment(ctx context.Context, c client.Client, md *capiv1.MachineDeployment) error {
	// TODO(alberto): why do we need to fetch the object and check the DeletionTimestamp first?
	// isn't Delete a no-op if the object is already deleting?
	// This is kept like this for now to contain the scope of the refactor and avoid backward compatibility issues.
	err := c.Get(ctx, client.ObjectKeyFromObject(md), md)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error getting MachineDeployment: %w", err)
	}
	if md.DeletionTimestamp != nil {
		return nil
	}
	err = c.Delete(ctx, md)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error deleting MachineDeployment: %w", err)
	}
	return nil
}

func (c *CAPI) pauseMachineDeployment(ctx context.Context) error {
	md := c.machineDeployment()
	err := c.Get(ctx, client.ObjectKeyFromObject(md), md)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error getting MachineDeployment: %w", err)
	}
	if md.Annotations == nil {
		md.Annotations = make(map[string]string)
	}
	// FIXME: In future we may want to use the spec field instead
	// https://github.com/kubernetes-sigs/cluster-api/issues/6966
	md.Annotations[capiv1.PausedAnnotation] = "true"
	return c.Update(ctx, md)
}

func deleteMachineSet(ctx context.Context, c client.Client, ms *capiv1.MachineSet) error {
	// TODO(alberto): why do we need to fetch the object and check the DeletionTimestamp first?
	// isn't Delete a no-op if the object is already deleting?
	// This is kept like this for now to contain the scope of the refactor and avoid backward compatibility issues.
	err := c.Get(ctx, client.ObjectKeyFromObject(ms), ms)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error getting MachineSet: %w", err)
	}
	if ms.DeletionTimestamp != nil {
		return nil
	}
	err = c.Delete(ctx, ms)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error deleting MachineSet: %w", err)
	}
	return nil
}

func (c *CAPI) Pause(ctx context.Context) error {
	// Pause MachineSet
	if err := c.pauseMachineSet(ctx); err != nil {
		return fmt.Errorf("error pausing MachineSet: %w", err)
	}

	// Pause MachineDeployment
	if err := c.pauseMachineDeployment(ctx); err != nil {
		return fmt.Errorf("error pausing MachineDeployment: %w", err)
	}

	return nil
}

func (c *CAPI) pauseMachineSet(ctx context.Context) error {
	ms := c.machineSet()
	err := c.Get(ctx, client.ObjectKeyFromObject(ms), ms)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error getting MachineSet: %w", err)
	}
	if ms.Annotations == nil {
		ms.Annotations = make(map[string]string)
	}
	// FIXME: In future we may want to use the spec field instead
	// https://github.com/kubernetes-sigs/cluster-api/issues/6966
	// TODO: Also for paused to be complete we will need to pause all MHC if autorepair
	// is enabled and remove the autoscaling labels from the MachineDeployment / Machineset
	ms.Annotations[capiv1.PausedAnnotation] = "true"
	return c.Update(ctx, ms)
}

func deleteMachineHealthCheck(ctx context.Context, c client.Client, mhc *capiv1.MachineHealthCheck) error {
	// TODO(alberto): why do we need to fetch the object and check the DeletionTimestamp first?
	// isn't Delete a no-op if the object is already deleting?
	// This is kept like this for now to contain the scope of the refactor and avoid backward compatibility issues.
	err := c.Get(ctx, client.ObjectKeyFromObject(mhc), mhc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error getting MachineHealthCheck: %w", err)
	}
	if mhc.DeletionTimestamp != nil {
		return nil
	}
	err = c.Delete(ctx, mhc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error deleting MachineHealthCheck: %w", err)
	}
	return nil
}

func (c *CAPI) reconcileMachineDeployment(ctx context.Context, log logr.Logger,
	machineDeployment *capiv1.MachineDeployment,
	machineTemplateCR client.Object) error {

	nodePool := c.nodePool
	capiClusterName := c.capiClusterName
	// Set annotations and labels
	if machineDeployment.GetAnnotations() == nil {
		machineDeployment.Annotations = map[string]string{}
	}
	machineDeployment.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
	// Delete any paused annotation
	delete(machineDeployment.Annotations, capiv1.PausedAnnotation)
	if machineDeployment.GetLabels() == nil {
		machineDeployment.Labels = map[string]string{}
	}
	machineDeployment.Labels[capiv1.ClusterNameLabel] = capiClusterName

	// Set defaults. These are normally set by the CAPI machinedeployment webhook.
	// However, since we don't run the webhook, CAPI updates the machinedeployment
	// after it has been created with defaults.
	machineDeployment.Spec.MinReadySeconds = ptr.To[int32](0)
	machineDeployment.Spec.RevisionHistoryLimit = ptr.To[int32](1)
	machineDeployment.Spec.ProgressDeadlineSeconds = ptr.To[int32](600)

	machineDeployment.Spec.ClusterName = capiClusterName
	if machineDeployment.Spec.Selector.MatchLabels == nil {
		machineDeployment.Spec.Selector.MatchLabels = map[string]string{}
	}
	machineDeployment.Spec.Selector.MatchLabels[capiv1.ClusterNameLabel] = capiClusterName
	resourcesName := generateName(capiClusterName, nodePool.Spec.ClusterName, nodePool.GetName())
	machineDeployment.Spec.Selector.MatchLabels[resourcesName] = resourcesName

	gvk, err := apiutil.GVKForObject(machineTemplateCR, api.Scheme)
	if err != nil {
		return err
	}
	machineDeployment.Spec.Template = capiv1.MachineTemplateSpec{
		ObjectMeta: capiv1.ObjectMeta{
			Labels: map[string]string{
				resourcesName:           resourcesName,
				capiv1.ClusterNameLabel: capiClusterName,
			},
			// Annotations here propagate down to Machines
			// https://cluster-api.sigs.k8s.io/developer/architecture/controllers/metadata-propagation.html#machinedeployment.
			Annotations: map[string]string{
				nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
			},
		},
		Spec: capiv1.MachineSpec{
			ClusterName: capiClusterName,
			Bootstrap: capiv1.Bootstrap{
				// Keep current user data for later check.
				DataSecretName: machineDeployment.Spec.Template.Spec.Bootstrap.DataSecretName,
			},
			InfrastructureRef: corev1.ObjectReference{
				Kind:       gvk.Kind,
				APIVersion: gvk.GroupVersion().String(),
				Namespace:  machineTemplateCR.GetNamespace(),
				// keep current template name for later check.
				Name: machineDeployment.Spec.Template.Spec.InfrastructureRef.Name,
			},
			// Keep current version for later check.
			Version:                 machineDeployment.Spec.Template.Spec.Version,
			NodeDrainTimeout:        nodePool.Spec.NodeDrainTimeout,
			NodeVolumeDetachTimeout: nodePool.Spec.NodeVolumeDetachTimeout,
		},
	}

	// The CAPI provider for OpenStack uses the FailureDomain field to set the availability zone.
	if c.nodePool.Spec.Platform.Type == hyperv1.OpenStackPlatform && c.nodePool.Spec.Platform.OpenStack != nil {
		if c.nodePool.Spec.Platform.OpenStack.AvailabilityZone != "" {
			machineDeployment.Spec.Template.Spec.FailureDomain = ptr.To(c.nodePool.Spec.Platform.OpenStack.AvailabilityZone)
		}
	}

	// After a MachineDeployment is created we propagate label/taints directly into Machines.
	// This is to avoid a NodePool label/taints to trigger a rolling upgrade.
	// TODO(Alberto): drop this an rely on core in-place propagation once CAPI 1.4.0 https://github.com/kubernetes-sigs/cluster-api/releases comes through the payload.
	// https://issues.redhat.com/browse/HOSTEDCP-971
	machineList := &capiv1.MachineList{}
	if err := c.List(ctx, machineList, client.InNamespace(machineDeployment.Namespace)); err != nil {
		return err
	}
	for _, machine := range machineList.Items {
		if nodePoolName := machine.GetAnnotations()[nodePoolAnnotation]; nodePoolName != client.ObjectKeyFromObject(nodePool).String() {
			continue
		}

		if machine.Annotations == nil {
			machine.Annotations = make(map[string]string)
		}
		if machine.Labels == nil {
			machine.Labels = make(map[string]string)
		}

		if result, err := controllerutil.CreateOrPatch(ctx, c.Client, &machine, func() error {
			// Propagate labels.
			for k, v := range nodePool.Spec.NodeLabels {
				// Propagated managed labels down to Machines with a known hardcoded prefix
				// so the CPO HCCO Node controller can recognize them and apply them to Nodes.
				labelKey := fmt.Sprintf("%s.%s", labelManagedPrefix, k)
				machine.Labels[labelKey] = v
			}

			// Propagate taints.
			taintsInJSON, err := taintsToJSON(nodePool.Spec.Taints)
			if err != nil {
				return err
			}

			machine.Annotations[nodePoolAnnotationTaints] = taintsInJSON
			return nil
		}); err != nil {
			return fmt.Errorf("failed to reconcile Machine %q: %w",
				client.ObjectKeyFromObject(&machine).String(), err)
		} else {
			log.Info("Reconciled Machine", "result", result)
		}
	}

	// Set strategy
	machineDeployment.Spec.Strategy = &capiv1.MachineDeploymentStrategy{}
	machineDeployment.Spec.Strategy.Type = capiv1.MachineDeploymentStrategyType(nodePool.Spec.Management.Replace.Strategy)
	if nodePool.Spec.Management.Replace.RollingUpdate != nil {
		machineDeployment.Spec.Strategy.RollingUpdate = &capiv1.MachineRollingUpdateDeployment{
			MaxUnavailable: nodePool.Spec.Management.Replace.RollingUpdate.MaxUnavailable,
			MaxSurge:       nodePool.Spec.Management.Replace.RollingUpdate.MaxSurge,
		}
	}

	setMachineDeploymentReplicas(nodePool, machineDeployment)

	isUpdating := false
	// Propagate version and userData Secret to the machineDeployment.
	userDataSecret := c.UserDataSecret()
	targetVersion := c.Version()
	targetConfigHash := c.HashWithoutVersion()
	targetConfigVersionHash := c.Hash()
	if userDataSecret.Name != ptr.Deref(machineDeployment.Spec.Template.Spec.Bootstrap.DataSecretName, "") {
		log.Info("New user data Secret has been generated",
			"current", machineDeployment.Spec.Template.Spec.Bootstrap.DataSecretName,
			"target", userDataSecret.Name)

		if targetVersion != ptr.Deref(machineDeployment.Spec.Template.Spec.Version, "") {
			log.Info("Starting version update: Propagating new version to the MachineDeployment",
				"releaseImage", nodePool.Spec.Release.Image, "target", targetVersion)
		}

		if targetConfigHash != nodePool.Annotations[nodePoolAnnotationCurrentConfig] {
			log.Info("Starting config update: Propagating new config to the MachineDeployment",
				"current", nodePool.Annotations[nodePoolAnnotationCurrentConfig], "target", targetConfigHash)
		}
		machineDeployment.Spec.Template.Spec.Version = &targetVersion
		machineDeployment.Spec.Template.Spec.Bootstrap.DataSecretName = ptr.To(userDataSecret.Name)
		isUpdating = true
	}

	// template spec has changed, signal a rolling upgrade.
	if machineTemplateCR.GetName() != machineDeployment.Spec.Template.Spec.InfrastructureRef.Name {
		log.Info("New machine template has been generated",
			"current", machineDeployment.Spec.Template.Spec.InfrastructureRef.Name,
			"target", machineTemplateCR.GetName())

		machineDeployment.Spec.Template.Spec.InfrastructureRef.Name = machineTemplateCR.GetName()
		isUpdating = true
	}

	if isUpdating {
		// We return early here during a version/config/MachineTemplate update to persist the resource with new user data Secret / MachineTemplate,
		// so in the next reconciling loop we get a new MachineDeployment.Generation
		// and we can do a legit MachineDeploymentComplete/MachineDeployment.Status.ObservedGeneration check.
		return nil
	}

	// If the MachineDeployment is now processing we know
	// is at the expected version (spec.version) and config (userData Secret) so we reconcile status and annotation.
	if MachineDeploymentComplete(machineDeployment) {
		if nodePool.Status.Version != targetVersion {
			log.Info("Version update complete",
				"previous", nodePool.Status.Version, "new", targetVersion)
			nodePool.Status.Version = targetVersion
		}

		if nodePool.Annotations == nil {
			nodePool.Annotations = make(map[string]string)
		}
		if nodePool.Annotations[nodePoolAnnotationCurrentConfig] != targetConfigHash {
			log.Info("Config update complete",
				"previous", nodePool.Annotations[nodePoolAnnotationCurrentConfig], "new", targetConfigHash)
			nodePool.Annotations[nodePoolAnnotationCurrentConfig] = targetConfigHash
		}
		nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = targetConfigVersionHash

		if nodePool.Annotations[nodePoolAnnotationPlatformMachineTemplate] != machineTemplateCR.GetName() {
			log.Info("Rolling upgrade complete",
				"previous", nodePool.Annotations[nodePoolAnnotationPlatformMachineTemplate], "new", machineTemplateCR.GetName())
			nodePool.Annotations[nodePoolAnnotationPlatformMachineTemplate] = machineTemplateCR.GetName()
		}
	}

	// Bubble up AvailableReplicas and Ready condition from MachineDeployment.
	nodePool.Status.Replicas = machineDeployment.Status.AvailableReplicas
	for _, c := range machineDeployment.Status.Conditions {
		// This condition should aggregate and summarise readiness from underlying MachineSets and Machines
		// https://github.com/kubernetes-sigs/cluster-api/issues/3486.
		if c.Type == capiv1.ReadyCondition {
			// this is so api server does not complain
			// invalid value: \"\": status.conditions.reason in body should be at least 1 chars long"
			reason := hyperv1.AsExpectedReason
			if c.Reason != "" {
				reason = c.Reason
			}

			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
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

func taintsToJSON(taints []hyperv1.Taint) (string, error) {
	taintsInJSON, err := json.Marshal(taints)
	if err != nil {
		return "", err
	}

	return string(taintsInJSON), nil
}

func (c *CAPI) reconcileMachineHealthCheck(ctx context.Context,
	mhc *capiv1.MachineHealthCheck) error {

	log := ctrl.LoggerFrom(ctx)
	nodePool := c.nodePool
	hc := c.hostedCluster
	capiClusterName := c.capiClusterName

	// Opinionated spec based on
	// https://github.com/openshift/managed-cluster-config/blob/14d4255ec75dc263ffd3d897dfccc725cb2b7072/deploy/osd-machine-api/011-machine-api.srep-worker-healthcheck.MachineHealthCheck.yaml
	// TODO (alberto): possibly expose this config at the nodePool API.
	maxUnhealthy := intstr.FromInt(2)
	var timeOut time.Duration
	nodeStartupTimeout := 20 * time.Minute

	switch nodePool.Spec.Platform.Type {
	case hyperv1.AgentPlatform, hyperv1.NonePlatform:
		timeOut = 16 * time.Minute
	default:
		timeOut = 8 * time.Minute
	}

	maxUnhealthyOverride := nodePool.Annotations[hyperv1.MachineHealthCheckMaxUnhealthyAnnotation]
	if maxUnhealthyOverride == "" {
		maxUnhealthyOverride = hc.Annotations[hyperv1.MachineHealthCheckMaxUnhealthyAnnotation]
	}
	if maxUnhealthyOverride != "" {
		maxUnhealthyValue := intstr.Parse(maxUnhealthyOverride)
		// validate that this is a valid value by getting a scaled value
		if _, err := intstr.GetScaledValueFromIntOrPercent(&maxUnhealthyValue, 100, true); err != nil {
			log.Error(err, "Cannot parse max unhealthy override duration", "value", maxUnhealthyOverride)
		} else {
			maxUnhealthy = maxUnhealthyValue
		}
	}

	timeOutOverride := nodePool.Annotations[hyperv1.MachineHealthCheckTimeoutAnnotation]
	if timeOutOverride == "" {
		timeOutOverride = hc.Annotations[hyperv1.MachineHealthCheckTimeoutAnnotation]
	}
	if timeOutOverride != "" {
		timeOutOverrideTime, err := time.ParseDuration(timeOutOverride)
		if err != nil {
			log.Error(err, "Cannot parse timeout override duration", "value", timeOutOverride)
		} else {
			timeOut = timeOutOverrideTime
		}
	}

	nodeStartupTimeoutOverride := nodePool.Annotations[hyperv1.MachineHealthCheckNodeStartupTimeoutAnnotation]
	if nodeStartupTimeoutOverride == "" {
		nodeStartupTimeoutOverride = hc.Annotations[hyperv1.MachineHealthCheckNodeStartupTimeoutAnnotation]
	}
	if nodeStartupTimeoutOverride != "" {
		nodeStartupTimeoutOverrideTime, err := time.ParseDuration(nodeStartupTimeoutOverride)
		if err != nil {
			log.Error(err, "Cannot parse node startup timeout override duration", "value", nodeStartupTimeoutOverrideTime)
		} else {
			nodeStartupTimeout = nodeStartupTimeoutOverrideTime
		}
	}

	resourcesName := generateName(capiClusterName, nodePool.Spec.ClusterName, nodePool.GetName())
	mhc.Spec = capiv1.MachineHealthCheckSpec{
		ClusterName: capiClusterName,
		Selector: metav1.LabelSelector{
			MatchLabels: map[string]string{
				resourcesName: resourcesName,
			},
		},
		UnhealthyConditions: []capiv1.UnhealthyCondition{
			{
				Type:   corev1.NodeReady,
				Status: corev1.ConditionFalse,
				Timeout: metav1.Duration{
					Duration: timeOut,
				},
			},
			{
				Type:   corev1.NodeReady,
				Status: corev1.ConditionUnknown,
				Timeout: metav1.Duration{
					Duration: timeOut,
				},
			},
		},
		MaxUnhealthy: &maxUnhealthy,
		NodeStartupTimeout: &metav1.Duration{
			Duration: nodeStartupTimeout,
		},
	}
	return nil
}

// setMachineDeploymentReplicas sets wanted replicas:
// If autoscaling is enabled we reconcile min/max annotations and leave replicas untouched.
func setMachineDeploymentReplicas(nodePool *hyperv1.NodePool, machineDeployment *capiv1.MachineDeployment) {
	if machineDeployment.Annotations == nil {
		machineDeployment.Annotations = make(map[string]string)
	}

	if isAutoscalingEnabled(nodePool) {
		// The MachineDeployment replicas field should default to a value inside the (min size, max size) range based on the autoscaler annotations
		// so the autoscaler can take control of the replicas field.
		//
		// 1. if it’s a new MachineDeployment, or the replicas field of the old MachineDeployment is < min size, use min size
		// 2. if the replicas field of the old MachineDeployment is > max size, use max size
		mdReplicas := ptr.Deref(machineDeployment.Spec.Replicas, 0)
		if mdReplicas < nodePool.Spec.AutoScaling.Min {
			machineDeployment.Spec.Replicas = &nodePool.Spec.AutoScaling.Min
		} else if mdReplicas > nodePool.Spec.AutoScaling.Max {
			machineDeployment.Spec.Replicas = &nodePool.Spec.AutoScaling.Max
		}

		machineDeployment.Annotations[autoscalerMaxAnnotation] = strconv.Itoa(int(nodePool.Spec.AutoScaling.Max))
		machineDeployment.Annotations[autoscalerMinAnnotation] = strconv.Itoa(int(nodePool.Spec.AutoScaling.Min))
	}

	// If autoscaling is NOT enabled we reset min/max annotations and reconcile replicas.
	if !isAutoscalingEnabled(nodePool) {
		machineDeployment.Annotations[autoscalerMaxAnnotation] = "0"
		machineDeployment.Annotations[autoscalerMinAnnotation] = "0"
		machineDeployment.Spec.Replicas = ptr.To(ptr.Deref(nodePool.Spec.Replicas, 0))
	}
}

// machineTemplateBuilders returns a client.Object with a particular (platform)MachineTemplate type.
// a func to mutate the (platform)MachineTemplate.spec, a json string representation for (platform)MachineTemplate.spec
// and an error.
func (c *CAPI) machineTemplateBuilders() (client.Object, func(object client.Object) error, string, error) {
	var mutateTemplate func(object client.Object) error
	var template client.Object
	var machineTemplateSpec interface{}

	nodePool := c.nodePool
	capiClusterName := c.capiClusterName
	hcluster := c.hostedCluster
	createDefaultAWSSecurityGroup := c.cpoCapabilities.CreateDefaultAWSSecurityGroup
	releaseImage := c.releaseImage

	switch nodePool.Spec.Platform.Type {
	// Define the desired template type and mutateTemplate function.
	case hyperv1.AWSPlatform:
		template = &capiaws.AWSMachineTemplate{}
		var err error
		machineTemplateSpec, err = awsMachineTemplateSpec(capiClusterName, hcluster, nodePool, createDefaultAWSSecurityGroup, releaseImage)
		if err != nil {
			return nil, nil, "", err
		}
		mutateTemplate = func(object client.Object) error {
			o, _ := object.(*capiaws.AWSMachineTemplate)
			o.Spec = *machineTemplateSpec.(*capiaws.AWSMachineTemplateSpec)
			if o.Annotations == nil {
				o.Annotations = make(map[string]string)
			}
			o.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
			return nil
		}
	case hyperv1.AgentPlatform:
		template = &agentv1.AgentMachineTemplate{}
		machineTemplateSpec = agentMachineTemplateSpec(nodePool)
		mutateTemplate = func(object client.Object) error {
			o, _ := object.(*agentv1.AgentMachineTemplate)
			o.Spec = *machineTemplateSpec.(*agentv1.AgentMachineTemplateSpec)
			if o.Annotations == nil {
				o.Annotations = make(map[string]string)
			}
			o.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
			return nil
		}
	case hyperv1.KubevirtPlatform:
		template = &capikubevirt.KubevirtMachineTemplate{}
		var err error
		machineTemplateSpec, err = kubevirt.MachineTemplateSpec(nodePool, hcluster, c.releaseImage, nil)
		if err != nil {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidMachineTemplateConditionType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.InvalidKubevirtMachineTemplate,
				Message:            err.Error(),
				ObservedGeneration: nodePool.Generation,
			})

			return nil, nil, "", err
		} else {
			removeStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolValidMachineTemplateConditionType)
		}

		mutateTemplate = func(object client.Object) error {
			o, _ := object.(*capikubevirt.KubevirtMachineTemplate)
			o.Spec = *machineTemplateSpec.(*capikubevirt.KubevirtMachineTemplateSpec)
			if o.Annotations == nil {
				o.Annotations = make(map[string]string)
			}
			o.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
			return nil
		}
	case hyperv1.AzurePlatform:
		var err error
		template = &capiazure.AzureMachineTemplate{}
		machineTemplateSpec, err = azureMachineTemplateSpec(nodePool)
		if err != nil {
			return nil, nil, "", err
		}
		mutateTemplate = func(object client.Object) error {
			o, _ := object.(*capiazure.AzureMachineTemplate)

			// The azure api requires passing a public key. This key is randomly generated, the private portion is thrown away and the public key
			// gets written to the template.
			sshKey := o.Spec.Template.Spec.SSHPublicKey
			if sshKey == "" {
				sshKey, err = generateSSHPubkey()
				if err != nil {
					return fmt.Errorf("failed to generate a SSH key: %w", err)
				}
			}

			o.Spec = *machineTemplateSpec.(*capiazure.AzureMachineTemplateSpec)
			o.Spec.Template.Spec.SSHPublicKey = sshKey

			if o.Annotations == nil {
				o.Annotations = make(map[string]string)
			}
			o.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
			return nil
		}

	case hyperv1.PowerVSPlatform:
		template = &capipowervs.IBMPowerVSMachineTemplate{}
		var err error
		machineTemplateSpec, err = ibmPowerVSMachineTemplateSpec(hcluster, nodePool, c.releaseImage)
		if err != nil {
			return nil, nil, "", err
		}
		mutateTemplate = func(object client.Object) error {
			o, _ := object.(*capipowervs.IBMPowerVSMachineTemplate)
			o.Spec = *machineTemplateSpec.(*capipowervs.IBMPowerVSMachineTemplateSpec)
			if o.Annotations == nil {
				o.Annotations = make(map[string]string)
			}
			o.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
			return nil
		}
	case hyperv1.OpenStackPlatform:
		template = &capiopenstackv1beta1.OpenStackMachineTemplate{}
		var err error
		machineTemplateSpec, err = openstack.MachineTemplateSpec(hcluster, nodePool)
		if err != nil {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidMachineTemplateConditionType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.InvalidOpenStackMachineTemplate,
				Message:            err.Error(),
				ObservedGeneration: nodePool.Generation,
			})

			return nil, nil, "", err
		} else {
			removeStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolValidMachineTemplateConditionType)
		}

		mutateTemplate = func(object client.Object) error {
			o, _ := object.(*capiopenstackv1beta1.OpenStackMachineTemplate)
			o.Spec = *machineTemplateSpec.(*capiopenstackv1beta1.OpenStackMachineTemplateSpec)
			if o.Annotations == nil {
				o.Annotations = make(map[string]string)
			}
			o.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
			return nil
		}
	default:
		// TODO(alberto): Consider signal in a condition.
		return nil, nil, "", fmt.Errorf("unsupported platform type: %s", nodePool.Spec.Platform.Type)
	}
	template.SetNamespace(manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name))

	machineTemplateSpecJSON, err := json.Marshal(machineTemplateSpec)
	if err != nil {
		return nil, nil, "", err
	}

	template.SetName(generateMachineTemplateName(nodePool, machineTemplateSpecJSON))

	return template, mutateTemplate, string(machineTemplateSpecJSON), nil
}

func generateMachineTemplateName(nodePool *hyperv1.NodePool, machineTemplateSpecJSON []byte) string {
	// using HashStruct(machineTemplateSpecJSON) ensures a rolling upgrade is triggered
	// by creating a new template with a different name if any field changes.
	return getName(nodePool.GetName(), supportutil.HashSimple(machineTemplateSpecJSON),
		validation.DNS1123SubdomainMaxLength)
}

func (c *CAPI) reconcileMachineSet(ctx context.Context,
	machineSet *capiv1.MachineSet,
	machineTemplateCR client.Object) error {

	nodePool := c.nodePool
	userDataSecret := c.UserDataSecret()
	capiClusterName := c.capiClusterName
	targetVersion := c.Version()
	targetConfigHash := c.HashWithoutVersion()
	targetConfigVersionHash := c.Hash()

	log := ctrl.LoggerFrom(ctx)
	// Set annotations and labels
	if machineSet.GetAnnotations() == nil {
		machineSet.Annotations = map[string]string{}
	}
	machineSet.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
	// Delete any paused annotation
	delete(machineSet.Annotations, capiv1.PausedAnnotation)
	if machineSet.GetLabels() == nil {
		machineSet.Labels = map[string]string{}
	}
	machineSet.Labels[capiv1.ClusterNameLabel] = capiClusterName

	resourcesName := generateName(capiClusterName, nodePool.Spec.ClusterName, nodePool.GetName())
	machineSet.Spec.MinReadySeconds = int32(0)

	gvk, err := apiutil.GVKForObject(machineTemplateCR, api.Scheme)
	if err != nil {
		return err
	}

	// Set MaxUnavailable for the inplace upgrader to use
	maxUnavailable, err := getInPlaceMaxUnavailable(nodePool)
	if err != nil {
		return err
	}
	machineSet.Annotations[nodePoolAnnotationMaxUnavailable] = strconv.Itoa(maxUnavailable)

	// Set selector and template
	machineSet.Spec.ClusterName = capiClusterName
	if machineSet.Spec.Selector.MatchLabels == nil {
		machineSet.Spec.Selector.MatchLabels = map[string]string{}
	}
	machineSet.Spec.Selector.MatchLabels[resourcesName] = resourcesName
	machineSet.Spec.Template = capiv1.MachineTemplateSpec{
		ObjectMeta: capiv1.ObjectMeta{
			Labels: map[string]string{
				resourcesName:           resourcesName,
				capiv1.ClusterNameLabel: capiClusterName,
			},
			// Annotations here propagate down to Machines
			// https://cluster-api.sigs.k8s.io/developer/architecture/controllers/metadata-propagation.html#machinedeployment.
			Annotations: map[string]string{
				nodePoolAnnotation: client.ObjectKeyFromObject(nodePool).String(),
			},
		},

		Spec: capiv1.MachineSpec{
			ClusterName: capiClusterName,
			Bootstrap: capiv1.Bootstrap{
				// Keep current user data for later check.
				DataSecretName: machineSet.Spec.Template.Spec.Bootstrap.DataSecretName,
			},
			InfrastructureRef: corev1.ObjectReference{
				Kind:       gvk.Kind,
				APIVersion: gvk.GroupVersion().String(),
				Namespace:  machineTemplateCR.GetNamespace(),
				// Keep current version for later check.
				Name: machineSet.Spec.Template.Spec.InfrastructureRef.Name,
			},
			// Keep current version for later check.
			Version:                 machineSet.Spec.Template.Spec.Version,
			NodeDrainTimeout:        nodePool.Spec.NodeDrainTimeout,
			NodeVolumeDetachTimeout: nodePool.Spec.NodeVolumeDetachTimeout,
		},
	}

	// Propagate labels.
	for k, v := range nodePool.Spec.NodeLabels {
		// Propagated managed labels down to Machines with a known hardcoded prefix
		// so the CPO HCCO Node controller can recognise them and apply them to Nodes.
		labelKey := fmt.Sprintf("%s.%s", labelManagedPrefix, k)
		machineSet.Spec.Template.Labels[labelKey] = v
	}

	// Propagate taints.
	taintsInJSON, err := taintsToJSON(nodePool.Spec.Taints)
	if err != nil {
		return err
	}
	machineSet.Spec.Template.Annotations[nodePoolAnnotationTaints] = taintsInJSON

	setMachineSetReplicas(nodePool, machineSet)

	isUpdating := false
	// Propagate version and userData Secret to the MachineSet.
	if userDataSecret.Name != ptr.Deref(machineSet.Spec.Template.Spec.Bootstrap.DataSecretName, "") {
		log.Info("New user data Secret has been generated",
			"current", machineSet.Spec.Template.Spec.Bootstrap.DataSecretName,
			"target", userDataSecret.Name)

		// TODO (alberto): possibly compare with NodePool here instead so we don't rely on impl details to drive decisions.
		if targetVersion != ptr.Deref(machineSet.Spec.Template.Spec.Version, "") {
			log.Info("Starting version upgrade: Propagating new version to the MachineSet",
				"releaseImage", nodePool.Spec.Release.Image, "target", targetVersion)
		}

		if targetConfigHash != nodePool.Annotations[nodePoolAnnotationCurrentConfig] {
			log.Info("Starting config upgrade: Propagating new config to the MachineSet",
				"current", nodePool.Annotations[nodePoolAnnotationCurrentConfig], "target", targetConfigHash)
		}
		machineSet.Spec.Template.Spec.Version = &targetVersion
		machineSet.Spec.Template.Spec.Bootstrap.DataSecretName = ptr.To(userDataSecret.Name)

		// Signal in-place upgrade request.
		machineSet.Annotations[nodePoolAnnotationTargetConfigVersion] = targetConfigVersionHash

		// If the machineSet is brand new, set current version to target so in-place upgrade no-op.
		if _, ok := machineSet.Annotations[nodePoolAnnotationCurrentConfigVersion]; !ok {
			machineSet.Annotations[nodePoolAnnotationCurrentConfigVersion] = targetConfigVersionHash
		}
		isUpdating = true
	}

	// template spec has changed, signal a rolling upgrade.
	if machineTemplateCR.GetName() != machineSet.Spec.Template.Spec.InfrastructureRef.Name {
		log.Info("New machine template has been generated",
			"current", machineSet.Spec.Template.Spec.InfrastructureRef.Name,
			"target", machineTemplateCR.GetName())

		machineSet.Spec.Template.Spec.InfrastructureRef.Name = machineTemplateCR.GetName()
		isUpdating = true
	}

	if isUpdating {
		// We return early here during a version/config/MachineTemplate update to persist the resource with new user data Secret / MachineTemplate,
		// so in the next reconciling loop we get a new MachineDeployment.Generation
		// and we can do a legit MachineDeploymentComplete/MachineDeployment.Status.ObservedGeneration check.
		return nil
	}

	if machineSetInPlaceRolloutIsComplete(machineSet) {
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

		if nodePool.Annotations[nodePoolAnnotationPlatformMachineTemplate] != machineTemplateCR.GetName() {
			log.Info("Rolling upgrade complete",
				"previous", nodePool.Annotations[nodePoolAnnotationPlatformMachineTemplate], "new", machineTemplateCR.GetName())
			nodePool.Annotations[nodePoolAnnotationPlatformMachineTemplate] = machineTemplateCR.GetName()
		}
	}

	// Bubble up upgrading NodePoolUpdatingVersionConditionType.
	var status corev1.ConditionStatus
	reason := ""
	message := ""
	status = "unknown"
	removeStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolUpdatingVersionConditionType)

	if _, ok := machineSet.Annotations[nodePoolAnnotationUpgradeInProgressTrue]; ok {
		status = corev1.ConditionTrue
		reason = hyperv1.AsExpectedReason
	}

	if _, ok := machineSet.Annotations[nodePoolAnnotationUpgradeInProgressFalse]; ok {
		status = corev1.ConditionFalse
		reason = hyperv1.NodePoolInplaceUpgradeFailedReason
	}

	// Check if config needs to be updated.
	isUpdatingConfig := isUpdatingConfig(nodePool, targetConfigHash)

	// Check if version needs to be updated.
	isUpdatingVersion := isUpdatingVersion(nodePool, targetVersion)

	if isUpdatingVersion {
		message = fmt.Sprintf("Updating Version, Target: %v", machineSet.Annotations[nodePoolAnnotationTargetConfigVersion])
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingVersionConditionType,
			Status:             status,
			ObservedGeneration: nodePool.Generation,
			Message:            message,
			Reason:             reason,
		})
	} else {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingVersionConditionType,
			Status:             corev1.ConditionFalse,
			ObservedGeneration: nodePool.Generation,
			Reason:             hyperv1.AsExpectedReason,
		})
	}

	if isUpdatingConfig {
		message = fmt.Sprintf("Updating Config, Target: %v", machineSet.Annotations[nodePoolAnnotationTargetConfigVersion])
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingConfigConditionType,
			Status:             status,
			ObservedGeneration: nodePool.Generation,
			Message:            message,
			Reason:             reason,
		})
	} else {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolUpdatingConfigConditionType,
			Status:             corev1.ConditionFalse,
			ObservedGeneration: nodePool.Generation,
			Reason:             hyperv1.AsExpectedReason,
		})
	}

	// Bubble up AvailableReplicas and Ready condition from MachineSet.
	nodePool.Status.Replicas = machineSet.Status.AvailableReplicas
	for _, c := range machineSet.Status.Conditions {
		// This condition should aggregate and summarise readiness from underlying MachineSets and Machines
		// https://github.com/kubernetes-sigs/cluster-api/issues/3486.
		if c.Type == capiv1.ReadyCondition {
			// this is so api server does not complain
			// invalid value: \"\": status.conditions.reason in body should be at least 1 chars long"
			reason := hyperv1.AsExpectedReason
			if c.Reason != "" {
				reason = c.Reason
			}

			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
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

func machineSetInPlaceRolloutIsComplete(machineSet *capiv1.MachineSet) bool {
	return machineSet.Annotations[nodePoolAnnotationCurrentConfigVersion] == machineSet.Annotations[nodePoolAnnotationTargetConfigVersion]
}

// setMachineSetReplicas sets wanted replicas:
// If autoscaling is enabled we reconcile min/max annotations and leave replicas untouched.
func setMachineSetReplicas(nodePool *hyperv1.NodePool, machineSet *capiv1.MachineSet) {
	if machineSet.Annotations == nil {
		machineSet.Annotations = make(map[string]string)
	}

	if isAutoscalingEnabled(nodePool) {
		// The MachineSet replicas field should default to a value inside the (min size, max size) range based on the autoscaler annotations
		// so the autoscaler can take control of the replicas field.
		//
		// 1. if it’s a new MachineSet, or the replicas field of the old MachineSet is < min size, use min size
		// 2. if the replicas field of the old MachineSet is > max size, use max size
		msReplicas := ptr.Deref(machineSet.Spec.Replicas, 0)
		if msReplicas < nodePool.Spec.AutoScaling.Min {
			machineSet.Spec.Replicas = &nodePool.Spec.AutoScaling.Min
		} else if msReplicas > nodePool.Spec.AutoScaling.Max {
			machineSet.Spec.Replicas = &nodePool.Spec.AutoScaling.Max
		}

		machineSet.Annotations[autoscalerMaxAnnotation] = strconv.Itoa(int(nodePool.Spec.AutoScaling.Max))
		machineSet.Annotations[autoscalerMinAnnotation] = strconv.Itoa(int(nodePool.Spec.AutoScaling.Min))
	}

	// If autoscaling is NOT enabled we reset min/max annotations and reconcile replicas.
	if !isAutoscalingEnabled(nodePool) {
		machineSet.Annotations[autoscalerMaxAnnotation] = "0"
		machineSet.Annotations[autoscalerMinAnnotation] = "0"
		machineSet.Spec.Replicas = ptr.To(ptr.Deref(nodePool.Spec.Replicas, 0))
	}
}

func getInPlaceMaxUnavailable(nodePool *hyperv1.NodePool) (int, error) {
	intOrPercent := intstr.FromInt(1)
	if nodePool.Spec.Management.InPlace != nil {
		if nodePool.Spec.Management.InPlace.MaxUnavailable != nil {
			intOrPercent = *nodePool.Spec.Management.InPlace.MaxUnavailable
		}
	}
	replicas := int(ptr.Deref(nodePool.Spec.Replicas, 0))
	maxUnavailable, err := intstr.GetScaledValueFromIntOrPercent(&intOrPercent, replicas, false)
	if err != nil {
		return 0, err
	}
	if maxUnavailable == 0 {
		maxUnavailable = 1
	}
	return maxUnavailable, nil
}

func (c *CAPI) machineDeployment() *capiv1.MachineDeployment {
	return &capiv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.nodePool.GetName(),
			Namespace: c.controlplaneNamespace,
		},
	}
}

func (c *CAPI) machineSet() *capiv1.MachineSet {
	return &capiv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.nodePool.GetName(),
			Namespace: c.controlplaneNamespace,
		},
	}
}

func (c *CAPI) machineHealthCheck() *capiv1.MachineHealthCheck {
	return &capiv1.MachineHealthCheck{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.nodePool.GetName(),
			Namespace: c.controlplaneNamespace,
		},
	}
}

// TODO (alberto) drop this deterministic naming logic and get the name for child MachineDeployment from the status/annotation/label?
func generateName(infraName, clusterName, suffix string) string {
	return getName(fmt.Sprintf("%s-%s", infraName, clusterName), suffix, 43)
}

// getName returns a name given a base ("deployment-5") and a suffix ("deploy")
// It will first attempt to join them with a dash. If the resulting name is longer
// than maxLength: if the suffix is too long, it will truncate the base name and add
// an 8-character hash of the [base]-[suffix] string.  If the suffix is not too long,
// it will truncate the base, add the hash of the base and return [base]-[hash]-[suffix]
func getName(base, suffix string, maxLength int) string {
	if maxLength <= 0 {
		return ""
	}
	name := fmt.Sprintf("%s-%s", base, suffix)
	if len(name) <= maxLength {
		return name
	}

	// length of -hash-
	baseLength := maxLength - 10 - len(suffix)

	// if the suffix is too long, ignore it
	if baseLength < 1 {
		prefix := base[0:min(len(base), max(0, maxLength-9))]
		// Calculate hash on initial base-suffix string
		shortName := fmt.Sprintf("%s-%s", prefix, supportutil.HashSimple(name))
		return shortName[:min(maxLength, len(shortName))]
	}

	prefix := base[0:baseLength]
	// Calculate hash on initial base-suffix string
	return fmt.Sprintf("%s-%s-%s", prefix, supportutil.HashSimple(base), suffix)
}

// max returns the greater of its 2 inputs
func max(a, b int) int {
	if b > a {
		return b
	}
	return a
}

// min returns the lesser of its 2 inputs
func min(a, b int) int {
	if b < a {
		return b
	}
	return a
}

func (c *CAPI) listMachineTemplates() ([]client.Object, error) {
	machineTemplateList := &unstructured.UnstructuredList{}
	nodePool := c.nodePool
	var gvk schema.GroupVersionKind
	var err error
	switch nodePool.Spec.Platform.Type {
	// Define the desired template type and mutateTemplate function.
	case hyperv1.AWSPlatform:
		gvk, err = apiutil.GVKForObject(&capiaws.AWSMachineTemplate{}, api.Scheme)
		if err != nil {
			return nil, err
		}
	case hyperv1.KubevirtPlatform:
		gvk, err = apiutil.GVKForObject(&capikubevirt.KubevirtMachineTemplate{}, api.Scheme)
		if err != nil {
			return nil, err
		}
	case hyperv1.AgentPlatform:
		gvk, err = apiutil.GVKForObject(&agentv1.AgentMachine{}, api.Scheme)
		if err != nil {
			return nil, err
		}
	case hyperv1.AzurePlatform:
		gvk, err = apiutil.GVKForObject(&capiazure.AzureMachineTemplate{}, api.Scheme)
		if err != nil {
			return nil, err
		}
	case hyperv1.OpenStackPlatform:
		gvk, err = apiutil.GVKForObject(&capiopenstackv1beta1.OpenStackMachineTemplate{}, api.Scheme)
		if err != nil {
			return nil, err
		}
	default:
		// need a default path that returns a value that does not cause the hypershift operator to crash
		// if no explicit machineTemplate is defined safe to assume none exist
		return nil, nil
	}

	machineTemplateList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   gvk.Group,
		Kind:    gvk.Kind,
		Version: gvk.Version,
	})
	if err := c.List(context.Background(), machineTemplateList); err != nil {
		return nil, fmt.Errorf("failed to list MachineTemplates: %w", err)
	}
	var filtered []client.Object
	for i, machineTemplate := range machineTemplateList.Items {
		if machineTemplate.GetAnnotations() != nil {
			if annotation, ok := machineTemplate.GetAnnotations()[nodePoolAnnotation]; ok &&
				annotation == client.ObjectKeyFromObject(nodePool).String() {
				filtered = append(filtered, &machineTemplateList.Items[i])
			}
		}
	}

	return filtered, nil
}

// TODO (alberto): Let the all the deletion logic be a capi func.
// This is kept like this for now to contain the scope of the refactor and avoid backward compatibility issues.

// ensureMachineDeletion ensures all the machines belonging to the NodePool's MachineSet are fully deleted.
// This function can be deleted once the upstream PR (https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/3805) is merged and pulled into https://github.com/openshift/cluster-api-provider-aws.
// This function is necessary to ensure AWSMachines are fully deleted prior to deleting the NodePull secrets being deleted due to a bug introduced by https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/2271
// See https://github.com/openshift/hypershift/pull/1826#discussion_r1007349564 for more details.
func (r *NodePoolReconciler) ensureMachineDeletion(ctx context.Context, nodePool *hyperv1.NodePool) error {
	machines, err := r.getMachinesForNodePool(ctx, nodePool)
	if err != nil {
		return fmt.Errorf("error getting Machines: %w", err)
	}

	if len(machines) > 0 {
		return fmt.Errorf("there are still Machines in for NodePool %q", nodePool.Name)
	}

	return nil
}

// getMachinesForNodePool get all Machines listed with the nodePoolAnnotation
// within the control plane Namespace for that NodePool.
func (r *NodePoolReconciler) getMachinesForNodePool(ctx context.Context, nodePool *hyperv1.NodePool) ([]*capiv1.Machine, error) {
	npAnnotation := client.ObjectKeyFromObject(nodePool).String()
	machines := capiv1.MachineList{}
	controlPlaneNamespace := fmt.Sprintf("%s-%s", nodePool.Namespace, strings.ReplaceAll(nodePool.Spec.ClusterName, ".", "-"))

	if err := r.List(ctx, &machines, &client.ListOptions{Namespace: controlPlaneNamespace}); err != nil {
		return nil, fmt.Errorf("failed to list Machines: %w", err)
	}

	// Filter out only machines belonging to deleted NodePool
	var machinesForNodePool []*capiv1.Machine
	for i, machine := range machines.Items {
		if machine.Annotations[nodePoolAnnotation] == npAnnotation {
			machinesForNodePool = append(machinesForNodePool, &machines.Items[i])
		}
	}

	return sortedByCreationTimestamp(machinesForNodePool), nil
}
