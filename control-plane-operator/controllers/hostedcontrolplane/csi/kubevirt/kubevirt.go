package kubevirt

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/configoperator"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	sigyaml "sigs.k8s.io/yaml"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
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

func getStorageDriverType(hcp *hyperv1.HostedControlPlane) hyperv1.KubevirtStorageDriverConfigType {

	storageDriverType := hyperv1.DefaultKubevirtStorageDriverConfigType

	if hcp.Spec.Platform.Kubevirt != nil &&
		hcp.Spec.Platform.Kubevirt.StorageDriver != nil &&
		hcp.Spec.Platform.Kubevirt.StorageDriver.Type != "" {

		storageDriverType = hcp.Spec.Platform.Kubevirt.StorageDriver.Type
	}
	return storageDriverType
}

type StorageSnapshotMapping struct {
	VolumeSnapshotClasses []string `yaml:"volumeSnapshotClasses,omitempty"`
	StorageClasses        []string `yaml:"storageClasses"`
}

func reconcileInfraConfigMap(cm *corev1.ConfigMap, hcp *hyperv1.HostedControlPlane) error {
	var storageClassEnforcement string

	storageDriverType := getStorageDriverType(hcp)

	switch storageDriverType {
	case hyperv1.ManualKubevirtStorageDriverConfigType:
		allowedSC := []string{}
		storageMap := make(map[string][]string)
		snapshotMap := make(map[string][]string)

		if hcp.Spec.Platform.Kubevirt.StorageDriver.Manual != nil {
			for _, mapping := range hcp.Spec.Platform.Kubevirt.StorageDriver.Manual.StorageClassMapping {
				allowedSC = append(allowedSC, mapping.InfraStorageClassName)
				storageMap[mapping.Group] = append(storageMap[mapping.Group], mapping.InfraStorageClassName)
			}
			for _, mapping := range hcp.Spec.Platform.Kubevirt.StorageDriver.Manual.VolumeSnapshotClassMapping {
				snapshotMap[mapping.Group] = append(snapshotMap[mapping.Group], mapping.InfraVolumeSnapshotClassName)
			}
		}

		storageSnapshotMapping := []StorageSnapshotMapping{}
		for group, storageClasses := range storageMap {
			mapping := StorageSnapshotMapping{}
			mapping.StorageClasses = storageClasses
			mapping.VolumeSnapshotClasses = snapshotMap[group]
			delete(snapshotMap, group)
			storageSnapshotMapping = append(storageSnapshotMapping, mapping)
		}
		for _, snapshotClasses := range snapshotMap {
			mapping := StorageSnapshotMapping{}
			mapping.VolumeSnapshotClasses = snapshotClasses
			storageSnapshotMapping = append(storageSnapshotMapping, mapping)
		}
		mappingBytes, err := sigyaml.Marshal(storageSnapshotMapping)
		if err != nil {
			return err
		}
		// For some reason yaml.Marhsal is generating upper case keys, so we need to convert them to lower case
		mappingBytes = bytes.ReplaceAll(mappingBytes, []byte("VolumeSnapshotClasses"), []byte("volumeSnapshotClasses"))
		mappingBytes = bytes.ReplaceAll(mappingBytes, []byte("StorageClasses"), []byte("storageClasses"))
		storageClassEnforcement = fmt.Sprintf("allowAll: false\nallowList: [%s]\nstorageSnapshotMapping: \n%s", strings.Join(allowedSC, ", "), string(mappingBytes))
	case hyperv1.NoneKubevirtStorageDriverConfigType:
		storageClassEnforcement = "allowDefault: false\nallowAll: false\n"
	case hyperv1.DefaultKubevirtStorageDriverConfigType:
		storageClassEnforcement = "allowDefault: true\nallowAll: false\n"
	default:
		storageClassEnforcement = "allowDefault: true\nallowAll: false\n"
	}
	var infraClusterNamespace string
	if configoperator.IsExternalInfraKv(hcp) {
		infraClusterNamespace = hcp.Spec.Platform.Kubevirt.Credentials.InfraNamespace
	} else {
		infraClusterNamespace = cm.Namespace
	}
	cm.Data = map[string]string{
		"infraClusterNamespace":        infraClusterNamespace,
		"infraClusterLabels":           fmt.Sprintf("%s=%s", hyperv1.InfraIDLabel, hcp.Spec.InfraID),
		"infraStorageClassEnforcement": storageClassEnforcement,
	}
	return nil
}

func reconcileController(controller *appsv1.Deployment, releaseImageProvider imageprovider.ReleaseImageProvider, deploymentConfig *config.DeploymentConfig, hcp *hyperv1.HostedControlPlane) error {
	controller.Spec = *controllerDeployment.Spec.DeepCopy()

	csiDriverImage, exists := releaseImageProvider.ImageExist("kubevirt-csi-driver")
	if !exists {
		return fmt.Errorf("unable to detect kubevirt-csi-driver image from release payload")
	}

	csiProvisionerImage, exists := releaseImageProvider.ImageExist("csi-external-provisioner")
	if !exists {
		return fmt.Errorf("unable to detect csi-external-provisioner image from release payload")
	}

	csiAttacherImage, exists := releaseImageProvider.ImageExist("csi-external-attacher")
	if !exists {
		return fmt.Errorf("unable to detect csi-external-attacher image from release payload")
	}

	csiLivenessProbeImage, exists := releaseImageProvider.ImageExist("csi-livenessprobe")
	if !exists {
		return fmt.Errorf("unable to detect csi-livenessprobe image from release payload")
	}

	csiExternalSnapshotterImage, exists := releaseImageProvider.ImageExist("csi-external-snapshotter")
	if !exists {
		return fmt.Errorf("unable to detect csi-external-snapshotter image from release payload")
	}

	csiExternalResizerImage, exists := releaseImageProvider.ImageExist("csi-external-resizer")
	if !exists {
		return fmt.Errorf("unable to detect csi-external-resizer image from release payload")
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
		case "csi-snapshotter":
			controller.Spec.Template.Spec.Containers[i].Image = csiExternalSnapshotterImage
		case "csi-resizer":
			controller.Spec.Template.Spec.Containers[i].Image = csiExternalResizerImage
		}
	}

	if configoperator.IsExternalInfraKv(hcp) {
		csiDriverContainerIndex := func() int {
			for i, container := range controller.Spec.Template.Spec.Containers {
				if container.Name == "csi-driver" {
					return i
				}
			}
			return -1
		}
		containerIndex := csiDriverContainerIndex()
		if containerIndex == -1 {
			return fmt.Errorf("unable to find csi-driver container in %s pod", controllerDeployment.Name)
		}
		csiDriverContainer := controller.Spec.Template.Spec.Containers[containerIndex]
		const infraClusterKubeconfigMount = "/var/run/secrets/infracluster"
		csiDriverContainer.Args = append(csiDriverContainer.Args, fmt.Sprintf("--infra-cluster-kubeconfig=%s/kubeconfig", infraClusterKubeconfigMount))

		externalKubeconfigVolumeMount := corev1.VolumeMount{
			Name:      "infracluster",
			MountPath: infraClusterKubeconfigMount,
		}
		csiDriverContainer.VolumeMounts = append(csiDriverContainer.VolumeMounts, externalKubeconfigVolumeMount)
		controller.Spec.Template.Spec.Containers[containerIndex] = csiDriverContainer

		infraClusterVolume := corev1.Volume{
			Name: "infracluster",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: hyperv1.KubeVirtInfraCredentialsSecretName,
				},
			},
		}
		controller.Spec.Template.Spec.Volumes = append(controller.Spec.Template.Spec.Volumes, infraClusterVolume)
	}

	deploymentConfig.ApplyTo(controller)

	return nil
}

func reconcileInfraSA(sa *corev1.ServiceAccount) error {
	util.EnsurePullSecret(sa, common.PullSecret("").Name)

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

func reconcileCustomTenantStorageClass(sc *storagev1.StorageClass, infraSCName string) error {
	sc.Provisioner = "csi.kubevirt.io"
	sc.Parameters = map[string]string{
		"bus":                   "scsi",
		"infraStorageClassName": infraSCName,
	}
	sc.AllowVolumeExpansion = ptr.To(true)

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
	sc.AllowVolumeExpansion = ptr.To(true)

	return nil
}

func reconcileDefaultTenantCSIDriverResource(csiDriver *storagev1.CSIDriver) error {
	csiDriver.Spec.AttachRequired = ptr.To(true)
	csiDriver.Spec.PodInfoOnMount = ptr.To(true)
	fsPolicy := storagev1.ReadWriteOnceWithFSTypeFSGroupPolicy
	csiDriver.Spec.FSGroupPolicy = &fsPolicy
	return nil
}

func reconcileTenantVolumeSnapshotClass(volumeSnapshotClass *snapshotv1.VolumeSnapshotClass, infraVSCName string) error {
	volumeSnapshotClass.Driver = "csi.kubevirt.io"
	volumeSnapshotClass.DeletionPolicy = snapshotv1.VolumeSnapshotContentDelete
	if infraVSCName != "" {
		volumeSnapshotClass.Parameters = map[string]string{
			"infraSnapshotClassName": infraVSCName,
		}
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

	if ds.Spec.Template.ObjectMeta.Annotations == nil {
		ds.Spec.Template.ObjectMeta.Annotations = map[string]string{}
	}

	ds.Spec.Template.ObjectMeta.Annotations["target.workload.openshift.io/management"] = `{"effect": "PreferredDuringScheduling"}`
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

func reconcileTenantStorageClasses(client crclient.Client, hcp *hyperv1.HostedControlPlane, ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN) error {

	switch getStorageDriverType(hcp) {
	case hyperv1.ManualKubevirtStorageDriverConfigType:
		if hcp.Spec.Platform.Kubevirt.StorageDriver.Manual != nil {
			for _, mapping := range hcp.Spec.Platform.Kubevirt.StorageDriver.Manual.StorageClassMapping {
				customSC := manifests.KubevirtCSIDriverDefaultTenantStorageClass()
				customSC.Name = mapping.GuestStorageClassName
				_, err := createOrUpdate(ctx, client, customSC, func() error {
					return reconcileCustomTenantStorageClass(customSC, mapping.InfraStorageClassName)
				})
				if err != nil {
					return err
				}
			}
		}
	case hyperv1.NoneKubevirtStorageDriverConfigType:
		// do nothing.
	default:
		storageClass := manifests.KubevirtCSIDriverDefaultTenantStorageClass()
		_, err := createOrUpdate(ctx, client, storageClass, func() error {
			return reconcileDefaultTenantStorageClass(storageClass)
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func reconcileTenantVolumeSnapshotClasses(client crclient.Client, hcp *hyperv1.HostedControlPlane, ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN) error {
	switch getStorageDriverType(hcp) {
	case hyperv1.ManualKubevirtStorageDriverConfigType:
		if hcp.Spec.Platform.Kubevirt.StorageDriver.Manual != nil {
			for _, mapping := range hcp.Spec.Platform.Kubevirt.StorageDriver.Manual.VolumeSnapshotClassMapping {
				customVSC := manifests.KubevirtCSIDriverVolumeSnapshotClass()
				customVSC.Name = mapping.GuestVolumeSnapshotClassName
				_, err := createOrUpdate(ctx, client, customVSC, func() error {
					return reconcileTenantVolumeSnapshotClass(customVSC, mapping.InfraVolumeSnapshotClassName)
				})
				if err != nil {
					return err
				}
			}
		}
	case hyperv1.NoneKubevirtStorageDriverConfigType:
		// do nothing.
	default:
		volumeSnapshotClass := manifests.KubevirtCSIDriverVolumeSnapshotClass()
		_, err := createOrUpdate(ctx, client, volumeSnapshotClass, func() error {
			return reconcileTenantVolumeSnapshotClass(volumeSnapshotClass, "")
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func ReconcileTenant(client crclient.Client, hcp *hyperv1.HostedControlPlane, ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, componentImages map[string]string) error {

	if getStorageDriverType(hcp) == hyperv1.NoneKubevirtStorageDriverConfigType {
		return nil
	}

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

	tenantNodeClusterRole := manifests.KubevirtCSIDriverTenantNodeClusterRole()
	_, err = createOrUpdate(ctx, client, tenantNodeClusterRole, func() error {
		return reconcileTenantNodeClusterRole(tenantNodeClusterRole)
	})
	if err != nil {
		return err
	}

	tenantNodeClusterRoleBinding := manifests.KubevirtCSIDriverTenantNodeClusterRoleBinding()
	_, err = createOrUpdate(ctx, client, tenantNodeClusterRoleBinding, func() error {
		return reconcileTenantNodeClusterRoleBinding(tenantNodeClusterRoleBinding, tenantNamespace)
	})
	if err != nil {
		return err
	}

	tenantControllerClusterRoleBinding := manifests.KubevirtCSIDriverTenantControllerClusterRoleBinding()
	_, err = createOrUpdate(ctx, client, tenantControllerClusterRoleBinding, func() error {
		return reconcileTenantControllerClusterRoleBinding(tenantControllerClusterRoleBinding, tenantNamespace)
	})
	if err != nil {
		return err
	}

	tenantControllerClusterRole := manifests.KubevirtCSIDriverTenantControllerClusterRole()
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

	err = reconcileTenantStorageClasses(client, hcp, ctx, createOrUpdate)
	if err != nil {
		return err
	}

	err = reconcileTenantVolumeSnapshotClasses(client, hcp, ctx, createOrUpdate)
	if err != nil {
		return err
	}

	csidriverResource := manifests.KubevirtCSIDriverResource()
	_, err = createOrUpdate(ctx, client, csidriverResource, func() error {
		return reconcileDefaultTenantCSIDriverResource(csidriverResource)
	})
	if err != nil {
		return err
	}

	return nil
}

// ReconcileInfra reconciles the csi driver controller on the underlying infra/Mgmt cluster
// that is hosting the KubeVirt VMs.
func ReconcileInfra(client crclient.Client, hcp *hyperv1.HostedControlPlane, ctx context.Context, createOrUpdate upsert.CreateOrUpdateFN, releaseImageProvider imageprovider.ReleaseImageProvider) error {

	// Do not install kubevirt-csi if the storage driver is set to NONE
	if getStorageDriverType(hcp) == hyperv1.NoneKubevirtStorageDriverConfigType {
		return nil
	}

	deploymentConfig := &config.DeploymentConfig{}
	deploymentConfig.Scheduling.PriorityClass = config.DefaultPriorityClass
	deploymentConfig.SetRestartAnnotation(hcp.ObjectMeta)
	deploymentConfig.SetDefaults(hcp, nil, ptr.To(1))

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

	tenantControllerKubeconfigSecret := manifests.KubevirtCSIDriverTenantKubeConfig(infraNamespace)
	_, err = createOrUpdate(ctx, client, tenantControllerKubeconfigSecret, func() error {
		return pki.ReconcileServiceAccountKubeconfig(tenantControllerKubeconfigSecret, csrSigner, rootCA, hcp, manifests.KubevirtCSIDriverTenantNamespaceStr, "kubevirt-csi-controller-sa")
	})
	if err != nil {
		return err
	}

	infraConfigMap := manifests.KubevirtCSIDriverInfraConfigMap(infraNamespace)
	_, err = createOrUpdate(ctx, client, infraConfigMap, func() error {
		return reconcileInfraConfigMap(infraConfigMap, hcp)
	})
	if err != nil {
		return err
	}

	controller := manifests.KubevirtCSIDriverController(infraNamespace)
	_, err = createOrUpdate(ctx, client, controller, func() error {
		return reconcileController(controller, releaseImageProvider, deploymentConfig, hcp)
	})
	if err != nil {
		return err
	}

	return nil
}
