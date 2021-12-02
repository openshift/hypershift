package nodepool

import (
	"fmt"

	agentv1 "github.com/eranco74/cluster-api-provider-agent/api/v1alpha1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sutilspointer "k8s.io/utils/pointer"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	EC2VolumeDefaultSize int64  = 16
	EC2VolumeDefaultType string = "gp2"
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

func AWSMachineTemplate(infraName, ami string, hostedCluster *hyperv1.HostedCluster, nodePool *hyperv1.NodePool, controlPlaneNamespace string) (*capiaws.AWSMachineTemplate, string) {
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
	rootVolume := &capiaws.Volume{
		Size: EC2VolumeDefaultSize,
	}
	if nodePool.Spec.Platform.AWS.RootVolume != nil {
		if nodePool.Spec.Platform.AWS.RootVolume.Type != "" {
			rootVolume.Type = capiaws.VolumeType(nodePool.Spec.Platform.AWS.RootVolume.Type)
		} else {
			rootVolume.Type = capiaws.VolumeType(EC2VolumeDefaultType)
		}
		if nodePool.Spec.Platform.AWS.RootVolume.Size > 0 {
			rootVolume.Size = nodePool.Spec.Platform.AWS.RootVolume.Size
		}
		if nodePool.Spec.Platform.AWS.RootVolume.IOPS > 0 {
			rootVolume.IOPS = nodePool.Spec.Platform.AWS.RootVolume.IOPS
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

	var tags capiaws.Tags
	for _, tag := range append(nodePool.Spec.Platform.AWS.ResourceTags, hostedCluster.Spec.Platform.AWS.ResourceTags...) {
		if tags == nil {
			tags = capiaws.Tags{}
		}
		tags[tag.Key] = tag.Value
	}

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
					AMI: capiaws.AMIReference{
						ID: k8sutilspointer.StringPtr(ami),
					},
					AdditionalSecurityGroups: securityGroups,
					Subnet:                   subnet,
					RootVolume:               rootVolume,
					AdditionalTags:           tags,
				},
			},
		},
	}
	specHash := hashStruct(awsMachineTemplate.Spec.Template.Spec)
	awsMachineTemplate.SetName(fmt.Sprintf("%s-%s", nodePool.GetName(), specHash))

	return awsMachineTemplate, specHash
}

func IgnitionUserDataSecret(namespace, name, payloadInputHash string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      fmt.Sprintf("user-data-%s-%s", name, payloadInputHash),
		},
	}
}

func TokenSecret(namespace, name, payloadInputHash string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      fmt.Sprintf("token-%s-%s", name, payloadInputHash),
		},
	}
}

func AgentMachineTemplate(nodePool *hyperv1.NodePool, controlPlaneNamespace string) *agentv1.AgentMachineTemplate {
	agentMachineTemplate := &agentv1.AgentMachineTemplate{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				nodePoolAnnotation: ctrlclient.ObjectKeyFromObject(nodePool).String(),
			},
			Namespace: controlPlaneNamespace,
		},
		Spec: agentv1.AgentMachineTemplateSpec{
			Template: agentv1.AgentMachineTemplateResource{
				Spec: agentv1.AgentMachineSpec{},
			},
		},
	}
	specHash := hashStruct(agentMachineTemplate.Spec.Template.Spec)
	agentMachineTemplate.SetName(fmt.Sprintf("%s-%s", nodePool.GetName(), specHash))

	return agentMachineTemplate
}
