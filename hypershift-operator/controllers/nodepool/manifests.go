package nodepool

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	capiv1 "github.com/openshift/hypershift/thirdparty/clusterapi/api/v1alpha4"
	capiaws "github.com/openshift/hypershift/thirdparty/clusterapiprovideraws/v1alpha4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sutilspointer "k8s.io/utils/pointer"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func machineDeployment(nodePool *hyperv1.NodePool, clusterName string, controlPlaneNamespace string) *capiv1.MachineDeployment {
	resourcesName := generateName(clusterName, nodePool.Spec.ClusterName, nodePool.GetName())
	return &capiv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourcesName,
			Namespace: controlPlaneNamespace,
		},
	}
}

func machineHealthCheck(nodePool *hyperv1.NodePool, controlPlaneNamespace string) *capiv1.MachineHealthCheck {
	return &capiv1.MachineHealthCheck{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      nodePool.GetName(),
			Namespace: controlPlaneNamespace,
		},
	}
}

func AWSMachineTemplate(infraName, ami string, nodePool *hyperv1.NodePool, controlPlaneNamespace string) *capiaws.AWSMachineTemplate {
	subnet := &capiaws.AWSResourceReference{}
	if nodePool.Spec.Platform.AWS.Subnet != nil {
		subnet.ID = nodePool.Spec.Platform.AWS.Subnet.ID
		subnet.ARN = nodePool.Spec.Platform.AWS.Subnet.ARN
		for k := range nodePool.Spec.Platform.AWS.Subnet.Filters {
			filter := capiaws.Filter{
				Name:   nodePool.Spec.Platform.AWS.Subnet.Filters[k].Name,
				Values: nodePool.Spec.Platform.AWS.Subnet.Filters[k].Values,
			}
			subnet.Filters = append(subnet.Filters, filter)
		}
	}

	securityGroups := []capiaws.AWSResourceReference{}
	for _, sg := range nodePool.Spec.Platform.AWS.SecurityGroups {
		filters := []capiaws.Filter{}
		for _, f := range sg.Filters {
			filters = append(filters, capiaws.Filter{
				Name:   f.Name,
				Values: f.Values,
			})
		}
		securityGroups = append(securityGroups, capiaws.AWSResourceReference{
			ARN:     sg.ARN,
			ID:      sg.ID,
			Filters: filters,
		})
	}

	instanceProfile := fmt.Sprintf("%s-worker-profile", infraName)
	if nodePool.Spec.Platform.AWS.InstanceProfile != "" {
		instanceProfile = nodePool.Spec.Platform.AWS.InstanceProfile
	}

	instanceType := nodePool.Spec.Platform.AWS.InstanceType

	awsMachineTemplate := &capiaws.AWSMachineTemplate{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				nodePoolAnnotation: ctrlclient.ObjectKeyFromObject(nodePool).String(),
			},
			Namespace: controlPlaneNamespace,
		},
		Spec: capiaws.AWSMachineTemplateSpec{
			Template: capiaws.AWSMachineTemplateResource{
				Spec: capiaws.AWSMachineSpec{
					UncompressedUserData: k8sutilspointer.BoolPtr(true),
					CloudInit: capiaws.CloudInit{
						InsecureSkipSecretsManager: true,
						SecureSecretsBackend:       "secrets-manager",
					},
					IAMInstanceProfile: instanceProfile,
					InstanceType:       instanceType,
					AMI: capiaws.AWSResourceReference{
						ID: k8sutilspointer.StringPtr(ami),
					},
					AdditionalSecurityGroups: securityGroups,
					Subnet:                   subnet,
				},
			},
		},
	}
	specHash := hashStruct(awsMachineTemplate.Spec.Template.Spec)
	awsMachineTemplate.SetName(fmt.Sprintf("%s-%s", nodePool.GetName(), specHash))

	return awsMachineTemplate
}

func MachineConfigServerDeployment(machineConfigServerNamespace, machineConfigServerName string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineConfigServerNamespace,
			Name:      fmt.Sprintf("machine-config-server-%s", machineConfigServerName),
		},
	}
}

func MachineConfigServerServiceAccount(machineConfigServerNamespace, machineConfigServerName string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineConfigServerNamespace,
			Name:      fmt.Sprintf("machine-config-server-%s", machineConfigServerName),
		},
	}
}

func MachineConfigServerRoleBinding(machineConfigServerNamespace, machineConfigServerName string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineConfigServerNamespace,
			Name:      fmt.Sprintf("machine-config-server-%s", machineConfigServerName),
		},
	}
}

func MachineConfigServerService(machineConfigServerNamespace, machineConfigServerName string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineConfigServerNamespace,
			Name:      fmt.Sprintf("machine-config-server-%s", machineConfigServerName),
		},
	}
}

func MachineConfigServerUserDataSecret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      fmt.Sprintf("user-data-%s", name),
		},
	}
}
