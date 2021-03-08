package fixtures

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type ExampleResources struct {
	Namespace      *corev1.Namespace
	PullSecret     *corev1.Secret
	AWSCredentials *corev1.Secret
	SSHKey         *corev1.Secret
	Cluster        *hyperv1.HostedCluster
}

func (o *ExampleResources) AsObjects() []crclient.Object {
	return []crclient.Object{
		o.Namespace,
		o.SSHKey,
		o.PullSecret,
		o.AWSCredentials,
		o.Cluster,
	}
}

type ExampleOptions struct {
	Namespace                              string
	Name                                   string
	ReleaseImage                           string
	PullSecret                             []byte
	AWSCredentials                         []byte
	SSHKey                                 []byte
	NodePoolReplicas                       int
	InfraID                                string
	ComputeCIDR                            string
	ControlPlaneServiceType                string
	ControlPlaneServiceTypeNodePortAddress string

	AWS ExampleAWSOptions
}

type ExampleAWSOptions struct {
	Region          string
	Zone            string
	VPCID           string
	SubnetID        string
	SecurityGroupID string
	InstanceProfile string
	InstanceType    string
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

	awsCredsSecret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      o.Name + "-provider-creds",
		},
		Data: map[string][]byte{
			"credentials": o.AWSCredentials,
		},
	}

	sshKeySecret := &corev1.Secret{
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

	cluster := &hyperv1.HostedCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HostedCluster",
			APIVersion: hyperv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      o.Name,
		},
		Spec: hyperv1.HostedClusterSpec{
			Release: hyperv1.Release{
				Image: o.ReleaseImage,
			},
			InitialComputeReplicas: o.NodePoolReplicas,
			Networking: hyperv1.ClusterNetworking{
				ServiceCIDR: "172.31.0.0/16",
				PodCIDR:     "10.132.0.0/14",
				MachineCIDR: o.ComputeCIDR,
			},
			InfraID:                                o.InfraID,
			PullSecret:                             corev1.LocalObjectReference{Name: pullSecret.Name},
			ProviderCreds:                          corev1.LocalObjectReference{Name: awsCredsSecret.Name},
			SSHKey:                                 corev1.LocalObjectReference{Name: sshKeySecret.Name},
			ControlPlaneServiceType:                o.ControlPlaneServiceType,
			ControlPlaneServiceTypeNodePortAddress: o.ControlPlaneServiceTypeNodePortAddress,
			Platform: hyperv1.PlatformSpec{
				AWS: &hyperv1.AWSPlatformSpec{
					Region: o.AWS.Region,
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
				},
			},
		},
	}

	return &ExampleResources{
		Namespace:      namespace,
		PullSecret:     pullSecret,
		AWSCredentials: awsCredsSecret,
		SSHKey:         sshKeySecret,
		Cluster:        cluster,
	}
}
