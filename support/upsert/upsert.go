package upsert

import (
	"context"
	"fmt"

	routev1 "github.com/openshift/api/route/v1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"

	configv1 "github.com/openshift/api/config/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	capiibmv1 "sigs.k8s.io/cluster-api-provider-ibmcloud/api/v1beta2"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type CreateOrUpdateFN = func(ctx context.Context, c crclient.Client, obj crclient.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error)

type CreateOrUpdateProvider interface {
	CreateOrUpdate(ctx context.Context, c crclient.Client, obj crclient.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error)
}

type CreateOrUpdateProviderV2 interface {
	CreateOrUpdateV2(ctx context.Context, c crclient.Client, obj crclient.Object) (controllerutil.OperationResult, error)
}

var withStatusSubresource = sets.NewString(
	fmt.Sprintf("%T", &capiaws.AWSCluster{}),
	fmt.Sprintf("%T", &configv1.ClusterOperator{}),
	fmt.Sprintf("%T", &capikubevirt.KubevirtCluster{}),
	fmt.Sprintf("%T", &capiv1.Cluster{}),
	fmt.Sprintf("%T", &capiazure.AzureCluster{}),
	fmt.Sprintf("%T", &capiibmv1.IBMPowerVSCluster{}),
)

func hasStatusSubResource(o crclient.Object) bool {
	return withStatusSubresource.Has(fmt.Sprintf("%T", o))
}

func New(enableUpdateLoopDetector bool) CreateOrUpdateProvider {
	p := &createOrUpdateProvider{}
	if enableUpdateLoopDetector {
		p.loopDetector = newUpdateLoopDetector()
	}
	return p
}

func NewV2(enableUpdateLoopDetector bool) CreateOrUpdateProviderV2 {
	p := &createOrUpdateProvider{}
	if enableUpdateLoopDetector {
		p.loopDetector = newUpdateLoopDetector()
	}
	return p
}

type createOrUpdateProvider struct {
	loopDetector *updateLoopDetector
}

// CreateOrUpdate is a copy of controllerutil.CreateOrUpdate with
// an important difference: It copies a number of fields from the object
// on the server to the mutated object if unset in the latter. This
// avoids unnecessary updates when our code sets a whole struct that
// has fields that get defaulted by the server.
func (p *createOrUpdateProvider) CreateOrUpdate(ctx context.Context, c crclient.Client, obj crclient.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	key := crclient.ObjectKeyFromObject(obj)
	if err := c.Get(ctx, key, obj); err != nil {
		if !apierrors.IsNotFound(err) {
			return controllerutil.OperationResultNone, err
		}
		if err := mutate(f, key, obj); err != nil {
			return controllerutil.OperationResultNone, err
		}
		if err := c.Create(ctx, obj); err != nil {
			return controllerutil.OperationResultNone, err
		}
		if hasStatusSubResource(obj) {
			if err := mutate(f, key, obj); err != nil {
				return controllerutil.OperationResultNone, err
			}
			if err := c.Status().Update(ctx, obj); err != nil {
				return controllerutil.OperationResultNone, err
			}
		}
		return controllerutil.OperationResultCreated, nil
	}

	existing := obj.DeepCopyObject() //nolint
	if err := mutate(f, key, obj); err != nil {
		return controllerutil.OperationResultNone, err
	}

	result, err := p.update(ctx, c, obj, existing)
	if err != nil || result == controllerutil.OperationResultNone {
		return result, err
	}

	// update statue
	if hasStatusSubResource(obj) {
		if err := mutate(f, key, obj); err != nil {
			return controllerutil.OperationResultNone, err
		}
		if err := c.Status().Update(ctx, obj); err != nil {
			return controllerutil.OperationResultNone, err
		}
	}

	return controllerutil.OperationResultUpdated, nil
}

func (p *createOrUpdateProvider) update(ctx context.Context, c crclient.Client, obj crclient.Object, existing runtime.Object) (controllerutil.OperationResult, error) {
	key := crclient.ObjectKeyFromObject(obj)

	switch existingTyped := existing.(type) {
	case *appsv1.Deployment:
		defaultDeploymentSpec(&existingTyped.Spec, &obj.(*appsv1.Deployment).Spec)
	case *appsv1.DaemonSet:
		defaultDaemonSetSpec(&existingTyped.Spec, &obj.(*appsv1.DaemonSet).Spec)
	case *appsv1.StatefulSet:
		defaultStatefulSetSpec(&existingTyped.Spec, &obj.(*appsv1.StatefulSet).Spec)
	case *batchv1.CronJob:
		defaultCronJobSpec(&existingTyped.Spec, &obj.(*batchv1.CronJob).Spec)
	case *corev1.Service:
		defaultServiceSpec(&existingTyped.Spec, &obj.(*corev1.Service).Spec)
	case *corev1.ServiceAccount:
		defaultServiceAccount(existingTyped, obj.(*corev1.ServiceAccount))
	case *routev1.Route:
		defaultRouteSpec(&existingTyped.Spec, &obj.(*routev1.Route).Spec)
	case *apiextensionsv1.CustomResourceDefinition:
		defaultCRDSpec(&existingTyped.Spec, &obj.(*apiextensionsv1.CustomResourceDefinition).Spec)
	case *admissionregistrationv1.MutatingWebhookConfiguration:
		defaultMutatingWebhookConfiguration(&existingTyped.Webhooks, &obj.(*admissionregistrationv1.MutatingWebhookConfiguration).Webhooks)
	}

	if equality.Semantic.DeepEqual(existing, obj) {
		if p.loopDetector != nil {
			p.loopDetector.recordNoOpUpdate(obj, key)
		}
		return controllerutil.OperationResultNone, nil
	}
	if p.loopDetector != nil {
		p.loopDetector.recordActualUpdate(existing, obj, key)
	}

	if err := c.Update(ctx, obj); err != nil {
		return controllerutil.OperationResultNone, err
	}

	return controllerutil.OperationResultUpdated, nil
}

// CreateOrUpdateV2 is a copy of CreateOrUpdate with
// an important difference: It doesn't take a mutate function, and it doesn't override the passed in object
// if it alreay exist on the API server.
// This is needed for objects loaded from a yaml manifest, so the loaded manifest is not overriden.
func (p *createOrUpdateProvider) CreateOrUpdateV2(ctx context.Context, c crclient.Client, obj crclient.Object) (controllerutil.OperationResult, error) {
	key := crclient.ObjectKeyFromObject(obj)
	existing := obj.DeepCopyObject().(crclient.Object) //nolint

	if err := c.Get(ctx, key, existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return controllerutil.OperationResultNone, err
		}
		if err := c.Create(ctx, obj); err != nil {
			return controllerutil.OperationResultNone, err
		}
		if hasStatusSubResource(obj) {
			if err := c.Status().Update(ctx, obj); err != nil {
				return controllerutil.OperationResultNone, err
			}
		}
		return controllerutil.OperationResultCreated, nil
	}

	preserveOriginalMetadata(existing, obj)
	result, err := p.update(ctx, c, obj, existing)
	if err != nil || result == controllerutil.OperationResultNone {
		return result, err
	}

	// TODO: check if managed fields is preserved automatically.

	if hasStatusSubResource(obj) {
		if err := c.Status().Update(ctx, obj); err != nil {
			return controllerutil.OperationResultNone, err
		}
	}
	return controllerutil.OperationResultUpdated, nil
}

// mutate wraps a MutateFn and applies validation to its result.
func mutate(f controllerutil.MutateFn, key crclient.ObjectKey, obj crclient.Object) error {
	if err := f(); err != nil {
		return err
	}
	if newKey := crclient.ObjectKeyFromObject(obj); key != newKey {
		return fmt.Errorf("MutateFn cannot mutate object name and/or object namespace. key: %s, kind: %s, namespace: %s", key, obj.GetObjectKind(), obj.GetNamespace())
	}
	return nil
}

func preserveOriginalMetadata(original, mutated crclient.Object) {
	labels := original.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	for key, value := range mutated.GetLabels() {
		labels[key] = value
	}
	mutated.SetLabels(labels)

	annotations := original.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	for key, value := range mutated.GetAnnotations() {
		annotations[key] = value
	}
	mutated.SetAnnotations(annotations)

	finalizers := sets.New(original.GetFinalizers()...).Insert(mutated.GetFinalizers()...)
	mutated.SetFinalizers(sets.List(finalizers))

	mutated.SetResourceVersion(original.GetResourceVersion())
}

// Below defaulting funcs. Their code is based on upstream code that is unfortunatelly
// not in staging so we can't import it:
// * https://github.com/kubernetes/kubernetes/blob/e5976909c6fb129228a67515e0f86336a53884f0/pkg/apis/core/v1/zz_generated.defaults.go
// * https://github.com/kubernetes/kubernetes/blob/e5976909c6fb129228a67515e0f86336a53884f0/pkg/apis/apps/v1/zz_generated.defaults.go
// * https://github.com/openshift/openshift-apiserver/blob/c3b45895167907f149184fb170e4cae1bf28576f/pkg/route/apis/route/v1/defaults.go
// * https://github.com/kubernetes/kubernetes/blob/e5976909c6fb129228a67515e0f86336a53884f0/pkg/apis/batch/v1/zz_generated.defaults.go

func defaultRouteSpec(original, mutated *routev1.RouteSpec) {
	if mutated.To.Weight == nil {
		mutated.To.Weight = original.To.Weight
	}
	if mutated.WildcardPolicy == "" {
		mutated.WildcardPolicy = original.WildcardPolicy
	}
}

func defaultServiceSpec(original, mutated *corev1.ServiceSpec) {
	if mutated.ClusterIP == "" {
		mutated.ClusterIP = original.ClusterIP
	}
	if mutated.ClusterIPs == nil {
		mutated.ClusterIPs = original.ClusterIPs
	}
	if mutated.IPFamilies == nil {
		mutated.IPFamilies = original.IPFamilies
	}
	if mutated.IPFamilyPolicy == nil {
		mutated.IPFamilyPolicy = original.IPFamilyPolicy
	}
	for i := range original.Ports {
		if i >= len(mutated.Ports) {
			break
		}
		if mutated.Ports[i].Protocol == "" {
			mutated.Ports[i].Protocol = original.Ports[i].Protocol
		}
		if mutated.Ports[i].TargetPort.String() == "0" {
			mutated.Ports[i].TargetPort = intstr.FromInt(int(mutated.Ports[i].Port))
		}
	}
	if mutated.SessionAffinity == "" {
		mutated.SessionAffinity = original.SessionAffinity
	}
	if mutated.Type == "" {
		mutated.Type = original.Type
	}
}

func defaultServiceAccount(original, mutated *corev1.ServiceAccount) {
	// keep original pull secrets, as those will be injected after the serviceAccount is created.
	// this is neccessary to avoid infinite update loop.
	mutated.ImagePullSecrets = original.ImagePullSecrets
	mutated.Secrets = original.Secrets

	util.EnsurePullSecret(mutated, common.PullSecret("").Name)
}

func defaultCronJobSpec(original, mutated *batchv1.CronJobSpec) {
	if mutated.ConcurrencyPolicy == "" {
		mutated.ConcurrencyPolicy = original.ConcurrencyPolicy
	}
	if mutated.FailedJobsHistoryLimit == nil {
		mutated.FailedJobsHistoryLimit = original.FailedJobsHistoryLimit
	}
	if mutated.JobTemplate.Spec.ActiveDeadlineSeconds == nil {
		mutated.JobTemplate.Spec.ActiveDeadlineSeconds = original.JobTemplate.Spec.ActiveDeadlineSeconds
	}
	if mutated.JobTemplate.Spec.BackoffLimit == nil {
		mutated.JobTemplate.Spec.BackoffLimit = original.JobTemplate.Spec.BackoffLimit
	}
	if mutated.StartingDeadlineSeconds == nil {
		mutated.StartingDeadlineSeconds = original.StartingDeadlineSeconds
	}
	if mutated.SuccessfulJobsHistoryLimit == nil {
		mutated.SuccessfulJobsHistoryLimit = original.SuccessfulJobsHistoryLimit
	}
	if mutated.Suspend == nil {
		mutated.Suspend = original.Suspend
	}

	defaultPodSpec(&original.JobTemplate.Spec.Template.Spec, &mutated.JobTemplate.Spec.Template.Spec)
}

func defaultDeploymentSpec(original, mutated *appsv1.DeploymentSpec) {
	if mutated.ProgressDeadlineSeconds == nil {
		mutated.ProgressDeadlineSeconds = original.ProgressDeadlineSeconds
	}
	if mutated.Replicas == nil {
		mutated.Replicas = original.Replicas
	}
	if mutated.Strategy.Type == "" {
		mutated.Strategy.Type = original.Strategy.Type
	}
	if mutated.Strategy.RollingUpdate == nil && original.Strategy.RollingUpdate != nil {
		mutated.Strategy.RollingUpdate = original.Strategy.RollingUpdate
	}
	if mutated.Strategy.RollingUpdate != nil && original.Strategy.RollingUpdate != nil {
		if mutated.Strategy.RollingUpdate.MaxSurge == nil {
			mutated.Strategy.RollingUpdate.MaxSurge = original.Strategy.RollingUpdate.MaxSurge
		}
		if mutated.Strategy.RollingUpdate.MaxUnavailable == nil {
			mutated.Strategy.RollingUpdate.MaxUnavailable = original.Strategy.RollingUpdate.MaxUnavailable
		}
	}
	if mutated.RevisionHistoryLimit == nil {
		mutated.RevisionHistoryLimit = original.RevisionHistoryLimit
	}

	defaultPodSpec(&original.Template.Spec, &mutated.Template.Spec)
}

func defaultDaemonSetSpec(original, mutated *appsv1.DaemonSetSpec) {
	if mutated.RevisionHistoryLimit == nil {
		mutated.RevisionHistoryLimit = original.RevisionHistoryLimit
	}
	if mutated.UpdateStrategy.Type == "" {
		mutated.UpdateStrategy.Type = original.UpdateStrategy.Type
	}
	if mutated.UpdateStrategy.RollingUpdate == nil && original.UpdateStrategy.RollingUpdate != nil {
		mutated.UpdateStrategy.RollingUpdate = original.UpdateStrategy.RollingUpdate
	}
	defaultPodSpec(&original.Template.Spec, &mutated.Template.Spec)
}

func defaultStatefulSetSpec(original, mutated *appsv1.StatefulSetSpec) {
	if mutated.Replicas == nil {
		mutated.Replicas = original.Replicas
	}
	if mutated.PodManagementPolicy == "" {
		mutated.PodManagementPolicy = original.PodManagementPolicy
	}
	if mutated.UpdateStrategy.RollingUpdate == nil && original.UpdateStrategy.RollingUpdate != nil {
		mutated.UpdateStrategy.RollingUpdate = original.UpdateStrategy.RollingUpdate
	}
	if mutated.UpdateStrategy.RollingUpdate != nil && original.UpdateStrategy.RollingUpdate != nil {
		if mutated.UpdateStrategy.RollingUpdate.Partition == nil {
			mutated.UpdateStrategy.RollingUpdate.Partition = original.UpdateStrategy.RollingUpdate.Partition
		}
	}
	if mutated.RevisionHistoryLimit == nil {
		mutated.RevisionHistoryLimit = original.RevisionHistoryLimit
	}

	defaultPodSpec(&original.Template.Spec, &mutated.Template.Spec)

	for i := range original.VolumeClaimTemplates {
		if i >= len(mutated.VolumeClaimTemplates) {
			break
		}
		defaultVolumeClaim(&original.VolumeClaimTemplates[i].Spec, &mutated.VolumeClaimTemplates[i].Spec)
		// k8s seems to update status within the volume claim template embedded in
		// the spec of the statefulset...
		mutated.VolumeClaimTemplates[i].Status = original.VolumeClaimTemplates[i].Status
	}
}

func defaultPodSpec(original, mutated *corev1.PodSpec) {
	for i := range original.InitContainers {
		if i >= len(mutated.InitContainers) {
			break
		}
		defaultContainer(&original.InitContainers[i], &mutated.InitContainers[i])
	}
	for i := range original.Containers {
		if i >= len(mutated.Containers) {
			break
		}
		defaultContainer(&original.Containers[i], &mutated.Containers[i])
	}
	if mutated.DNSPolicy == "" {
		mutated.DNSPolicy = original.DNSPolicy
	}
	if mutated.ServiceAccountName == "" {
		mutated.ServiceAccountName = original.ServiceAccountName
	}
	if mutated.DeprecatedServiceAccount == "" {
		mutated.DeprecatedServiceAccount = original.DeprecatedServiceAccount
	}
	if mutated.RestartPolicy == "" {
		mutated.RestartPolicy = original.RestartPolicy
	}
	if mutated.SchedulerName == "" {
		mutated.SchedulerName = original.SchedulerName
	}
	if mutated.TerminationGracePeriodSeconds == nil {
		mutated.TerminationGracePeriodSeconds = original.TerminationGracePeriodSeconds
	}

	for i := range original.Volumes {
		if i >= len(mutated.Volumes) {
			break
		}
		defaultVolume(&original.Volumes[i], &mutated.Volumes[i])
	}

	if mutated.SecurityContext == nil {
		mutated.SecurityContext = original.SecurityContext
	}
}

func defaultContainer(original, mutated *corev1.Container) {
	if mutated.ImagePullPolicy == "" {
		mutated.ImagePullPolicy = original.ImagePullPolicy
	}
	if mutated.TerminationMessagePath == "" {
		mutated.TerminationMessagePath = original.TerminationMessagePath
	}
	if mutated.TerminationMessagePolicy == "" {
		mutated.TerminationMessagePolicy = original.TerminationMessagePolicy
	}

	if original.LivenessProbe != nil && mutated.LivenessProbe != nil {
		defaultProbe(original.LivenessProbe, mutated.LivenessProbe)
	}

	if original.ReadinessProbe != nil && mutated.ReadinessProbe != nil {
		defaultProbe(original.ReadinessProbe, mutated.ReadinessProbe)
	}

	for i := range original.Env {
		if i >= len(mutated.Env) {
			break
		}
		defaultEnv(&original.Env[i], &mutated.Env[i])
	}

	for i := range original.Ports {
		if i >= len(mutated.Ports) {
			break
		}
		defaultContainerPort(&original.Ports[i], &mutated.Ports[i])
	}

	if original.SecurityContext != nil && mutated.SecurityContext == nil {
		mutated.SecurityContext = original.SecurityContext
	}
	if original.SecurityContext != nil && mutated.SecurityContext != nil {
		if mutated.SecurityContext.RunAsUser == nil && original.SecurityContext.RunAsUser != nil {
			mutated.SecurityContext.RunAsUser = original.SecurityContext.RunAsUser
		}
	}
}

func defaultProbe(original, mutated *corev1.Probe) {
	if mutated.TimeoutSeconds == 0 {
		mutated.TimeoutSeconds = original.TimeoutSeconds
	}
	if mutated.PeriodSeconds == 0 {
		mutated.PeriodSeconds = original.PeriodSeconds
	}
	if mutated.SuccessThreshold == 0 {
		mutated.SuccessThreshold = original.SuccessThreshold
	}
	if mutated.FailureThreshold == 0 {
		mutated.FailureThreshold = original.FailureThreshold
	}
	if mutated.HTTPGet != nil && original.HTTPGet != nil && mutated.HTTPGet.Scheme == "" {
		mutated.HTTPGet.Scheme = original.HTTPGet.Scheme
	}
}

func defaultVolume(original, mutated *corev1.Volume) {
	if mutated.VolumeSource.Secret != nil && original.VolumeSource.Secret != nil && mutated.VolumeSource.Secret.DefaultMode == nil {
		mutated.VolumeSource.Secret.DefaultMode = original.VolumeSource.Secret.DefaultMode
	}
	if mutated.VolumeSource.ConfigMap != nil && original.VolumeSource.ConfigMap != nil && mutated.VolumeSource.ConfigMap.DefaultMode == nil {
		mutated.VolumeSource.ConfigMap.DefaultMode = original.VolumeSource.ConfigMap.DefaultMode
	}
}

func defaultVolumeClaim(original, mutated *corev1.PersistentVolumeClaimSpec) {
	if original.VolumeMode != nil && mutated.VolumeMode == nil {
		mutated.VolumeMode = original.VolumeMode
	}
	if original.StorageClassName != nil && mutated.StorageClassName == nil {
		mutated.StorageClassName = original.StorageClassName
	}
}

func defaultEnv(original, mutated *corev1.EnvVar) {
	if mutated.ValueFrom != nil && original.ValueFrom != nil && mutated.ValueFrom.FieldRef != nil && original.ValueFrom.FieldRef != nil && mutated.ValueFrom.FieldRef.APIVersion == "" {
		mutated.ValueFrom.FieldRef.APIVersion = original.ValueFrom.FieldRef.APIVersion
	}
}

func defaultContainerPort(original, mutated *corev1.ContainerPort) {
	if mutated.Protocol == "" {
		mutated.Protocol = original.Protocol
	}
}

func defaultCRDSpec(original, mutated *apiextensionsv1.CustomResourceDefinitionSpec) {
	if mutated.Conversion == nil {
		mutated.Conversion = &apiextensionsv1.CustomResourceConversion{
			Strategy: apiextensionsv1.NoneConverter,
		}
	}
}

func defaultMutatingWebhookConfiguration(original, mutated *[]admissionregistrationv1.MutatingWebhook) {
	if len(*original) != len(*mutated) {
		return
	}

	for idx, webhook := range *mutated {
		if len(webhook.Rules) == len((*original)[idx].Rules) {
			for jdx, webhookRule := range webhook.Rules {
				if webhookRule.Scope == nil {
					(*mutated)[idx].Rules[jdx].Scope = (*original)[idx].Rules[jdx].Scope
				}
			}

			if webhook.FailurePolicy == nil {
				(*mutated)[idx].FailurePolicy = (*original)[idx].FailurePolicy
			}
			if webhook.MatchPolicy == nil {
				(*mutated)[idx].MatchPolicy = (*original)[idx].MatchPolicy
			}
			if webhook.NamespaceSelector == nil {
				(*mutated)[idx].NamespaceSelector = (*original)[idx].NamespaceSelector
			}
			if webhook.ObjectSelector == nil {
				(*mutated)[idx].ObjectSelector = (*original)[idx].ObjectSelector
			}
			if webhook.TimeoutSeconds == nil {
				(*mutated)[idx].TimeoutSeconds = (*original)[idx].TimeoutSeconds
			}
			if webhook.ReinvocationPolicy == nil {
				(*mutated)[idx].ReinvocationPolicy = (*original)[idx].ReinvocationPolicy
			}
		}
	}

}
