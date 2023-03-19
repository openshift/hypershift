package kubevirt

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/yaml"
	utilpointer "k8s.io/utils/pointer"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed files/*
var resources embed.FS

var (
	controllerDeployment               = mustDeployment("controller.yaml")
	infraRole                          = mustRole("infra_role.yaml")
	infraRoleBinding                   = mustRoleBinding("infra_rolebinding.yaml")
	tenantControllerClusterRole        = mustClusterRole("tenant_controller_clusterrole.yaml")
	tenantControllerClusterRoleBinding = mustClusterRoleBinding("tenant_controller_clusterrolebinding.yaml")

	tenantNodeClusterRole        = mustClusterRole("tenant_node_clusterrole.yaml")
	tenantNodeClusterRoleBinding = mustClusterRoleBinding("tenant_node_clusterrolebinding.yaml")

	daemonset = mustDaemonSet("daemonset.yaml")

	defaultResourceRequirements = corev1.ResourceRequirements{Requests: corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("10m"),
		corev1.ResourceMemory: resource.MustParse("50Mi"),
	}}
)

func mustDeployment(file string) *appsv1.Deployment {

	controllerBytes := getContentsOrDie(file)
	controller := &appsv1.Deployment{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(controllerBytes), 500).Decode(&controller); err != nil {
		panic(err)
	}

	return controller
}

func mustDaemonSet(file string) *appsv1.DaemonSet {
	b := getContentsOrDie(file)
	obj := &appsv1.DaemonSet{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(b), 500).Decode(&obj); err != nil {
		panic(err)
	}

	return obj
}

func mustClusterRole(file string) *rbacv1.ClusterRole {
	b := getContentsOrDie(file)
	obj := &rbacv1.ClusterRole{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(b), 500).Decode(&obj); err != nil {
		panic(err)
	}

	return obj
}

func mustClusterRoleBinding(file string) *rbacv1.ClusterRoleBinding {
	b := getContentsOrDie(file)
	obj := &rbacv1.ClusterRoleBinding{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(b), 500).Decode(&obj); err != nil {
		panic(err)
	}

	return obj
}

func mustRole(file string) *rbacv1.Role {
	b := getContentsOrDie(file)
	obj := &rbacv1.Role{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(b), 500).Decode(&obj); err != nil {
		panic(err)
	}

	return obj
}

func mustRoleBinding(file string) *rbacv1.RoleBinding {
	b := getContentsOrDie(file)
	obj := &rbacv1.RoleBinding{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(b), 500).Decode(&obj); err != nil {
		panic(err)
	}

	return obj
}

func getContentsOrDie(file string) []byte {
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

func reconcileInfraConfigMap(cm *corev1.ConfigMap, infraID string) error {
	cm.Data = map[string]string{
		"infraClusterNamespace": cm.Namespace,
		"infraClusterLabels":    fmt.Sprintf("%s=%s", hyperv1.InfraIDLabel, infraID),
	}
	return nil
}

func reconcileController(controller *appsv1.Deployment, componentImages map[string]string, deploymentConfig *config.DeploymentConfig) error {
	controller.Spec = *controllerDeployment.Spec.DeepCopy()

	csiDriverImage, exists := componentImages["kubevirt-csi-driver"]
	if !exists {
		return fmt.Errorf("unable to detect kubevirt-csi-driver image from release payload")
	}

	csiProvisionerImage, exists := componentImages["csi-external-provisioner"]
	if !exists {
		return fmt.Errorf("unable to detect csi-external-provisioner image from release payload")
	}

	csiAttacherImage, exists := componentImages["csi-external-attacher"]
	if !exists {
		return fmt.Errorf("unable to detect csi-external-attacher image from release payload")
	}

	csiLivenessProbeImage, exists := componentImages["csi-livenessprobe"]
	if !exists {
		return fmt.Errorf("unable to detect csi-livenessprobe image from release payload")
	}

	for i, container := range controller.Spec.Template.Spec.Containers {
		if len(container.Resources.Requests) == 0 && len(container.Resources.Limits) == 0 {
			controller.Spec.Template.Spec.Containers[i].Resources = defaultResourceRequirements
		}
		controller.Spec.Template.Spec.Containers[i].ImagePullPolicy = corev1.PullIfNotPresent
		switch container.Name {
		case "csi-driver":
			controller.Spec.Template.Spec.Containers[i].Image = csiDriverImage
		case "csi-provisioner":
			controller.Spec.Template.Spec.Containers[i].Image = csiProvisionerImage
		case "csi-attacher":
			controller.Spec.Template.Spec.Containers[i].Image = csiAttacherImage
		case "csi-liveness-probe":
			controller.Spec.Template.Spec.Containers[i].Image = csiLivenessProbeImage
		}
	}

	deploymentConfig.ApplyTo(controller)

	return nil
}

func reconcileInfraSA(sa *corev1.ServiceAccount) error {
	return nil
}

func reconcileInfraRole(role *rbacv1.Role) error {
	role.Rules = infraRole.DeepCopy().Rules
	return nil
}

func reconcileInfraRoleBinding(roleBinding *rbacv1.RoleBinding) error {
	dc := infraRoleBinding.DeepCopy()

	roleBinding.RoleRef = dc.RoleRef
	roleBinding.Subjects = dc.Subjects

	for i := range roleBinding.Subjects {
		roleBinding.Subjects[i].Namespace = roleBinding.Namespace
	}
	return nil
}

func reconcileTenantControllerSA(sa *corev1.ServiceAccount) error {
	return nil
}

func reconcileTenantControllerClusterRole(cr *rbacv1.ClusterRole) error {
	cr.Rules = tenantControllerClusterRole.DeepCopy().Rules
	return nil
}

func reconcileTenantControllerClusterRoleBinding(crb *rbacv1.ClusterRoleBinding, saNamespace string) error {
	dc := tenantControllerClusterRoleBinding.DeepCopy()

	crb.RoleRef = dc.RoleRef
	crb.Subjects = dc.Subjects

	for i := range crb.Subjects {
		crb.Subjects[i].Namespace = saNamespace
	}
	return nil
}

func reconcileDefaultTenantStorageClass(sc *storagev1.StorageClass) error {
	sc.Annotations = map[string]string{
		"storageclass.kubernetes.io/is-default-class": "true",
	}
	sc.Provisioner = "csi.kubevirt.io"
	sc.Parameters = map[string]string{
		"bus": "scsi",
	}

	return nil
}

func reconcileTenantNodeSA(sa *corev1.ServiceAccount) error {
	return nil
}

func reconcileTenantNodeClusterRole(cr *rbacv1.ClusterRole) error {
	cr.Rules = tenantNodeClusterRole.DeepCopy().Rules
	return nil
}

func reconcileTenantNodeClusterRoleBinding(crb *rbacv1.ClusterRoleBinding, saNamespace string) error {
	dc := tenantNodeClusterRoleBinding.DeepCopy()

	crb.RoleRef = dc.RoleRef
	crb.Subjects = dc.Subjects

	for i := range crb.Subjects {
		crb.Subjects[i].Namespace = saNamespace
	}
	return nil
}

func reconcileTenantDaemonset(ds *appsv1.DaemonSet, componentImages map[string]string) error {
	ds.Spec = *daemonset.Spec.DeepCopy()

	csiDriverImage, exists := componentImages["kubevirt-csi-driver"]
	if !exists {
		return fmt.Errorf("unable to detect kubevirt-csi-driver image from release payload")
	}

	csiNodeDriverRegistrarImage, exists := componentImages["csi-node-driver-registrar"]
	if !exists {
		return fmt.Errorf("unable to detect csi-node-driver-registrar image from release payload")
	}

	csiLivenessProbeImage, exists := componentImages["csi-livenessprobe"]
	if !exists {
		return fmt.Errorf("unable to detect csi-livenessprobe image from release payload")
	}

	for i, container := range ds.Spec.Template.Spec.Containers {
		if len(container.Resources.Requests) == 0 && len(container.Resources.Limits) == 0 {
			ds.Spec.Template.Spec.Containers[i].Resources = defaultResourceRequirements
		}
		ds.Spec.Template.Spec.Containers[i].ImagePullPolicy = corev1.PullIfNotPresent
		switch container.Name {
		case "csi-driver":
			ds.Spec.Template.Spec.Containers[i].Image = csiDriverImage
		case "csi-node-driver-registrar":
			ds.Spec.Template.Spec.Containers[i].Image = csiNodeDriverRegistrarImage
		case "csi-liveness-probe":
			ds.Spec.Template.Spec.Containers[i].Image = csiLivenessProbeImage
		}
	}

	return nil
}

func ReconcileTenant(client crclient.Client, hcp *hyperv1.HostedControlPlane, ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, componentImages map[string]string) error {

	tenantNamespace := manifests.KubevirtCSIDriverTenantNamespaceStr

	ns := manifests.KubevirtCSIDriverTenantNamespace(tenantNamespace)
	_, err := createOrUpdate(ctx, client, ns, func() error { return nil })
	if err != nil {
		return err
	}

	tenantNodeServiceAccount := manifests.KubevirtCSIDriverTenantNodeSA(tenantNamespace)
	_, err = createOrUpdate(ctx, client, tenantNodeServiceAccount, func() error {
		return reconcileTenantNodeSA(tenantNodeServiceAccount)
	})
	if err != nil {
		return err
	}

	tenantNodeClusterRole := manifests.KubevirtCSIDriverTenantNodeClusterRole(tenantNamespace)
	_, err = createOrUpdate(ctx, client, tenantNodeClusterRole, func() error {
		return reconcileTenantNodeClusterRole(tenantNodeClusterRole)
	})
	if err != nil {
		return err
	}

	tenantNodeClusterRoleBinding := manifests.KubevirtCSIDriverTenantNodeClusterRoleBinding(tenantNamespace)
	_, err = createOrUpdate(ctx, client, tenantNodeClusterRoleBinding, func() error {
		return reconcileTenantNodeClusterRoleBinding(tenantNodeClusterRoleBinding, tenantNamespace)
	})
	if err != nil {
		return err
	}

	tenantControllerClusterRoleBinding := manifests.KubevirtCSIDriverTenantControllerClusterRoleBinding(tenantNamespace)
	_, err = createOrUpdate(ctx, client, tenantControllerClusterRoleBinding, func() error {
		return reconcileTenantControllerClusterRoleBinding(tenantControllerClusterRoleBinding, tenantNamespace)
	})
	if err != nil {
		return err
	}

	tenantControllerClusterRole := manifests.KubevirtCSIDriverTenantControllerClusterRole(tenantNamespace)
	_, err = createOrUpdate(ctx, client, tenantControllerClusterRole, func() error {
		return reconcileTenantControllerClusterRole(tenantControllerClusterRole)
	})
	if err != nil {
		return err
	}

	tenantControllerServiceAccount := manifests.KubevirtCSIDriverTenantControllerSA(tenantNamespace)
	_, err = createOrUpdate(ctx, client, tenantControllerServiceAccount, func() error {
		return reconcileTenantControllerSA(tenantControllerServiceAccount)
	})
	if err != nil {
		return err
	}

	daemonSet := manifests.KubevirtCSIDriverDaemonSet(tenantNamespace)
	_, err = createOrUpdate(ctx, client, daemonSet, func() error {
		return reconcileTenantDaemonset(daemonSet, componentImages)
	})
	if err != nil {
		return err
	}

	storageClass := manifests.KubevirtCSIDriverDefaultTenantStorageClass()
	_, err = createOrUpdate(ctx, client, storageClass, func() error {
		return reconcileDefaultTenantStorageClass(storageClass)
	})
	if err != nil {
		return err
	}

	return nil
}

// ReconcileInfra reconciles the csi driver controller on the underlying infra/Mgmt cluster
// that is hosting the KubeVirt VMs.
func ReconcileInfra(client crclient.Client, hcp *hyperv1.HostedControlPlane, ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, componentImages map[string]string) error {

	deploymentConfig := &config.DeploymentConfig{}
	deploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	deploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	deploymentConfig.SetDefaults(hcp, nil, utilpointer.IntPtr(1))

	infraNamespace := hcp.Namespace

	infraServiceAccount := manifests.KubevirtCSIDriverInfraSA(infraNamespace)
	_, err := createOrUpdate(ctx, client, infraServiceAccount, func() error {
		return reconcileInfraSA(infraServiceAccount)
	})
	if err != nil {
		return err
	}

	infraRole := manifests.KubevirtCSIDriverInfraRole(infraNamespace)
	_, err = createOrUpdate(ctx, client, infraRole, func() error {
		return reconcileInfraRole(infraRole)
	})
	if err != nil {
		return err
	}

	infraRoleBinding := manifests.KubevirtCSIDriverInfraRoleBinding(infraNamespace)
	_, err = createOrUpdate(ctx, client, infraRoleBinding, func() error {
		return reconcileInfraRoleBinding(infraRoleBinding)
	})
	if err != nil {
		return err
	}

	rootCA := manifests.RootCAConfigMap(hcp.Namespace)
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca cert secret: %w", err)
	}

	csrSigner := manifests.CSRSignerCASecret(hcp.Namespace)
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(csrSigner), csrSigner); err != nil {
		return fmt.Errorf("failed to get csr signer cert secret: %w", err)
	}

	if err != nil {
		return err
	}
	tenantControllerKubeconfigSecret := manifests.KubevirtCSIDriverTenantKubeConfig(infraNamespace)
	_, err = createOrUpdate(ctx, client, tenantControllerKubeconfigSecret, func() error {
		return pki.ReconcileServiceAccountKubeconfig(tenantControllerKubeconfigSecret, csrSigner, rootCA, hcp, manifests.KubevirtCSIDriverTenantNamespaceStr, "kubevirt-csi-controller-sa")
	})
	if err != nil {
		return err
	}

	infraConfigMap := manifests.KubevirtCSIDriverInfraConfigMap(infraNamespace)
	_, err = createOrUpdate(ctx, client, infraConfigMap, func() error {
		return reconcileInfraConfigMap(infraConfigMap, hcp.Spec.InfraID)
	})
	if err != nil {
		return err
	}

	controller := manifests.KubevirtCSIDriverController(infraNamespace)
	_, err = createOrUpdate(ctx, client, controller, func() error {
		return reconcileController(controller, componentImages, deploymentConfig)
	})
	if err != nil {
		return err
	}

	return nil
}
