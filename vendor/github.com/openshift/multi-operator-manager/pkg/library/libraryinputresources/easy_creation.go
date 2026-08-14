package libraryinputresources

func ExactResource(group, version, resource, namespace, name string) ExactResourceID {
	return ExactResourceID{
		InputResourceTypeIdentifier: InputResourceTypeIdentifier{
			Group:    group,
			Version:  version,
			Resource: resource,
		},
		Namespace: namespace,
		Name:      name,
	}
}

func ExactSecret(namespace, name string) ExactResourceID {
	return ExactResource("", "v1", "secrets", namespace, name)
}

func ExactConfigMap(namespace, name string) ExactResourceID {
	return ExactResource("", "v1", "configmaps", namespace, name)
}

func ExactNamespace(name string) ExactResourceID {
	return ExactResource("", "v1", "namespaces", "", name)
}

func ExactServiceAccount(namespace, name string) ExactResourceID {
	return ExactResource("", "v1", "serviceaccounts", namespace, name)
}

func ExactDeployment(namespace, name string) ExactResourceID {
	return ExactResource("apps", "v1", "deployments", namespace, name)
}

func ExactDaemonSet(namespace, name string) ExactResourceID {
	return ExactResource("apps", "v1", "daemonsets", namespace, name)
}

func ExactClusterOperator(name string) ExactResourceID {
	return ExactResource("config.openshift.io", "v1", "clusteroperators", "", name)
}

func ExactLowLevelOperator(resource string) ExactResourceID {
	return ExactResource("operator.openshift.io", "v1", resource, "", "cluster")
}

func ExactClusterRole(name string) ExactResourceID {
	return ExactResource("rbac.authorization.k8s.io", "v1", "clusterroles", "", name)
}

func ExactClusterRoleBinding(name string) ExactResourceID {
	return ExactResource("rbac.authorization.k8s.io", "v1", "clusterrolebindings", "", name)
}

func ExactRole(namespace, name string) ExactResourceID {
	return ExactResource("rbac.authorization.k8s.io", "v1", "roles", namespace, name)
}

func ExactRoleBinding(namespace, name string) ExactResourceID {
	return ExactResource("rbac.authorization.k8s.io", "v1", "rolebindings", namespace, name)
}

func ExactConfigResource(resource string) ExactResourceID {
	return ExactResource("config.openshift.io", "v1", resource, "", "cluster")
}

func SecretIdentifierType() InputResourceTypeIdentifier {
	return InputResourceTypeIdentifier{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}
}
