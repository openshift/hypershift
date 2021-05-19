package fixtures

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type ExampleResources struct {
	Namespace                   *corev1.Namespace
	PullSecret                  *corev1.Secret
	KubeCloudControllerAWSCreds *corev1.Secret
	NodePoolManagementAWSCreds  *corev1.Secret
	SigningKey                  *corev1.Secret
	SSHKey                      *corev1.Secret
	Cluster                     *hyperv1.HostedCluster
	NodePool                    *hyperv1.NodePool
}

func (o *ExampleResources) AsObjects() []crclient.Object {
	objects := []crclient.Object{
		o.Namespace,
		o.PullSecret,
		o.SigningKey,
		o.KubeCloudControllerAWSCreds,
		o.NodePoolManagementAWSCreds,
		o.Cluster,
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
	Namespace        string
	Name             string
	ReleaseImage     string
	PullSecret       []byte
	SigningKey       []byte
	IssuerURL        string
	SSHKey           []byte
	NodePoolReplicas int32
	InfraID          string
	ComputeCIDR      string
	BaseDomain       string
	PublicZoneID     string
	PrivateZoneID    string
	Annotations      map[string]string

	AWS ExampleAWSOptions
}

type ExampleAWSOptions struct {
	Region                                 string
	Zone                                   string
	VPCID                                  string
	SubnetID                               string
	SecurityGroupID                        string
	InstanceProfile                        string
	InstanceType                           string
	Roles                                  []hyperv1.AWSRoleCredentials
	KubeCloudControllerUserAccessKeyID     string
	KubeCloudControllerUserAccessKeySecret string
	NodePoolManagementUserAccessKeyID      string
	NodePoolManagementUserAccessKeySecret  string
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

	buildAWSCreds := func(name, id, key string) *corev1.Secret {
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
aws_access_key_id = %s
aws_secret_access_key = %s
`, id, key)),
			},
		}
	}
	kubeCloudControllerCredsSecret := buildAWSCreds(
		o.Name+"-cloud-ctrl-creds",
		o.AWS.KubeCloudControllerUserAccessKeyID,
		o.AWS.KubeCloudControllerUserAccessKeySecret)
	nodePoolManagementCredsSecret := buildAWSCreds(
		o.Name+"-node-mgmt-creds",
		o.AWS.NodePoolManagementUserAccessKeyID,
		o.AWS.NodePoolManagementUserAccessKeySecret)

	signingKeySecret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      o.Name + "-signing-key",
		},
		Data: map[string][]byte{
			"key": o.SigningKey,
		},
	}

	var sshKeySecret *corev1.Secret
	var sshKeyReference corev1.LocalObjectReference
	if len(o.SSHKey) > 0 {
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
				"id_rsa.pub": o.SSHKey,
			},
		}
		sshKeyReference = corev1.LocalObjectReference{Name: sshKeySecret.Name}
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
			Networking: hyperv1.ClusterNetworking{
				ServiceCIDR: "172.31.0.0/16",
				PodCIDR:     "10.132.0.0/14",
				MachineCIDR: o.ComputeCIDR,
			},
			Services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service: hyperv1.APIServer,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type: hyperv1.LoadBalancer,
					},
				},
				{
					Service: hyperv1.VPN,
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
				hyperv1.ServicePublishingStrategyMapping{
					Service: hyperv1.OIDC,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type: hyperv1.Route,
					},
				},
			},
			InfraID:    o.InfraID,
			PullSecret: corev1.LocalObjectReference{Name: pullSecret.Name},
			SigningKey: corev1.LocalObjectReference{Name: signingKeySecret.Name},
			IssuerURL:  o.IssuerURL,
			SSHKey:     sshKeyReference,
			DNS: hyperv1.DNSSpec{
				BaseDomain:    o.BaseDomain,
				PublicZoneID:  o.PublicZoneID,
				PrivateZoneID: o.PrivateZoneID,
			},
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					Region: o.AWS.Region,
					Roles:  o.AWS.Roles,
					VPC:    o.AWS.VPCID,
					NodePoolDefaults: &hyperv1.AWSNodePoolPlatform{
						InstanceType:    o.AWS.InstanceType,
						InstanceProfile: o.AWS.InstanceProfile,
						Subnet: &hyperv1.AWSResourceReference{
							ID: &o.AWS.SubnetID,
						},
						SecurityGroups: []hyperv1.AWSResourceReference{
							{ID: &o.AWS.SecurityGroupID},
						},
						Zone: o.AWS.Zone,
					},
					KubeCloudControllerCreds: corev1.LocalObjectReference{Name: kubeCloudControllerCredsSecret.Name},
					NodePoolManagementCreds:  corev1.LocalObjectReference{Name: nodePoolManagementCredsSecret.Name},
				},
			},
		},
	}

	var nodePool *hyperv1.NodePool
	if o.NodePoolReplicas > 0 {
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
				IgnitionService: hyperv1.ServicePublishingStrategy{
					Type: hyperv1.Route,
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
			nodePool.Spec.Platform.AWS = cluster.Spec.Platform.AWS.NodePoolDefaults
		}
	}

	return &ExampleResources{
		Namespace:                   namespace,
		PullSecret:                  pullSecret,
		KubeCloudControllerAWSCreds: kubeCloudControllerCredsSecret,
		NodePoolManagementAWSCreds:  nodePoolManagementCredsSecret,
		SigningKey:                  signingKeySecret,
		SSHKey:                      sshKeySecret,
		Cluster:                     cluster,
		NodePool:                    nodePool,
	}
}
