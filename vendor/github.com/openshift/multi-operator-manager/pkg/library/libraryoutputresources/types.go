package libraryoutputresources

// OutputResources is a list of resources that an operator will need to mutate from apply-configuration and apply-configuration-live.
// This needs to be a complete list.  Any resource not present in this list will not be mutable for this operator.
type OutputResources struct {
	// configurationResources are targeted at the cluster where configuration is held.
	// On standalone, this is the one cluster.
	// On HCP, this is logically a view into the resources in the namespace of the guest cluster.
	ConfigurationResources ResourceList `json:"configurationResources,omitempty"`
	// managementResources are targeted at the cluster where management plane responsibility is held.
	// On standalone, this is the one cluster.
	// On HCP, this is logically resources in the namespace of the guest cluster: usually the control plane aspects.
	ManagementResources ResourceList `json:"managementResources,omitempty"`
	// UserWorkloadResources are targeted at the cluster where user workloads run.
	// On standalone, this is the one cluster.
	// On HCP, this is the guest cluster.
	UserWorkloadResources ResourceList `json:"userWorkloadResources,omitempty"`
}

type ResourceList struct {
	// exactResources are lists of exact names that are mutated
	ExactResources []ExactResourceID `json:"exactResources,omitempty"`

	// generatedNameResources are lists of generatedNames that are mutated.
	// These are also honored on non-creates, via prefix matching, but *only* on resource with generatedNames.
	// This is not a cheat code for prefix matching.
	GeneratedNameResources []GeneratedResourceID `json:"generatedNameResources,omitempty"`

	// eventingNamespaces holds a list of namespaces that the operator can output event into.
	// This allows redirection of events to a particular cluster on a per-namespace level.
	// For instance, the openshift-authentication-operator  can go to management, but openshift-authentication can go
	// to the userWorkload cluster.
	EventingNamespaces []string `json:"eventingNamespaces,omitempty"`

	// TODO I bet this covers 95% of what we need, but maybe we need label selector.
	// I'm a solid -1 on "pattern" based selection. We select in kube based on label selectors.
}

type ExactResourceID struct {
	OutputResourceTypeIdentifier `json:",inline"`

	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

type GeneratedResourceID struct {
	OutputResourceTypeIdentifier `json:",inline"`

	Namespace     string `json:"namespace,omitempty"`
	GeneratedName string `json:"name"`
}

// OutputResourceTypeIdentifier does *not* include version, because the serialization doesn't matter for production.
// We'll be able to read the file and see how it is serialized.
type OutputResourceTypeIdentifier struct {
	Group    string `json:"group"`
	Version  string `json:"version"`
	Resource string `json:"resource"`
}
