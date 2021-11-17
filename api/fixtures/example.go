package fixtures

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type ExampleResources struct {
	Namespace    *corev1.Namespace
	PullSecret   *corev1.Secret
	AWSResources *ExampleAWSResources
	SSHKey       *corev1.Secret
	Cluster      *hyperv1.HostedCluster
	NodePool     *hyperv1.NodePool
}

type ExampleAWSResources struct {
	KubeCloudControllerAWSCreds *corev1.Secret
	NodePoolManagementAWSCreds  *corev1.Secret
}

func (o *ExampleResources) AsObjects() []crclient.Object {
	objects := []crclient.Object{
		o.Namespace,
		o.PullSecret,
		o.Cluster,
	}
	if o.AWSResources != nil {
		objects = append(objects, o.AWSResources.KubeCloudControllerAWSCreds)
		objects = append(objects, o.AWSResources.NodePoolManagementAWSCreds)
	}
	if o.SSHKey != nil {
		objects = append(objects, o.SSHKey)
	}
	if o.NodePool != nil {
		objects = append(objects, o.NodePool)
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
	BaseDomain                       string
	PublicZoneID                     string
	PrivateZoneID                    string
	Annotations                      map[string]string
	FIPS                             bool
	AutoRepair                       bool
	EtcdStorageClass                 string
	AWS                              *ExampleAWSOptions
	None                             *ExampleNoneOptions
	NetworkType                      hyperv1.NetworkType
	ControlPlaneAvailabilityPolicy   hyperv1.AvailabilityPolicy
	InfrastructureAvailabilityPolicy hyperv1.AvailabilityPolicy
}

type ExampleNoneOptions struct {
	APIServerAddress string
}

type ExampleAWSOptions struct {
	Region                     string
	Zone                       string
	VPCID                      string
	SubnetID                   string
	SecurityGroupID            string
	InstanceProfile            string
	InstanceType               string
	Roles                      []hyperv1.AWSRoleCredentials
	KubeCloudControllerRoleARN string
	NodePoolManagementRoleARN  string
	RootVolumeSize             int64
	RootVolumeType             string
	RootVolumeIOPS             int64
	ResourceTags               []hyperv1.AWSResourceTag
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
	var exampleAWSResources *ExampleAWSResources
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
		exampleAWSResources = &ExampleAWSResources{}
		exampleAWSResources.KubeCloudControllerAWSCreds = buildAWSCreds(
			o.Name+"-cloud-ctrl-creds",
			o.AWS.KubeCloudControllerRoleARN)
		exampleAWSResources.NodePoolManagementAWSCreds = buildAWSCreds(
			o.Name+"-node-mgmt-creds",
			o.AWS.NodePoolManagementRoleARN)
		platformSpec = hyperv1.PlatformSpec{
			Type: hyperv1.AWSPlatform,
			AWS: &hyperv1.AWSPlatformSpec{
				Region: o.AWS.Region,
				Roles:  o.AWS.Roles,
				CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
					VPC: o.AWS.VPCID,
					Subnet: &hyperv1.AWSResourceReference{
						ID: &o.AWS.SubnetID,
					},
					Zone: o.AWS.Zone,
				},
				KubeCloudControllerCreds: corev1.LocalObjectReference{Name: exampleAWSResources.KubeCloudControllerAWSCreds.Name},
				NodePoolManagementCreds:  corev1.LocalObjectReference{Name: exampleAWSResources.NodePoolManagementAWSCreds.Name},
				ResourceTags:             o.AWS.ResourceTags,
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
		services = []hyperv1.ServicePublishingStrategyMapping{
			{
				Service: hyperv1.APIServer,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type:     hyperv1.NodePort,
					NodePort: &hyperv1.NodePortPublishingStrategy{Address: o.None.APIServerAddress},
				},
			},
			{
				Service: hyperv1.OAuthServer,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type:     hyperv1.NodePort,
					NodePort: &hyperv1.NodePortPublishingStrategy{Address: o.None.APIServerAddress},
				},
			},
			{
				Service: hyperv1.OIDC,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type:     hyperv1.None,
					NodePort: &hyperv1.NodePortPublishingStrategy{Address: o.None.APIServerAddress},
				},
			},
			{
				Service: hyperv1.Konnectivity,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type:     hyperv1.NodePort,
					NodePort: &hyperv1.NodePortPublishingStrategy{Address: o.None.APIServerAddress},
				},
			},
			{
				Service: hyperv1.Ignition,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
					Type:     hyperv1.NodePort,
					NodePort: &hyperv1.NodePortPublishingStrategy{Address: o.None.APIServerAddress},
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
				ServiceCIDR: "172.31.0.0/16",
				PodCIDR:     "10.132.0.0/14",
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

	var nodePool *hyperv1.NodePool
	if o.NodePoolReplicas > -1 {
		nodePool = &hyperv1.NodePool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "NodePool",
				APIVersion: hyperv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace.Name,
				Name:      o.Name,
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

		switch nodePool.Spec.Platform.Type {
		case hyperv1.AWSPlatform:
			nodePool.Spec.Platform.AWS = &hyperv1.AWSNodePoolPlatform{
				InstanceType:    o.AWS.InstanceType,
				InstanceProfile: o.AWS.InstanceProfile,
				Subnet: &hyperv1.AWSResourceReference{
					ID: &o.AWS.SubnetID,
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
		}
	}

	return &ExampleResources{
		Namespace:    namespace,
		PullSecret:   pullSecret,
		AWSResources: exampleAWSResources,
		SSHKey:       sshKeySecret,
		Cluster:      cluster,
		NodePool:     nodePool,
	}
}
