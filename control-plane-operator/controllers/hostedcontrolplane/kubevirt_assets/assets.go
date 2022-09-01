package kubevirt_assets

import (
	"bytes"
	"embed"
	"io"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func ptrbool(p bool) *bool {
	return &p
}

func ptrHostPathType(p corev1.HostPathType) *corev1.HostPathType {
	return &p
}

func ptrMountPropagationMode(p corev1.MountPropagationMode) *corev1.MountPropagationMode {
	return &p
}

//go:embed files/*
var resources embed.FS

func getContents(file string) []byte {
	f, err := resources.Open("files/" + file)
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			panic(err)
		}
	}()
	b, err := io.ReadAll(f)
	if err != nil {
		panic(err)
	}
	return b
}

func GenerateTenantNamespace(namespace string) crclient.Object {
	namespaceBytes := getContents("namespace.yaml")
	namespaceObject := &corev1.Namespace{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(namespaceBytes), 100).Decode(&namespaceObject); err != nil {
		panic(err)
	}
	namespaceObject.ObjectMeta.Name = namespace
	return namespaceObject
}

// schema.GroupVersionKind
//unstructured.Unstructured

func GenerateInfraServiceAccountResources(namespace string) (*corev1.ServiceAccount, *rbacv1.Role, *rbacv1.RoleBinding) {
	infraServiceAccountBytes := getContents("infra_serviceaccount.yaml")
	serviceaccount := &corev1.ServiceAccount{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(infraServiceAccountBytes), 500).Decode(&serviceaccount); err != nil {
		panic(err)
	}
	serviceaccount.ObjectMeta.Namespace = namespace

	infraRoleBytes := getContents("infra_role.yaml")
	role := &rbacv1.Role{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(infraRoleBytes), 500).Decode(&role); err != nil {
		panic(err)
	}
	role.ObjectMeta.Namespace = namespace

	infraRoleBindingBytes := getContents("infra_rolebinding.yaml")
	rolebinding := &rbacv1.RoleBinding{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(infraRoleBindingBytes), 500).Decode(&rolebinding); err != nil {
		panic(err)
	}
	rolebinding.ObjectMeta.Namespace = namespace
	rolebinding.Subjects[0].Namespace = namespace

	return serviceaccount, role, rolebinding
}

func GenerateTenantNodeServiceAccountResources(namespace string) []crclient.Object {
	infraServiceAccountBytes := getContents("tenant_node_serviceaccount.yaml")
	serviceaccount := &corev1.ServiceAccount{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(infraServiceAccountBytes), 500).Decode(&serviceaccount); err != nil {
		panic(err)
	}
	serviceaccount.ObjectMeta.Namespace = namespace

	infraClusterRoleBytes := getContents("tenant_node_clusterrole.yaml")
	clusterRole := &rbacv1.ClusterRole{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(infraClusterRoleBytes), 500).Decode(&clusterRole); err != nil {
		panic(err)
	}

	infraClusterRoleBindingBytes := getContents("tenant_node_clusterrolebinding.yaml")
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(infraClusterRoleBindingBytes), 500).Decode(&clusterRoleBinding); err != nil {
		panic(err)
	}
	clusterRoleBinding.Subjects[0].Namespace = namespace

	return []crclient.Object{serviceaccount, clusterRole, clusterRoleBinding}
}

func GenerateTenantControllerServiceAccountResources(namespace string) []crclient.Object {
	infraServiceAccountBytes := getContents("tenant_controller_serviceaccount.yaml")
	serviceaccount := &corev1.ServiceAccount{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(infraServiceAccountBytes), 500).Decode(&serviceaccount); err != nil {
		panic(err)
	}
	serviceaccount.ObjectMeta.Namespace = namespace

	infraClusterRoleBytes := getContents("tenant_controller_clusterrole.yaml")
	clusterRole := &rbacv1.ClusterRole{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(infraClusterRoleBytes), 500).Decode(&clusterRole); err != nil {
		panic(err)
	}

	infraClusterRoleBindingBytes := getContents("tenant_controller_clusterrolebinding.yaml")
	roleClusterBinding := &rbacv1.ClusterRoleBinding{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(infraClusterRoleBindingBytes), 500).Decode(&roleClusterBinding); err != nil {
		panic(err)
	}
	roleClusterBinding.Subjects[0].Namespace = namespace

	return []crclient.Object{serviceaccount, clusterRole, roleClusterBinding}
}

func GenerateTenantConfigmap(tenantNamespace string, infraNamespace string) crclient.Object {
	configmap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "driver-config",
			Namespace: tenantNamespace,
		},
		Data: map[string]string{
			"infraClusterNamespace": infraNamespace,
			"infraClusterLabels": "",
		},
	}
	return configmap
}

func GenerateInfraConfigmap(namespace string) crclient.Object {
	configmap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "driver-config",
			Namespace: namespace,
		},
		Data: map[string]string{
			"infraClusterNamespace": namespace,
			"infraClusterLabels": "",
		},
	}
	return configmap
}

func GenerateDaemonset(namespace string) crclient.Object {
	daemonsetBytes := getContents("daemonset.yaml")
	daemonset := &appsv1.DaemonSet{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(daemonsetBytes), 500).Decode(&daemonset); err != nil {
		panic(err)
	}
	daemonset.ObjectMeta.Namespace = namespace
	return daemonset
}

func GenerateController(namespace string) crclient.Object {
	controllerBytes := getContents("controller.yaml")
	controller := &appsv1.Deployment{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(controllerBytes), 500).Decode(&controller); err != nil {
		panic(err)
	}
	controller.ObjectMeta.Namespace = namespace
	return controller
}
