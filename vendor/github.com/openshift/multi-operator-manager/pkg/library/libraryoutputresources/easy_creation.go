package libraryoutputresources

func ExactResource(group, version, resource, namespace, name string) ExactResourceID {
	return ExactResourceID{
		OutputResourceTypeIdentifier: OutputResourceTypeIdentifier{
			Group:    group,
			Version:  version,
			Resource: resource,
		},
		Namespace: namespace,
		Name:      name,
	}
}

func GeneratedResource(group, version, resource, namespace, name string) GeneratedResourceID {
	return GeneratedResourceID{
		OutputResourceTypeIdentifier: OutputResourceTypeIdentifier{
			Group:    group,
			Version:  version,
			Resource: resource,
		},
		Namespace:     namespace,
		GeneratedName: name,
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

func GeneratedCSR(generateName string) GeneratedResourceID {
	return GeneratedResource("certificates.k8s.io", "v1", "certificatesigningrequests", "", generateName)
}

func ExactPDB(namespace, name string) ExactResourceID {
	return ExactResource("policy", "v1", "poddisruptionbudgets", namespace, name)
}

func ExactService(namespace, name string) ExactResourceID {
	return ExactResource("", "v1", "services", namespace, name)
}

func ExactOAuthClient(name string) ExactResourceID {
	return ExactResource("oauth.openshift.io", "v1", "oauthclients", "", name)
}
