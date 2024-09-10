package nodepool

import (
	"bufio"
	"bytes"
	"context"
	coreerrors "errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/go-logr/logr"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	performanceprofilev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	supportutil "github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	serializer "k8s.io/apimachinery/pkg/runtime/serializer/json"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/utils/ptr"
	"k8s.io/utils/set"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MirrorConfig holds the information needed to mirror a config object to HCP namespace
type MirrorConfig struct {
	*corev1.ConfigMap
	Labels map[string]string
}

// reconcileMirroredConfigs mirrors configs into
// the HCP namespace, that are needed as an input for certain operators (such as NTO)
func (r *NodePoolReconciler) reconcileMirroredConfigs(ctx context.Context, logr logr.Logger, mirroredConfigs []*MirrorConfig, controlPlaneNamespace string, nodePool *hyperv1.NodePool) error {
	// get configs which already mirrored to the HCP namespace
	existingConfigsList := &corev1.ConfigMapList{}
	if err := r.List(ctx, existingConfigsList, &client.ListOptions{
		Namespace: controlPlaneNamespace,
		LabelSelector: labels.SelectorFromValidatedSet(labels.Set{
			NTOMirroredConfigLabel: "true",
			hyperv1.NodePoolLabel:  nodePool.Name}),
	}); err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	want := set.Set[string]{}
	for _, mirroredConfig := range mirroredConfigs {
		want.Insert(supportutil.ShortenName(mirroredConfig.Name, nodePool.Name, validation.LabelValueMaxLength))
	}
	have := set.Set[string]{}
	for _, configMap := range existingConfigsList.Items {
		have.Insert(configMap.Name)
	}
	toCreate, toDelete := want.Difference(have), have.Difference(want)
	if len(toCreate) > 0 {
		logr = logr.WithValues("toCreate", toCreate.UnsortedList())
	}
	if len(toDelete) > 0 {
		logr = logr.WithValues("toDelete", toDelete.UnsortedList())
	}
	if len(toCreate) > 0 || len(toDelete) > 0 {
		logr.Info("updating mirrored configs")
	}
	// delete the redundant configs that are no longer part of the nodepool spec
	for i := 0; i < len(existingConfigsList.Items); i++ {
		existingConfig := &existingConfigsList.Items[i]
		if !toDelete.Has(existingConfig.Name) {
			continue
		}
		_, err := supportutil.DeleteIfNeeded(ctx, r.Client, existingConfig)
		if err != nil {
			return fmt.Errorf("failed to delete ConfigMap %s: %w", client.ObjectKeyFromObject(existingConfig).String(), err)
		}
	}
	// NTO also generates config in the HCP namespace.
	ntoGeneratedKubeletConfigs := &corev1.ConfigMapList{}
	if err := r.List(ctx, ntoGeneratedKubeletConfigs, &client.ListOptions{
		Namespace: controlPlaneNamespace,
		LabelSelector: labels.SelectorFromValidatedSet(labels.Set{
			nodeTuningGeneratedConfigLabel: "true",
			KubeletConfigConfigMapLabel:    "true",
			hyperv1.NodePoolLabel:          nodePool.Name,
		}),
	}); err != nil {
		return err
	}
	// we need to validate that generated configs and user-provided configs
	// are not conflicting with each other, before we create the new ones
	err := validateMirroredConfigs(ntoGeneratedKubeletConfigs.Items, mirroredConfigs, nodePool.Name)
	if err != nil {
		return fmt.Errorf("failed to validate mirrored configs: %w", err)
	}

	// update or create the configs that need to be mirrored into the HCP NS
	for _, mirroredConfig := range mirroredConfigs {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      supportutil.ShortenName(mirroredConfig.Name, nodePool.Name, validation.LabelValueMaxLength),
				Namespace: controlPlaneNamespace},
		}
		if result, err := r.CreateOrUpdate(ctx, r.Client, cm, func() error {
			return mutateMirroredConfig(cm, mirroredConfig, nodePool)
		}); err != nil {
			return fmt.Errorf("failed to reconcile mirrored %s/%s ConfigMap: %w", cm.Namespace, cm.Name, err)
		} else {
			logr.Info("Reconciled ConfigMap", "result", result)
		}
	}
	return nil
}

func validateMirroredConfigs(generatedKubeletConfigs []corev1.ConfigMap, mirroredConfigs []*MirrorConfig, nodePoolName string) error {
	KubeletConfigConfigMapCount := len(generatedKubeletConfigs)

	for _, mirroredConfig := range mirroredConfigs {
		if _, ok := mirroredConfig.Labels[KubeletConfigConfigMapLabel]; ok {
			KubeletConfigConfigMapCount++
		}
	}
	if KubeletConfigConfigMapCount > 1 {
		// whether the config provided by the user or by NTO, only a single KubeletConfig ConfigMap allow per NodePool
		var ntoGeneratedKubeletConfigNames, userProvidedConfigNames []string
		for _, ntoGenerated := range generatedKubeletConfigs {
			ntoGeneratedKubeletConfigNames = append(ntoGeneratedKubeletConfigNames, ntoGenerated.Name)
		}
		for _, mirroredConfig := range mirroredConfigs {
			userProvidedConfigNames = append(userProvidedConfigNames, mirroredConfig.Name)
		}
		return fmt.Errorf("more than a single KubeletConfig ConfigMap is associated with NodePool %s. please delete the redundant configs: NTO generated KubeletConfigs %v user provided KubeletConfigs %v",
			nodePoolName, ntoGeneratedKubeletConfigNames, userProvidedConfigNames)
	}
	return nil
}

func reconcileNodeTuningConfigMap(tuningConfigMap *corev1.ConfigMap, nodePool *hyperv1.NodePool, rawConfig string) error {
	tuningConfigMap.Immutable = ptr.To(false)
	if tuningConfigMap.Annotations == nil {
		tuningConfigMap.Annotations = make(map[string]string)
	}
	if tuningConfigMap.Labels == nil {
		tuningConfigMap.Labels = make(map[string]string)
	}

	tuningConfigMap.Annotations[nodePoolAnnotation] = client.ObjectKeyFromObject(nodePool).String()
	tuningConfigMap.Labels[nodePoolAnnotation] = nodePool.GetName()

	if tuningConfigMap.Data == nil {
		tuningConfigMap.Data = map[string]string{}
	}
	tuningConfigMap.Data[tuningConfigKey] = rawConfig

	return nil
}

// reconcileTunedConfigMap inserts the Tuned object manifest in tunedConfig into ConfigMap tunedConfigMap.
// This is used to mirror the Tuned object manifest into the control plane namespace, for the Node
// Tuning Operator to mirror and reconcile in the hosted cluster.
func reconcileTunedConfigMap(tunedConfigMap *corev1.ConfigMap, nodePool *hyperv1.NodePool, tunedConfig string) error {
	if err := reconcileNodeTuningConfigMap(tunedConfigMap, nodePool, tunedConfig); err != nil {
		return err
	}
	tunedConfigMap.Labels[tunedConfigMapLabel] = "true"
	return nil
}

// reconcilePerformanceProfileConfigMap inserts the PerformanceProfile object manifest in performanceProfileConfig into ConfigMap performanceProfileConfigMap.
// This is used to mirror the PerformanceProfile object manifest into the control plane namespace, for the Node
// Tuning Operator to mirror and reconcile in the hosted cluster.
func reconcilePerformanceProfileConfigMap(performanceProfileConfigMap *corev1.ConfigMap, nodePool *hyperv1.NodePool, performanceProfileConfig string) error {
	if err := reconcileNodeTuningConfigMap(performanceProfileConfigMap, nodePool, performanceProfileConfig); err != nil {
		return err
	}
	performanceProfileConfigMap.Labels[PerformanceProfileConfigMapLabel] = "true"
	return nil
}

func mutateMirroredConfig(cm *corev1.ConfigMap, mirroredConfig *MirrorConfig, nodePool *hyperv1.NodePool) error {
	cm.Immutable = ptr.To(true)
	if cm.Annotations == nil {
		cm.Annotations = make(map[string]string)
	}
	if cm.Labels == nil {
		cm.Labels = make(map[string]string)
	}
	cm.Labels[hyperv1.NodePoolLabel] = nodePool.GetName()
	cm.Labels[NTOMirroredConfigLabel] = "true"
	cm.Labels = labels.Merge(cm.Labels, mirroredConfig.Labels)
	cm.Data = mirroredConfig.Data
	return nil
}

func (r *NodePoolReconciler) getTuningConfig(ctx context.Context,
	nodePool *hyperv1.NodePool,
) (string, string, string, error) {
	var (
		configs                              []corev1.ConfigMap
		tunedAllConfigPlainText              []string
		performanceProfileConfigMapName      string
		performanceProfileAllConfigPlainText []string
		errors                               []error
	)

	for _, config := range nodePool.Spec.TuningConfig {
		configConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.Name,
				Namespace: nodePool.Namespace,
			},
		}
		if err := r.Get(ctx, client.ObjectKeyFromObject(configConfigMap), configConfigMap); err != nil {
			errors = append(errors, err)
			continue
		}
		configs = append(configs, *configConfigMap)
	}

	for _, config := range configs {
		manifestRaw, ok := config.Data[tuningConfigKey]
		if !ok {
			errors = append(errors, fmt.Errorf("no manifest found in configmap %q with key %q", config.Name, tuningConfigKey))
			continue
		}
		manifestTuned, manifestPerformanceProfile, err := validateTuningConfigManifest([]byte(manifestRaw))
		if err != nil {
			errors = append(errors, fmt.Errorf("configmap %q failed validation: %w", config.Name, err))
			continue
		}
		if manifestTuned != nil {
			tunedAllConfigPlainText = append(tunedAllConfigPlainText, string(manifestTuned))
		}
		if manifestPerformanceProfile != nil {
			performanceProfileConfigMapName = config.Name
			performanceProfileAllConfigPlainText = append(performanceProfileAllConfigPlainText, string(manifestPerformanceProfile))
		}
	}

	if len(performanceProfileAllConfigPlainText) > 1 {
		errors = append(errors, fmt.Errorf("there cannot be more than one PerformanceProfile per NodePool. found: %d", len(performanceProfileAllConfigPlainText)))
	}

	// Keep output deterministic to avoid unnecessary no-op changes to Tuned ConfigMap
	sort.Strings(tunedAllConfigPlainText)
	sort.Strings(performanceProfileAllConfigPlainText)

	return strings.Join(tunedAllConfigPlainText, "\n---\n"), strings.Join(performanceProfileAllConfigPlainText, "\n---\n"), performanceProfileConfigMapName, utilerrors.NewAggregate(errors)

}

func validateTuningConfigManifest(manifest []byte) ([]byte, []byte, error) {
	scheme := runtime.NewScheme()
	tunedv1.AddToScheme(scheme)
	performanceprofilev2.AddToScheme(scheme)

	yamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, scheme, scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)
	cr, _, err := yamlSerializer.Decode(manifest, nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("error decoding config: %w", err)
	}

	switch obj := cr.(type) {
	case *tunedv1.Tuned:
		buff := bytes.Buffer{}
		if err := yamlSerializer.Encode(obj, &buff); err != nil {
			return nil, nil, fmt.Errorf("failed to encode Tuned object: %w", err)
		}
		manifest = buff.Bytes()
		return manifest, nil, nil

	case *performanceprofilev2.PerformanceProfile:
		validationErrors := obj.ValidateBasicFields()
		if len(validationErrors) > 0 {
			return nil, nil, fmt.Errorf("PerformanceProfile validation failed pp:%s : %w", obj.Name, coreerrors.Join(validationErrors.ToAggregate().Errors()...))
		}

		buff := bytes.Buffer{}
		if err := yamlSerializer.Encode(obj, &buff); err != nil {
			return nil, nil, fmt.Errorf("failed to encode performance profile after defaulting it: %w", err)
		}
		manifest = buff.Bytes()
		return nil, manifest, nil

	default:
		return nil, nil, fmt.Errorf("unsupported tuningConfig object type: %T", obj)
	}
}

// SetPerformanceProfileConditions checks for performance profile status updates, and reflects them in the nodepool status conditions
func (r *NodePoolReconciler) SetPerformanceProfileConditions(ctx context.Context, logger logr.Logger, nodePool *hyperv1.NodePool, controlPlaneNamespace string, toDelete bool) error {
	if toDelete {
		performanceProfileConditions := []string{
			hyperv1.NodePoolPerformanceProfileTuningAvailableConditionType,
			hyperv1.NodePoolPerformanceProfileTuningProgressingConditionType,
			hyperv1.NodePoolPerformanceProfileTuningUpgradeableConditionType,
			hyperv1.NodePoolPerformanceProfileTuningDegradedConditionType,
		}
		for _, condition := range performanceProfileConditions {
			removeStatusCondition(&nodePool.Status.Conditions, condition)
		}
		return nil
	}
	// Get performance profile status configmap
	cmList := &corev1.ConfigMapList{}
	if err := r.Client.List(ctx, cmList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{
			NodeTuningGeneratedPerformanceProfileStatusLabel: "true",
			hyperv1.NodePoolLabel:                            nodePool.Name}),
		Namespace: controlPlaneNamespace,
	}); err != nil {
		return err
	}
	if len(cmList.Items) > 1 {
		return fmt.Errorf("there cannot be more than one PerformanceProfile ConfigMap status per NodePool. found: %d NodePool: %s", len(cmList.Items), nodePool.Name)
	}
	if len(cmList.Items) == 0 {
		// Only log here and do not return an error because it might take sometime for NTO to
		// generate the ConfigMap with the PerformanceProfile status.
		// The creation of the ConfigMap itself triggers the reconciliation loop which eventually calls
		// this flow again.
		logger.Error(nil, "no PerformanceProfile ConfigMap status found", "Namespace", controlPlaneNamespace, "NodePool", nodePool.Name)
		return nil
	}
	performanceProfileStatusConfigMap := cmList.Items[0]
	statusRaw, ok := performanceProfileStatusConfigMap.Data["status"]
	if !ok {
		return fmt.Errorf("status not found in performance profile status configmap")
	}
	status := &performanceprofilev2.PerformanceProfileStatus{}
	if err := yaml.Unmarshal([]byte(statusRaw), status); err != nil {
		return fmt.Errorf("failed to decode the performance profile status: %w", err)
	}

	for _, performanceProfileCondition := range status.Conditions {
		condition := hyperv1.NodePoolCondition{
			Type:               fmt.Sprintf("%s/%s", hyperv1.NodePoolPerformanceProfileTuningConditionTypePrefix, performanceProfileCondition.Type),
			Status:             performanceProfileCondition.Status,
			Reason:             performanceProfileCondition.Reason,
			Message:            performanceProfileCondition.Message,
			ObservedGeneration: nodePool.Generation,
		}
		oldCondition := FindStatusCondition(nodePool.Status.Conditions, condition.Type)

		// Will set the condition only if it was not set previously, or has changed
		if oldCondition == nil || oldCondition.ObservedGeneration != condition.ObservedGeneration {
			SetStatusCondition(&nodePool.Status.Conditions, condition)
		}
	}
	return nil
}

// getNTOGeneratedConfig gets all the configMaps in the controlplaneNamespace generated by the NTO.
func getNTOGeneratedConfig(ctx context.Context, cg *ConfigGenerator) ([]corev1.ConfigMap, error) {
	nodeTuningGeneratedConfigs := &corev1.ConfigMapList{}
	if err := cg.List(ctx, nodeTuningGeneratedConfigs, client.MatchingLabels{
		nodeTuningGeneratedConfigLabel: "true",
		hyperv1.NodePoolLabel:          cg.nodePool.GetName(),
	}, client.InNamespace(cg.controlplaneNamespace)); err != nil {
		return nil, err
	}
	return nodeTuningGeneratedConfigs.Items, nil
}

// BuildMirrorConfigs returns a slice of MirrorConfigs for user configs that are supposed
// to be mirrored to the HCP namespace.
func BuildMirrorConfigs(ctx context.Context, cg *ConfigGenerator) ([]*MirrorConfig, error) {
	userConfig, err := cg.getUserConfigs(ctx)
	if err != nil {
		return nil, err
	}

	var errors []error
	var mirrorConfigs []*MirrorConfig
	for i, config := range userConfig {
		cmPayload := config.Data[TokenSecretConfigKey]
		// ignition config-map payload may contain multiple manifests
		yamlReader := yaml.NewYAMLReader(bufio.NewReader(strings.NewReader(cmPayload)))
		for {
			manifestRaw, err := yamlReader.Read()
			if err != nil && err != io.EOF {
				errors = append(errors, fmt.Errorf("configmap %q contains invalid yaml: %w", config.Name, err))
				continue
			}
			if len(manifestRaw) != 0 && strings.TrimSpace(string(manifestRaw)) != "" {
				mirrorConfig, err := getMirrorConfigForManifest(manifestRaw)
				if err != nil {
					errors = append(errors, fmt.Errorf("configmap %q yaml document failed validation: %w", config.Name, err))
					continue
				}
				if mirrorConfig != nil {
					mirrorConfig.ConfigMap = &userConfig[i]
					mirrorConfigs = append(mirrorConfigs, mirrorConfig)
				}
			}
			if err == io.EOF {
				break
			}
		}
	}

	return mirrorConfigs, utilerrors.NewAggregate(errors)
}

// getMirrorConfigForManifest returns a MirrorConfig for a ContainerRuntimeConfig manifest or nil.
func getMirrorConfigForManifest(manifest []byte) (*MirrorConfig, error) {
	scheme := runtime.NewScheme()
	_ = mcfgv1.Install(scheme)

	yamlSerializer := serializer.NewSerializerWithOptions(
		serializer.DefaultMetaFactory, scheme, scheme,
		serializer.SerializerOptions{Yaml: true, Pretty: true, Strict: false},
	)
	// for manifests that should be mirrored into hosted control plane namespace
	var mirrorConfig *MirrorConfig
	cr, _, err := yamlSerializer.Decode(manifest, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("error decoding config: %w", err)
	}

	switch cr.(type) {
	case *mcfgv1.ContainerRuntimeConfig:
		mirrorConfig = &MirrorConfig{Labels: map[string]string{
			ContainerRuntimeConfigConfigMapLabel: "true",
			NTOMirroredConfigLabel:               "true",
		}}
	case *mcfgv1.KubeletConfig:
		mirrorConfig = &MirrorConfig{Labels: map[string]string{
			KubeletConfigConfigMapLabel: "true",
			NTOMirroredConfigLabel:      "true",
		}}
	}
	return mirrorConfig, err
}

func (r *NodePoolReconciler) ntoReconcile(ctx context.Context, nodePool *hyperv1.NodePool, configGenerator *ConfigGenerator, controlPlaneNamespace string) error {
	log := ctrl.LoggerFrom(ctx)

	mirroredConfigs, err := BuildMirrorConfigs(ctx, configGenerator)
	if err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidMachineConfigConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.NodePoolValidationFailedReason,
			Message:            err.Error(),
			ObservedGeneration: nodePool.Generation,
		})
		return fmt.Errorf("failed to build mirror configs: %w", err)
	}
	if err := r.reconcileMirroredConfigs(ctx, log, mirroredConfigs, controlPlaneNamespace, nodePool); err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidMachineConfigConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.NodePoolValidationFailedReason,
			Message:            err.Error(),
			ObservedGeneration: nodePool.Generation,
		})
		return fmt.Errorf("failed to mirror configs: %w", err)
	}

	// Validate tuningConfig input.
	tunedConfig, performanceProfileConfig, performanceProfileConfigMapName, err := r.getTuningConfig(ctx, nodePool)
	if err != nil {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidTuningConfigConditionType,
			Status:             corev1.ConditionFalse,
			Reason:             hyperv1.NodePoolValidationFailedReason,
			Message:            err.Error(),
			ObservedGeneration: nodePool.Generation,
		})
		return fmt.Errorf("failed to get tuningConfig: %w", err)
	}
	SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
		Type:               hyperv1.NodePoolValidTuningConfigConditionType,
		Status:             corev1.ConditionTrue,
		Reason:             hyperv1.AsExpectedReason,
		ObservedGeneration: nodePool.Generation,
	})

	tunedConfigMap := TunedConfigMap(controlPlaneNamespace, nodePool.Name)
	if tunedConfig == "" {
		if _, err := supportutil.DeleteIfNeeded(ctx, r.Client, tunedConfigMap); err != nil {
			return fmt.Errorf("failed to delete tunedConfig ConfigMap: %w", err)
		}
	} else {
		if result, err := r.CreateOrUpdate(ctx, r.Client, tunedConfigMap, func() error {
			return reconcileTunedConfigMap(tunedConfigMap, nodePool, tunedConfig)
		}); err != nil {
			return fmt.Errorf("failed to reconcile Tuned ConfigMap: %w", err)
		} else {
			log.Info("Reconciled Tuned ConfigMap", "result", result)
		}
	}

	if performanceProfileConfig == "" {
		// at this point in time, we no longer know the name of the ConfigMap in the HCP NS
		// so, we remove it by listing by a label unique to PerformanceProfile
		if err := deleteConfigByLabel(ctx, r.Client, map[string]string{
			PerformanceProfileConfigMapLabel: "true",
			hyperv1.NodePoolLabel:            nodePool.Name,
		}, controlPlaneNamespace); err != nil {
			return fmt.Errorf("failed to delete performanceprofileConfig ConfigMap: %w", err)
		}
		if err := r.SetPerformanceProfileConditions(ctx, log, nodePool, controlPlaneNamespace, true); err != nil {
			return err
		}
	} else {
		performanceProfileConfigMap := PerformanceProfileConfigMap(controlPlaneNamespace, performanceProfileConfigMapName, nodePool.Name)
		result, err := r.CreateOrUpdate(ctx, r.Client, performanceProfileConfigMap, func() error {
			return reconcilePerformanceProfileConfigMap(performanceProfileConfigMap, nodePool, performanceProfileConfig)
		})
		if err != nil {
			return fmt.Errorf("failed to reconcile PerformanceProfile ConfigMap: %w", err)
		}
		log.Info("Reconciled PerformanceProfile ConfigMap", "result", result)
		if err := r.SetPerformanceProfileConditions(ctx, log, nodePool, controlPlaneNamespace, false); err != nil {
			return err
		}
	}

	return nil
}
