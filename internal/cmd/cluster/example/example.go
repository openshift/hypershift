package example

import (
	"fmt"
	"io/ioutil"
	"os"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"
	securityv1 "github.com/openshift/api/security/v1"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/api/v1alpha3"
	capiv1 "sigs.k8s.io/cluster-api/api/v1alpha4"

	hyperv1 "openshift.io/hypershift/api/v1alpha1"
)

var (
	scheme         = runtime.NewScheme()
	yamlSerializer = json.NewSerializerWithOptions(
		json.DefaultMetaFactory, scheme, scheme,
		json.SerializerOptions{Yaml: true, Pretty: true, Strict: true},
	)
)

func init() {
	capiaws.AddToScheme(scheme)
	clientgoscheme.AddToScheme(scheme)
	hyperv1.AddToScheme(scheme)
	capiv1.AddToScheme(scheme)
	configv1.AddToScheme(scheme)
	securityv1.AddToScheme(scheme)
	operatorv1.AddToScheme(scheme)
	routev1.AddToScheme(scheme)
	rbacv1.AddToScheme(scheme)
	corev1.AddToScheme(scheme)
	apiextensionsv1.AddToScheme(scheme)
}

type Options struct {
	Namespace    string
	Name         string
	ReleaseImage string

	PullSecret []byte
	AWSCreds   []byte
	SSHKey     []byte
}

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "example",
		Short: "Creates a working example HostedCluster resource",
	}

	var opts Options

	var pullSecretFile string
	var awsCredsFile string
	var sshKeyFile string

	cmd.Flags().StringVar(&opts.Namespace, "namespace", "clusters", "A namespace to contain the generated resources")
	cmd.Flags().StringVar(&opts.Name, "name", "example", "A name for the cluster")
	cmd.Flags().StringVar(&opts.ReleaseImage, "release-image", "quay.io/openshift-release-dev/ocp-release:4.7.0-fc.3-x86_64", "The OCP release image for the cluster")
	cmd.Flags().StringVar(&pullSecretFile, "pull-secret", "", "Path to a pull secret")
	cmd.Flags().StringVar(&awsCredsFile, "aws-creds", "", "Path to an AWS credentials file")
	cmd.Flags().StringVar(&sshKeyFile, "ssh-key", "", "Path to an SSH key file")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		var err error
		opts.PullSecret, err = ioutil.ReadFile(pullSecretFile)
		if err != nil {
			panic(err)
		}
		opts.AWSCreds, err = ioutil.ReadFile(awsCredsFile)
		if err != nil {
			panic(err)
		}
		opts.SSHKey, err = ioutil.ReadFile(sshKeyFile)
		if err != nil {
			panic(err)
		}

		var objects []runtime.Object

		objects = append(objects, clusterManifests(opts)...)

		for _, object := range objects {
			err := yamlSerializer.Encode(object, os.Stdout)
			if err != nil {
				panic(err)
			}
			fmt.Println("---")
		}
	}

	return cmd
}

func clusterManifests(opts Options) []runtime.Object {
	namespace := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Namespace",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: opts.Namespace,
		},
	}

	pullSecret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      opts.Name + "-pull-secret",
		},
		Data: map[string][]byte{
			".dockerconfigjson": opts.PullSecret,
		},
	}

	awsCredsSecret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      opts.Name + "-provider-creds",
		},
		Data: map[string][]byte{
			"credentials": opts.AWSCreds,
		},
	}

	sshKeySecret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      opts.Name + "-ssh-key",
		},
		Data: map[string][]byte{
			"id_rsa.pub": opts.SSHKey,
		},
	}

	cluster := &hyperv1.HostedCluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "HostedCluster",
			APIVersion: hyperv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace.Name,
			Name:      opts.Name,
		},
		Spec: hyperv1.HostedClusterSpec{
			Release: hyperv1.ReleaseSpec{
				Image: opts.ReleaseImage,
			},
			InitialComputeReplicas: 2,
			ServiceCIDR:            "172.31.0.0/16",
			PodCIDR:                "10.132.0.0/14",
			PullSecret:             corev1.LocalObjectReference{Name: pullSecret.Name},
			ProviderCreds:          corev1.LocalObjectReference{Name: awsCredsSecret.Name},
			SSHKey:                 corev1.LocalObjectReference{Name: sshKeySecret.Name},
		},
	}

	return []runtime.Object{
		namespace,
		pullSecret,
		awsCredsSecret,
		sshKeySecret,
		cluster,
	}
}
