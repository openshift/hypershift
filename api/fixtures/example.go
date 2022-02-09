package fixtures

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"

	apiresource "k8s.io/apimachinery/pkg/api/resource"
	kubevirtv1 "kubevirt.io/api/core/v1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type ExampleResources struct {
	Namespace  *corev1.Namespace
	PullSecret *corev1.Secret
	Resources  []crclient.Object
	SSHKey     *corev1.Secret
	Cluster    *hyperv1.HostedCluster
	NodePools  []*hyperv1.NodePool
}

func (o *ExampleResources) AsObjects() []crclient.Object {
	objects := []crclient.Object{
		o.Namespace,
		o.PullSecret,
		o.Cluster,
	}
	objects = append(objects, o.Resources...)
	if o.SSHKey != nil {
		objects = append(objects, o.SSHKey)
	}
	for _, nodePool := range o.NodePools {
		objects = append(objects, nodePool)
	}
	return objects
}

type ExampleOptions struct {
	Namespace                        string
	Name                             string
	ReleaseImage                     string
	PullSecret                       []byte
	IssuerURL                        string
	SSHPublicKey                     []byte
	SSHPrivateKey                    []byte
	NodePoolReplicas                 int32
	InfraID                          string
	ComputeCIDR                      string
	ServiceCIDR                      string
	PodCIDR                          string
	BaseDomain                       string
	PublicZoneID                     string
	PrivateZoneID                    string
	Annotations                      map[string]string
	FIPS                             bool
	AutoRepair                       bool
	EtcdStorageClass                 string
	AWS                              *ExampleAWSOptions
	None                             *ExampleNoneOptions
	Agent                            *ExampleAgentOptions
	Kubevirt                         *ExampleKubevirtOptions
	Azure                            *ExampleAzureOptions
	NetworkType                      hyperv1.NetworkType
	ControlPlaneAvailabilityPolicy   hyperv1.AvailabilityPolicy
	InfrastructureAvailabilityPolicy hyperv1.AvailabilityPolicy
}

type ExampleNoneOptions struct {
	APIServerAddress string
}

type ExampleAgentOptions struct {
	APIServerAddress string
	AgentNamespace   string
}

type ExampleKubevirtOptions struct {
	APIServerAddress string
	Memory           string
	Cores            uint32
	Image            string
}

type ExampleAWSOptionsZones struct {
	Name     string
	SubnetID *string
}

type ExampleAWSOptions struct {
	Region                      string
	Zones                       []ExampleAWSOptionsZones
	VPCID                       string
	SecurityGroupID             string
	InstanceProfile             string
	InstanceType                string
	Roles                       []hyperv1.AWSRoleCredentials
	KubeCloudControllerRoleARN  string
	NodePoolManagementRoleARN   string
	ControlPlaneOperatorRoleARN string
	RootVolumeSize              int64
	RootVolumeType              string
	RootVolumeIOPS              int64
	ResourceTags                []hyperv1.AWSResourceTag
	EndpointAccess              string
}

type ExampleAzureOptions struct {
	Creds             AzureCreds
	Location          string
	ResourceGroupName string
	VnetName          string
	VnetID            string
	BootImageID       string
	MachineIdentityID string
	InstanceType      string
	SecurityGroupName string
}

// TODO: This format is made up by using the env var keys as keys.
// Is there any kind of official file format for this?
type AzureCreds struct {
	SubscriptionID string `json:"AZURE_SUBSCRIPTION_ID"`
	TenantID       string `json:"AZURE_TENANT_ID"`
	ClientID       string `json:"AZURE_CLIENT_ID"`
	ClientSecret   string `json:"AZURE_CLIENT_SECRET"`
}

func (o ExampleOptions) Resources() *ExampleResources {
	namespace := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Namespace",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: o.Namespace,
		},
	}

	pullSecret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      o.Name + "-pull-secret",
		},
		Data: map[string][]byte{
			".dockerconfigjson": o.PullSecret,
		},
	}

	var sshKeySecret *corev1.Secret
	var sshKeyReference corev1.LocalObjectReference
	if len(o.SSHPublicKey) > 0 {
		sshKeySecret = &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: corev1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace.Name,
				Name:      o.Name + "-ssh-key",
			},
			Data: map[string][]byte{
				"id_rsa.pub": o.SSHPublicKey,
			},
		}
		if len(o.SSHPrivateKey) > 0 {
			sshKeySecret.Data["id_rsa"] = o.SSHPrivateKey
		}
		sshKeyReference = corev1.LocalObjectReference{Name: sshKeySecret.Name}
	}

	var platformSpec hyperv1.PlatformSpec
	var resources []crclient.Object
	var services []hyperv1.ServicePublishingStrategyMapping

	switch {
	case o.AWS != nil:
		buildAWSCreds := func(name, arn string) *corev1.Secret {
			return &corev1.Secret{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Secret",
					APIVersion: corev1.SchemeGroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace.Name,
					Name:      name,
				},
				Data: map[string][]byte{
					"credentials": []byte(fmt.Sprintf(`[default]
role_arn = %s
web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
`, arn)),
				},
			}
		}
		awsResources := &ExampleAWSResources{
			buildAWSCreds(o.Name+"-cloud-ctrl-creds", o.AWS.KubeCloudControllerRoleARN),
			buildAWSCreds(o.Name+"-node-mgmt-creds", o.AWS.NodePoolManagementRoleARN),
			buildAWSCreds(o.Name+"-cpo-creds", o.AWS.ControlPlaneOperatorRoleARN),
		}
		resources = awsResources.AsObjects()
		platformSpec = hyperv1.PlatformSpec{
			Type: hyperv1.AWSPlatform,
			AWS: &hyperv1.AWSPlatformSpec{
				Region: o.AWS.Region,
				Roles:  o.AWS.Roles,
				CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
					VPC: o.AWS.VPCID,
					Subnet: &hyperv1.AWSResourceReference{
						ID: o.AWS.Zones[0].SubnetID,
					},
					Zone: o.AWS.Zones[0].Name,
				},
				KubeCloudControllerCreds:  corev1.LocalObjectReference{Name: awsResources.KubeCloudControllerAWSCreds.Name},
				NodePoolManagementCreds:   corev1.LocalObjectReference{Name: awsResources.NodePoolManagementAWSCreds.Name},
				ControlPlaneOperatorCreds: corev1.LocalObjectReference{Name: awsResources.ControlPlaneOperatorAWSCreds.Name},
				ResourceTags:              o.AWS.ResourceTags,
				EndpointAccess:            hyperv1.AWSEndpointAccessType(o.AWS.EndpointAccess),
			},
		}
		services = []hyperv1.ServicePublishingStrategyMapping{
			{
				Service: hyperv1.APIServer,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type: hyperv1.LoadBalancer,
				},
			},
			{
				Service: hyperv1.OAuthServer,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type: hyperv1.Route,
				},
			},
			{
				Service: hyperv1.OIDC,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type: hyperv1.S3,
				},
			},
			{
				Service: hyperv1.Konnectivity,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type: hyperv1.Route,
				},
			},
			{
				Service: hyperv1.Ignition,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type: hyperv1.Route,
				},
			},
		}
	case o.None != nil:
		platformSpec = hyperv1.PlatformSpec{
			Type: hyperv1.NonePlatform,
		}
		services = o.getServicePublishingStrategyMappingByAPIServerAddress(o.None.APIServerAddress)
	case o.Agent != nil:
		platformSpec = hyperv1.PlatformSpec{
			Type: hyperv1.AgentPlatform,
			Agent: &hyperv1.AgentPlatformSpec{
				AgentNamespace: o.Agent.AgentNamespace,
			},
		}
		services = o.getServicePublishingStrategyMappingByAPIServerAddress(o.Agent.APIServerAddress)
	case o.Kubevirt != nil:
		platformSpec = hyperv1.PlatformSpec{
			Type: hyperv1.KubevirtPlatform,
		}
		services = o.getServicePublishingStrategyMappingByAPIServerAddress(o.Kubevirt.APIServerAddress)
	case o.Azure != nil:
		credentialSecret := &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Secret",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      o.Name + "-cloud-credentials",
				Namespace: namespace.Name,
			},
			Data: map[string][]byte{
				"AZURE_SUBSCRIPTION_ID": []byte(o.Azure.Creds.SubscriptionID),
				"AZURE_TENANT_ID":       []byte(o.Azure.Creds.TenantID),
				"AZURE_CLIENT_ID":       []byte(o.Azure.Creds.ClientID),
				"AZURE_CLIENT_SECRET":   []byte(o.Azure.Creds.ClientSecret),
			},
		}
		resources = append(resources, credentialSecret)

		platformSpec = hyperv1.PlatformSpec{
			Type: hyperv1.AzurePlatform,
			Azure: &hyperv1.AzurePlatformSpec{
				Credentials:       corev1.LocalObjectReference{Name: credentialSecret.Name},
				Location:          o.Azure.Location,
				ResourceGroupName: o.Azure.ResourceGroupName,
				VnetName:          o.Azure.VnetName,
				VnetID:            o.Azure.VnetID,
				SubscriptionID:    o.Azure.Creds.SubscriptionID,
				MachineIdentityID: o.Azure.MachineIdentityID,
				SecurityGroupName: o.Azure.SecurityGroupName,
			},
		}
		services = []hyperv1.ServicePublishingStrategyMapping{
			{
				Service: hyperv1.APIServer,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type: hyperv1.LoadBalancer,
				},
			},
			{
				Service: hyperv1.OAuthServer,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type: hyperv1.Route,
				},
			},
			{
				Service: hyperv1.Konnectivity,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type: hyperv1.Route,
				},
			},
			{
				Service: hyperv1.Ignition,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type: hyperv1.Route,
				},
			},
		}

	default:
		panic("no platform specified")
	}

	var etcdStorgageClass *string = nil
	if len(o.EtcdStorageClass) > 0 {
		etcdStorgageClass = pointer.StringPtr(o.EtcdStorageClass)
	}
	cluster := &hyperv1.HostedCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HostedCluster",
			APIVersion: hyperv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   namespace.Name,
			Name:        o.Name,
			Annotations: o.Annotations,
		},
		Spec: hyperv1.HostedClusterSpec{
			Release: hyperv1.Release{
				Image: o.ReleaseImage,
			},
			Etcd: hyperv1.EtcdSpec{
				ManagementType: hyperv1.Managed,
				Managed: &hyperv1.ManagedEtcdSpec{
					Storage: hyperv1.ManagedEtcdStorageSpec{
						Type: hyperv1.PersistentVolumeEtcdStorage,
						PersistentVolume: &hyperv1.PersistentVolumeEtcdStorageSpec{
							StorageClassName: etcdStorgageClass,
							Size:             &hyperv1.DefaultPersistentVolumeEtcdStorageSize,
						},
					},
				},
			},
			Networking: hyperv1.ClusterNetworking{
				ServiceCIDR: o.ServiceCIDR,
				PodCIDR:     o.PodCIDR,
				MachineCIDR: o.ComputeCIDR,
				NetworkType: o.NetworkType,
			},
			Services:   services,
			InfraID:    o.InfraID,
			PullSecret: corev1.LocalObjectReference{Name: pullSecret.Name},
			IssuerURL:  o.IssuerURL,
			SSHKey:     sshKeyReference,
			FIPS:       o.FIPS,
			DNS: hyperv1.DNSSpec{
				BaseDomain:    o.BaseDomain,
				PublicZoneID:  o.PublicZoneID,
				PrivateZoneID: o.PrivateZoneID,
			},
			ControllerAvailabilityPolicy:     o.ControlPlaneAvailabilityPolicy,
			InfrastructureAvailabilityPolicy: o.InfrastructureAvailabilityPolicy,
			Platform:                         platformSpec,
		},
	}

	if o.NodePoolReplicas <= -1 {
		return &ExampleResources{
			Namespace:  namespace,
			PullSecret: pullSecret,
			Resources:  resources,
			SSHKey:     sshKeySecret,
			Cluster:    cluster,
			NodePools:  []*hyperv1.NodePool{},
		}
	}

	defaultNodePool := func(name string) *hyperv1.NodePool {
		return &hyperv1.NodePool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "NodePool",
				APIVersion: hyperv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace.Name,
				Name:      name,
			},
			Spec: hyperv1.NodePoolSpec{
				Management: hyperv1.NodePoolManagement{
					AutoRepair:  o.AutoRepair,
					UpgradeType: hyperv1.UpgradeTypeReplace,
				},
				NodeCount:   &o.NodePoolReplicas,
				ClusterName: o.Name,
				Release: hyperv1.Release{
					Image: o.ReleaseImage,
				},
				Platform: hyperv1.NodePoolPlatform{
					Type: cluster.Spec.Platform.Type,
				},
			},
		}
	}

	var nodePools []*hyperv1.NodePool
	switch cluster.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		for _, zone := range o.AWS.Zones {
			nodePool := defaultNodePool(fmt.Sprintf("%s-%s", cluster.Name, zone.Name))
			nodePool.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{
				InstanceType:    o.AWS.InstanceType,
				InstanceProfile: o.AWS.InstanceProfile,
				Subnet: &hyperv1.AWSResourceReference{
					ID: zone.SubnetID,
				},
				SecurityGroups: []hyperv1.AWSResourceReference{
					{
						ID: &o.AWS.SecurityGroupID,
					},
				},
				RootVolume: &hyperv1.Volume{
					Size: o.AWS.RootVolumeSize,
					Type: o.AWS.RootVolumeType,
					IOPS: o.AWS.RootVolumeIOPS,
				},
			}
			nodePools = append(nodePools, nodePool)
		}
	case hyperv1.KubevirtPlatform:
		nodePool := defaultNodePool(cluster.Name)
		runAlways := kubevirtv1.RunStrategyAlways
		guestQuantity := apiresource.MustParse(o.Kubevirt.Memory)
		nodePool.Spec.Platform.Kubevirt = &hyperv1.KubevirtNodePoolPlatform{
			NodeTemplate: &capikubevirt.VirtualMachineTemplateSpec{
				Spec: kubevirtv1.VirtualMachineSpec{
					RunStrategy: &runAlways,
					Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
						Spec: kubevirtv1.VirtualMachineInstanceSpec{
							Domain: kubevirtv1.DomainSpec{
								CPU:    &kubevirtv1.CPU{Cores: o.Kubevirt.Cores},
								Memory: &kubevirtv1.Memory{Guest: &guestQuantity},
								Devices: kubevirtv1.Devices{
									Disks: []kubevirtv1.Disk{
										{
											Name: "containervolume",
											DiskDevice: kubevirtv1.DiskDevice{
												Disk: &kubevirtv1.DiskTarget{
													Bus: "virtio",
												},
											},
										},
									},
								},
							},
							Volumes: []kubevirtv1.Volume{
								{
									Name: "containervolume",
									VolumeSource: kubevirtv1.VolumeSource{
										ContainerDisk: &kubevirtv1.ContainerDiskSource{
											Image: o.Kubevirt.Image,
										},
									},
								},
							},
						},
					},
				},
			},
		}
		nodePools = append(nodePools, nodePool)
	case hyperv1.NonePlatform, hyperv1.AgentPlatform:
		nodePools = append(nodePools, defaultNodePool(cluster.Name))
	case hyperv1.AzurePlatform:
		nodePool := defaultNodePool(cluster.Name)
		nodePool.Spec.Platform.Azure = &hyperv1.AzureNodePoolPlatform{
			VMSize:  o.Azure.InstanceType,
			ImageID: o.Azure.BootImageID,
		}
		nodePools = append(nodePools, nodePool)
	default:
		panic("Unsupported platform")
	}

	return &ExampleResources{
		Namespace:  namespace,
		PullSecret: pullSecret,
		Resources:  resources,
		SSHKey:     sshKeySecret,
		Cluster:    cluster,
		NodePools:  nodePools,
	}
}

func (o ExampleOptions) getServicePublishingStrategyMappingByAPIServerAddress(APIServerAddress string) []hyperv1.ServicePublishingStrategyMapping {
	return []hyperv1.ServicePublishingStrategyMapping{
		{
			Service: hyperv1.APIServer,
			ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type:     hyperv1.NodePort,
				NodePort: &hyperv1.NodePortPublishingStrategy{Address: APIServerAddress},
			},
		},
		{
			Service: hyperv1.OAuthServer,
			ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type:     hyperv1.NodePort,
				NodePort: &hyperv1.NodePortPublishingStrategy{Address: APIServerAddress},
			},
		},
		{
			Service: hyperv1.OIDC,
			ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type:     hyperv1.None,
				NodePort: &hyperv1.NodePortPublishingStrategy{Address: APIServerAddress},
			},
		},
		{
			Service: hyperv1.Konnectivity,
			ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type:     hyperv1.NodePort,
				NodePort: &hyperv1.NodePortPublishingStrategy{Address: APIServerAddress},
			},
		},
		{
			Service: hyperv1.Ignition,
			ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type:     hyperv1.NodePort,
				NodePort: &hyperv1.NodePortPublishingStrategy{Address: APIServerAddress},
			},
		},
	}
}
