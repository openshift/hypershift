package controlplanecomponent

import (
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type controlPlaneWorkloadBuilder[T client.Object] struct {
	workload *controlPlaneWorkload[T]
}

func NewDeploymentComponent(name string, opts ComponentOptions) *controlPlaneWorkloadBuilder[*appsv1.Deployment] {
	return &controlPlaneWorkloadBuilder[*appsv1.Deployment]{
		workload: &controlPlaneWorkload[*appsv1.Deployment]{
			name:             name,
			workloadProvider: &deploymentProvider{},
			ComponentOptions: opts,
		},
	}
}

func NewStatefulSetComponent(name string, opts ComponentOptions) *controlPlaneWorkloadBuilder[*appsv1.StatefulSet] {
	return &controlPlaneWorkloadBuilder[*appsv1.StatefulSet]{
		workload: &controlPlaneWorkload[*appsv1.StatefulSet]{
			name:             name,
			workloadProvider: &statefulSetProvider{},
			ComponentOptions: opts,
		},
	}
}

func NewCronJobComponent(name string, opts ComponentOptions) *controlPlaneWorkloadBuilder[*batchv1.CronJob] {
	return &controlPlaneWorkloadBuilder[*batchv1.CronJob]{
		workload: &controlPlaneWorkload[*batchv1.CronJob]{
			name:             name,
			workloadProvider: &cronJobProvider{},
			ComponentOptions: opts,
		},
	}
}

func NewJobComponent(name string, opts ComponentOptions) *controlPlaneWorkloadBuilder[*batchv1.Job] {
	return &controlPlaneWorkloadBuilder[*batchv1.Job]{
		workload: &controlPlaneWorkload[*batchv1.Job]{
			name:             name,
			workloadProvider: &jobProvider{},
			ComponentOptions: opts,
		},
	}
}

func (b *controlPlaneWorkloadBuilder[T]) WithAdaptFunction(adapt func(cpContext WorkloadContext, obj T) error) *controlPlaneWorkloadBuilder[T] {
	b.workload.adapt = adapt
	return b
}

func (b *controlPlaneWorkloadBuilder[T]) WithPredicate(predicate func(cpContext WorkloadContext) (bool, error)) *controlPlaneWorkloadBuilder[T] {
	b.workload.predicate = predicate
	return b
}

func (b *controlPlaneWorkloadBuilder[T]) WithManifestAdapter(manifestName string, opts ...option) *controlPlaneWorkloadBuilder[T] {
	adapter := &genericAdapter{}
	for _, opt := range opts {
		opt(adapter)
	}

	if b.workload.manifestsAdapters == nil {
		b.workload.manifestsAdapters = make(map[string]genericAdapter)
	}
	b.workload.manifestsAdapters[manifestName] = *adapter
	return b
}

func (b *controlPlaneWorkloadBuilder[T]) WithDependencies(dependencies ...string) *controlPlaneWorkloadBuilder[T] {
	b.workload.dependencies = append(b.workload.dependencies, dependencies...)
	return b
}

func (b *controlPlaneWorkloadBuilder[T]) InjectKonnectivityContainer(opts KonnectivityContainerOptions) *controlPlaneWorkloadBuilder[T] {
	b.workload.konnectivityContainerOpts = &opts
	return b
}

func (b *controlPlaneWorkloadBuilder[T]) InjectAvailabilityProberContainer(opts util.AvailabilityProberOpts) *controlPlaneWorkloadBuilder[T] {
	b.workload.availabilityProberOpts = &opts
	return b
}

// InjectTokenMinterContainer will injecta sidecar container which mints ServiceAccount tokens in the tenant cluster for the given named service account,
// and then make it available for the main container with a volume mount.
func (b *controlPlaneWorkloadBuilder[T]) InjectTokenMinterContainer(opts TokenMinterContainerOptions) *controlPlaneWorkloadBuilder[T] {
	b.workload.tokenMinterContainerOpts = &opts
	return b
}

// InjectServiceAccountKubeConfig will cause the generation of a secret with a kubeconfig using certificates for the given named service account
// and the volume mounts for that secret within the given mountPath.
func (b *controlPlaneWorkloadBuilder[T]) InjectServiceAccountKubeConfig(opts ServiceAccountKubeConfigOpts) *controlPlaneWorkloadBuilder[T] {
	b.workload.serviceAccountKubeConfigOpts = &opts
	return b
}

// WithCustomOperandsRolloutCheckFunc allows to set a custom function to check the rollout status of operands.
// This function should return true if the operands are ready, false otherwise.
// TODO: This is a temporary solution, should be replaced by MonitorOperandsRolloutStatus() once we enforce a common label/annotation on all operands to provide more generic rollout check.
// https://issues.redhat.com/browse/CORENET-6230
// https://issues.redhat.com/browse/STOR-2523
func (b *controlPlaneWorkloadBuilder[T]) WithCustomOperandsRolloutCheckFunc(fn func(cpContext WorkloadContext) (bool, error)) *controlPlaneWorkloadBuilder[T] {
	b.workload.customOperandsRolloutCheck = fn
	return b
}

// MonitorOperandsRolloutStatus will enable monitoring of the rollout status of all operands(deployments) with the label "hypershift.openshift.io/managed-by: <component-name>".
// Operands must also have the annotation "release.openshift.io/version" with their release image version.
// The component will be marked as ready only when all operands are ready.
func (b *controlPlaneWorkloadBuilder[T]) MonitorOperandsRolloutStatus() *controlPlaneWorkloadBuilder[T] {
	b.workload.monitorOperandsRolloutStatus = true
	return b
}

type ServiceAccountKubeConfigOpts struct {
	Name, Namespace, MountPath, ContainerName string
}

func (b *controlPlaneWorkloadBuilder[T]) Build() ControlPlaneComponent {
	b.validate()
	return b.workload
}

func (b *controlPlaneWorkloadBuilder[T]) validate() {
	if b.workload.name == "" {
		panic("name is required")
	}
}
