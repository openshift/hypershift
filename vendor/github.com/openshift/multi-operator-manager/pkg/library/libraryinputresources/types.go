package libraryinputresources

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InputResources contains the items that an operator needs to make a decision about what needs to be create,
// modified, or removed.
type InputResources struct {
	// applyConfigurationResources are the list of resources used as input to the apply-configuration command.
	// It is the responsibility of the MOM to determine where the inputs come from.
	ApplyConfigurationResources ResourceList `json:"applyConfigurationResources,omitempty"`

	// operandResources is the list of resources that are important for determining check-health
	OperandResources OperandResourceList `json:"operandResources,omitempty"`
}

type ResourceList struct {
	ExactResources []ExactResourceID `json:"exactResources,omitempty"`

	GeneratedNameResources []GeneratedResourceID `json:"generatedNameResources,omitempty"`

	LabelSelectedResources []LabelSelectedResource `json:"labelSelectedResources,omitempty"`

	// use resourceReferences when one resource (apiserver.config.openshift.io/cluster) refers to another resource
	// like a secret (.spec.servingCerts.namedCertificates[*].servingCertificates.name).
	ResourceReferences []ResourceReference `json:"resourceReferences,omitempty"`
}

type LabelSelectedResource struct {
	InputResourceTypeIdentifier `json:",inline"`

	Namespace string `json:"namespace,omitempty"`

	// validation prevents setting matchExpressions
	LabelSelector metav1.LabelSelector `json:"labelSelector"`
}

type OperandResourceList struct {
	ConfigurationResources ResourceList `json:"configurationResources,omitempty"`
	ManagementResources    ResourceList `json:"managementResources,omitempty"`
	UserWorkloadResources  ResourceList `json:"userWorkloadResources,omitempty"`
}

type ExactResourceID struct {
	InputResourceTypeIdentifier `json:",inline"`

	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

type GeneratedResourceID struct {
	InputResourceTypeIdentifier `json:",inline"`

	Namespace     string `json:"namespace,omitempty"`
	GeneratedName string `json:"name"`
}

type ResourceReference struct {
	// TODO determine if we need the ability to select multiple containing resources.  I don’t think we’ll need to given the shape of our configuration.
	ReferringResource ExactResourceID `json:"referringResource"`

	Type ResourceReferenceType `json:"type"`

	ExplicitNamespacedReference *ExplicitNamespacedReference `json:"explicitNamespacedReference,omitempty"`
	ImplicitNamespacedReference *ImplicitNamespacedReference `json:"implicitNamespacedReference,omitempty"`
	ClusterScopedReference      *ClusterScopedReference      `json:"clusterScopedReference,omitempty"`
}

type ResourceReferenceType string

const (
	ExplicitNamespacedReferenceType ResourceReferenceType = "ExplicitNamespacedReference"
	ImplicitNamespacedReferenceType ResourceReferenceType = "ImplicitNamespacedReference"
	ClusterScopedReferenceType      ResourceReferenceType = "ClusterScopedReference"
)

type ExplicitNamespacedReference struct {
	InputResourceTypeIdentifier `json:",inline"`

	// may have multiple matches
	// TODO CEL may be more appropriate
	NamespaceJSONPath string `json:"namespaceJSONPath"`
	NameJSONPath      string `json:"nameJSONPath"`
}

type ImplicitNamespacedReference struct {
	InputResourceTypeIdentifier `json:",inline"`

	Namespace string `json:"namespace"`
	// may have multiple matches
	// TODO CEL may be more appropriate
	NameJSONPath string `json:"nameJSONPath"`
}

type ClusterScopedReference struct {
	InputResourceTypeIdentifier `json:",inline"`

	// may have multiple matches
	// TODO CEL may be more appropriate
	NameJSONPath string `json:"nameJSONPath"`
}

type InputResourceTypeIdentifier struct {
	Group string `json:"group"`
	// version is very important because it must match the version of serialization that your operator expects.
	// All Group,Resource tuples must use the same Version.
	Version  string `json:"version"`
	Resource string `json:"resource"`
}
