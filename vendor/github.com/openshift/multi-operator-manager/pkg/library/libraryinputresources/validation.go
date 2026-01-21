package libraryinputresources

import "k8s.io/apimachinery/pkg/util/validation/field"

func validateInputResources(obj *InputResources) []error {
	errs := []error{}

	errs = append(errs, validateResourceList(field.NewPath("applyConfigurationResources"), obj.ApplyConfigurationResources)...)
	errs = append(errs, validateOperandResourceList(field.NewPath("operandResources"), obj.OperandResources)...)

	return errs
}

func validateOperandResourceList(path *field.Path, obj OperandResourceList) []error {
	errs := []error{}

	errs = append(errs, validateResourceList(path.Child("configurationResources"), obj.ConfigurationResources)...)
	errs = append(errs, validateResourceList(path.Child("managementResources"), obj.ManagementResources)...)
	errs = append(errs, validateResourceList(path.Child("userWorkloadResources"), obj.UserWorkloadResources)...)

	return errs
}

func validateResourceList(path *field.Path, obj ResourceList) []error {
	errs := []error{}

	for i, curr := range obj.ExactResources {
		errs = append(errs, validateExactResourceID(path.Child("exactResources").Index(i), curr)...)
	}
	for i, curr := range obj.LabelSelectedResources {
		errs = append(errs, validateLabelSelectedResources(path.Child("labelSelectedResources").Index(i), curr)...)
	}
	for i, curr := range obj.ResourceReferences {
		errs = append(errs, validateResourceReference(path.Child("resourceReferences").Index(i), curr)...)
	}

	return errs
}

func validateExactResourceID(path *field.Path, obj ExactResourceID) []error {
	errs := []error{}

	errs = append(errs, validateInputResourceTypeIdentifier(path, obj.InputResourceTypeIdentifier)...)
	if len(obj.Name) == 0 {
		errs = append(errs, field.Required(path.Child("name"), "must be present"))
	}

	return errs
}

func validateLabelSelectedResources(path *field.Path, obj LabelSelectedResource) []error {
	errs := []error{}

	errs = append(errs, validateInputResourceTypeIdentifier(path, obj.InputResourceTypeIdentifier)...)
	if len(obj.LabelSelector.MatchExpressions) > 0 {
		errs = append(errs, field.Forbidden(path.Child("matchExpressions"), "not supported"))
	}
	return errs
}

func validateInputResourceTypeIdentifier(path *field.Path, obj InputResourceTypeIdentifier) []error {
	errs := []error{}

	if len(obj.Version) == 0 {
		errs = append(errs, field.Required(path.Child("version"), "must be present"))
	}
	if len(obj.Resource) == 0 {
		errs = append(errs, field.Required(path.Child("resource"), "must be present"))
	}

	return errs
}

func validateResourceReference(path *field.Path, obj ResourceReference) []error {
	errs := []error{}

	errs = append(errs, validateExactResourceID(path.Child("referringResource"), obj.ReferringResource)...)

	switch obj.Type {
	case ImplicitNamespacedReferenceType:
		errs = append(errs, validateImplicitNamespaceReference(path.Child("implicitNamespacedReference"), obj.ImplicitNamespacedReference)...)
	default:
		errs = append(errs, field.NotSupported(path.Child("type"), obj.Type, []ResourceReferenceType{ImplicitNamespacedReferenceType}))
	}

	return errs
}

func validateImplicitNamespaceReference(path *field.Path, obj *ImplicitNamespacedReference) []error {
	errs := []error{}

	errs = append(errs, validateInputResourceTypeIdentifier(path, obj.InputResourceTypeIdentifier)...)
	if len(obj.Namespace) == 0 {
		errs = append(errs, field.Required(path.Child("namespace"), "must be present"))
	}

	_, err := builder.NewEvaluable(obj.NameJSONPath)
	if err != nil {
		errs = append(errs, field.Invalid(path.Child("nameJSONPath"), obj.NameJSONPath, err.Error()))
	}

	return errs
}
