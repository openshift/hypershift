# Adding new ControlPlane Component

## Creating a manifests directory

Every component needs to create a directory under `/control-plane-operator/controllers/hostedcontrolplane/v2/assets` to host its manifests, the name of the directory is your component's name
```shell
mkdir /control-plane-operator/controllers/hostedcontrolplane/v2/my-component
```

### Creating Workload manifest

Create a new file named `deployment.yaml` containing the Deployment's manifest of your component

```yaml
cat <<EOF > /control-plane-operator/controllers/hostedcontrolplane/v2/my-component/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-component
spec:
  selector:
    matchLabels:
      app: my-component
  template:
    metadata:
      labels:
        app: my-component
    spec:
      containers:
      - name: my-component
        image: image-key
EOF
```

Note that the container image `image-key` will be replaced with the image value corresponding to that key from the release payload if that key exist, and kept as is otherwise.

!!! note 

    If your workload is a StatefulSet, create a filed named `statefulset.yaml` containing the StatefulSet's manifest instead!


### Creating other resource manifests

Place any other required resource under the same directory, such as configMaps, secrets, roles, etc. All resource manifests will be automatically picked up and deployed.

To create a configMap for example:

```yaml
cat <<EOF > /control-plane-operator/controllers/hostedcontrolplane/v2/my-component/my-config.yaml
apiVersion: v1
data:
  config.yaml: "test-config"
kind: ConfigMap
metadata:
  name: my-config
EOF
```

## Defining your component

Create a new directory for your component's code under `/control-plane-operator/controllers/hostedcontrolplane/v2`
```shell
mkdir /control-plane-operator/controllers/hostedcontrolplane/v2/mycomponent
```

Create a new go file
```shell
touch /control-plane-operator/controllers/hostedcontrolplane/v2/mycomponent/component.go
```

### Implementing the `ComponentOptions` interface

```go
// control-plane-operator/controllers/hostedcontrolplane/v2/mycomponent/component.go

import (
    component "github.com/openshift/hypershift/support/controlplane-component"
)

var _ component.ComponentOptions = &MyComponent{}

type MyComponent struct {
}

// Specify whether this component serves requests outside its node.
func (m *MyComponent) IsRequestServing() bool {
	return false
}

// Specify whether this component's workload(pods) should be spread across availability zones
func (m *MyComponent) MultiZoneSpread() bool {
	return false
}

// Specify whether this component requires access to the kube-apiserver of the cluster where the workload is running
func (m *MyComponent) NeedsManagementKASAccess() bool {
	return false
}
```

### Creating a `ControlPlaneComponent` instance

```go
// control-plane-operator/controllers/hostedcontrolplane/v2/mycomponent/component.go

const (
    // This is the name of your manifests directory.
    ComponentName = "my-component"
)

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &MyComponent{}).
		Build()
}
```

!!! note 

    use `component.NewStatefulSetComponent()` instead if your components's workload is a StatefulSet.


## Registering your component

Finally your component needs to be be registered in `/control-plane-operator/controllers/hostedcontrolplane/hostedcontrolplane_controller.go` inside `registerComponents()` method

```go
// control-plane-operator/controllers/hostedcontrolplane/hostedcontrolplane_controller.go

import (
    mycomponentv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/mycomponent"
)

func (r *HostedControlPlaneReconciler) registerComponents() {
	r.components = append(r.components,
        // other components  
        ...
		mycomponentv2.NewComponent(),
	)
}
```

## Customizing your component

Some components require dynamic additional config, arguments, env variables, etc. based on the `HostedControlPlane` spec. You can adapt your manifests dynamically by defining adapt functions with the following signature `func(ControlPlaneContext, resourceType) error`

To adapt your deployment, first define your adapt function:
```go
// control-plane-operator/controllers/hostedcontrolplane/v2/mycomponent/component.go

import (
    "github.com/openshift/hypershift/support/util"
    component "github.com/openshift/hypershift/support/controlplane-component"
)

func adaptDeployment(cpContext component.ControlPlaneContext, deployment *appsv1.Deployment) error {
    // for example, append a new arg to a container
    util.UpdateContainer("container-name", deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Args = append(c.Args,
			"--platform-type", string(cpContext.HCP.Spec.Platform.Type),
		)
    })
}
```

Then register your adapt function when creating the `ControlPlaneComponent` instance

```go hl_lines="5"
// control-plane-operator/controllers/hostedcontrolplane/v2/mycomponent/component.go

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &MyComponent{}).
        WithAdaptFunction(adaptDeployment).
		Build()
}
```

Similarly to adapt any other manifest, define your adapt function then register it using `.WithManifestAdapter("manifest-name.yaml", component.WithAdaptFunction(func))`:

```go hl_lines="6 7 8 9"
// control-plane-operator/controllers/hostedcontrolplane/v2/mycomponent/component.go

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &MyComponent{}).
        WithAdaptFunction(adaptDeployment).
        WithManifestAdapter(
			"my-config.yaml",
			component.WithAdaptFunction(adaptMyConfig),
		).
		Build()
}

func adaptMyConfig(cpContext component.ControlPlaneContext, config *corev1.ConfigMap) error {
    config.Data["config.yaml"] = "dynamic-content"
}
```

### Predicates

If your component has any prerequisites or depends on external resources to exist, you can define a predicate to block the creation/reconciliation of your component until your conditions are met.

```go
// control-plane-operator/controllers/hostedcontrolplane/v2/mycomponent/component.go

func myPredicate(cpContext component.ControlPlaneContext) (bool, error) {
    // reconcile only if platform is AWS
    if cpContext.HCP.Spec.Platform.Type == "AWS" {
        return true
    }
    return false
}
```

Then register your predicate function when creating the `ControlPlaneComponent` instance

```go hl_lines="5"
// control-plane-operator/controllers/hostedcontrolplane/v2/mycomponent/component.go

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &MyComponent{}).
        WithPredicate(myPredicate).
		Build()
}
```

Similarly to define predicate for any specific manifest, define your predicate function then register it using `.WithManifestAdapter("manifest-name.yaml", component.WithPredicate(func))`:

```go hl_lines="5 6 7 8 9"
// control-plane-operator/controllers/hostedcontrolplane/v2/mycomponent/component.go

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &MyComponent{}).
        WithManifestAdapter(
			"my-config.yaml",
            component.WithAdaptFunction(adaptMyConfig),
			component.WithPredicate(myConfigPredicate),
		).
		Build()
}

func myConfigPredicate(cpContext ControlPlaneContext) bool {
    if _, exists := cpContext.HCP.Annotations["disable_my_config"]; exists {
        return false
    }
    return true
}
```

### Watching ConfigMap/Secrets

If your workload(Deployment/StatefulSet) requires a rollout if a configMap or Secret data is changed, you can configure your component to watch those resources as follows:

```go hl_lines="5 6"
// control-plane-operator/controllers/hostedcontrolplane/v2/mycomponent/component.go

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &MyComponent{}).
        WatchResource(&corev1.ConfigMap{}, "my-config").
        WatchResource(&corev1.Secret{}, "my-secret").
		Build()
}
```
This will trigger a rollout of your Deployment/StatefulSet on any change to `my-config` configMap or `my-secret` Secret.

### Dependencies

If your component depends on other components being `Available` before it can start reconciliation, you can define your Dependencies as a list of components' names as follows:

```go hl_lines="5"
// control-plane-operator/controllers/hostedcontrolplane/v2/mycomponent/component.go

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &MyComponent{}).
        WithDependencies("component1", "component2").
		Build()
}
```

See [controlPlaneWorkloadBuilder](/support/controlplane-component/builder.go) for all available options
