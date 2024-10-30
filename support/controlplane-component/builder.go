package controlplanecomponent

import (
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ControlPlaneWorkloadBuilder[T client.Object] struct {
	workload *controlPlaneWorkload
}

func NewDeploymentComponent(name string, opts ComponentOptions) *ControlPlaneWorkloadBuilder[*appsv1.Deployment] {
	return &ControlPlaneWorkloadBuilder[*appsv1.Deployment]{
		workload: &controlPlaneWorkload{
			name:             name,
			workloadType:     deploymentWorkloadType,
			ComponentOptions: opts,
		},
	}
}

func NewStatefulSetComponent(name string, opts ComponentOptions) *ControlPlaneWorkloadBuilder[*appsv1.StatefulSet] {
	return &ControlPlaneWorkloadBuilder[*appsv1.StatefulSet]{
		workload: &controlPlaneWorkload{
			name:             name,
			workloadType:     statefulSetWorkloadType,
			ComponentOptions: opts,
		},
	}
}

func (b *ControlPlaneWorkloadBuilder[T]) WithAdaptFunction(adapt func(cpContext ControlPlaneContext, obj T) error) *ControlPlaneWorkloadBuilder[T] {
	b.workload.adapt = func(cpContext ControlPlaneContext, obj client.Object) error {
		return adapt(cpContext, obj.(T))
	}
	return b
}

func (b *ControlPlaneWorkloadBuilder[T]) WithPredicate(predicate func(cpContext ControlPlaneContext) (bool, error)) *ControlPlaneWorkloadBuilder[T] {
	b.workload.predicate = predicate
	return b
}

func (b *ControlPlaneWorkloadBuilder[T]) WithManifestAdapter(manifestName string, opts ...option) *ControlPlaneWorkloadBuilder[T] {
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

func (b *ControlPlaneWorkloadBuilder[T]) WatchResource(resource client.Object, name string) *ControlPlaneWorkloadBuilder[T] {
	resource.SetName(name)
	b.workload.watchedResources = append(b.workload.watchedResources, resource)
	return b
}

func (b *ControlPlaneWorkloadBuilder[T]) WithDependencies(dependencies ...string) *ControlPlaneWorkloadBuilder[T] {
	b.workload.dependencies = append(b.workload.dependencies, dependencies...)
	return b
}

func (b *ControlPlaneWorkloadBuilder[T]) InjectKonnectivityContainer(opts KonnectivityContainerOptions) *ControlPlaneWorkloadBuilder[T] {
	b.workload.konnectivityContainerOpts = &opts
	return b
}

func (b *ControlPlaneWorkloadBuilder[T]) InjectAvailabilityProberContainer(opts util.AvailabilityProberOpts) *ControlPlaneWorkloadBuilder[T] {
	b.workload.availabilityProberOpts = &opts
	return b
}

func (b *ControlPlaneWorkloadBuilder[T]) Build() ControlPlaneComponent {
	b.validate()
	return b.workload
}

func (b *ControlPlaneWorkloadBuilder[T]) validate() {
	if b.workload.name == "" {
		panic("name is required")
	}
}
