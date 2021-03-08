// +build e2e

package e2e

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/clientcmd"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperapi "github.com/openshift/hypershift/api"
	apifixtures "github.com/openshift/hypershift/api/fixtures"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	awsinfra "github.com/openshift/hypershift/cmd/infra/aws"
	"github.com/openshift/hypershift/version"

	configv1 "github.com/openshift/api/config/v1"
)

// QuickStartOptions are the raw user input used to construct the test input.
type QuickStartOptions struct {
	AWSCredentialsFile string
	PullSecretFile     string
	SSHKeyFile         string
	ReleaseImage       string
	InfraFile          string
	InstanceProfile    string
	InstanceType       string
}

var quickStartOptions QuickStartOptions

func init() {
	flag.StringVar(&quickStartOptions.AWSCredentialsFile, "e2e.quick-start.aws-credentials-file", "", "path to AWS credentials")
	flag.StringVar(&quickStartOptions.PullSecretFile, "e2e.quick-start.pull-secret-file", "", "path to pull secret")
	flag.StringVar(&quickStartOptions.SSHKeyFile, "e2e.quick-start.ssh-key-file", filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa.pub"), "path to SSH public key")
	flag.StringVar(&quickStartOptions.ReleaseImage, "e2e.quick-start.release-image", "", "OCP release image to test")
	flag.StringVar(&quickStartOptions.InfraFile, "e2e.quick-start.infra-json", "", "File containing infra description")
	flag.StringVar(&quickStartOptions.InstanceProfile, "e2e.quick-start.instance-profile", "hypershift-worker-profile", "Name of instance profile to use")
	flag.StringVar(&quickStartOptions.InstanceType, "e2e.quick-start.instance-type", "m4.large", "Instance type to use in tests")
}

// QuickStartInput are the validated options for running the test.
type QuickStartInput struct {
	Client          crclient.Client
	ReleaseImage    string
	AWSCredentials  []byte
	PullSecret      []byte
	SSHKey          []byte
	Infra           awsinfra.CreateInfraOutput
	InstanceProfile string
	InstanceType    string
}

// GetContext builds a QuickStartInput from the options.
func (o QuickStartOptions) GetInput() (*QuickStartInput, error) {
	input := &QuickStartInput{}

	var err error
	input.PullSecret, err = ioutil.ReadFile(o.PullSecretFile)
	if err != nil {
		return nil, fmt.Errorf("couldn't read pull secret file %q: %w", o.PullSecretFile, err)
	}
	if len(input.PullSecret) == 0 {
		return nil, fmt.Errorf("pull secret is required")
	}

	input.AWSCredentials, err = ioutil.ReadFile(o.AWSCredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("couldn't read aws credentials file %q: %w", o.AWSCredentialsFile, err)
	}
	if len(input.AWSCredentials) == 0 {
		return nil, fmt.Errorf("AWS credentials are required")
	}

	input.SSHKey, err = ioutil.ReadFile(o.SSHKeyFile)
	if err != nil {
		return nil, fmt.Errorf("couldn't read SSH key file %q: %w", o.SSHKeyFile, err)
	}
	if len(input.SSHKey) == 0 {
		return nil, fmt.Errorf("SSH key is required")
	}

	infraBytes, err := ioutil.ReadFile(o.InfraFile)
	if err != nil {
		return nil, fmt.Errorf("couldn't read infra file %q: %w", o.InfraFile, err)
	}
	if err = json.Unmarshal(infraBytes, &input.Infra); err != nil {
		return nil, fmt.Errorf("could parse infra file %q: %w", o.InfraFile, err)
	}

	if len(o.ReleaseImage) == 0 {
		defaultVersion, err := version.LookupDefaultOCPVersion()
		if err != nil {
			return nil, fmt.Errorf("couldn't look up default OCP version: %w", err)
		}
		input.ReleaseImage = defaultVersion.PullSpec
	}
	if len(input.ReleaseImage) == 0 {
		return nil, fmt.Errorf("release image is required")
	}

	input.Client, err = crclient.New(ctrl.GetConfigOrDie(), crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create kube client: %w", err)
	}

	input.InstanceProfile = o.InstanceProfile
	input.InstanceType = o.InstanceType

	return input, nil
}

// TestQuickStart implements a test that mimics the operation described in the
// HyperShift quick start (creating a basic guest cluster).
//
// This test is meant to provide a first, fast signal to detect regression; it
// is recommended to use it as a PR blocker test.
func TestQuickStart(t *testing.T) {
	ctx, cancel := context.WithCancel(GlobalTestContext)
	defer cancel()

	input, err := quickStartOptions.GetInput()
	if err != nil {
		t.Fatalf("failed to create test context: %s", err)
	}

	t.Logf("Testing OCP release image %s", input.ReleaseImage)

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "e2e-",
		},
	}
	err = input.Client.Create(ctx, namespace)
	if err != nil {
		t.Fatalf("failed to create namespace: %s", err)
	}
	if len(namespace.Name) == 0 {
		t.Fatalf("generated namespace has no name")
	}
	t.Logf("Created test namespace %s", namespace.Name)

	// Clean up the namespace after the test
	defer func() {
		cleanupCtx := context.Background()
		err := input.Client.Delete(cleanupCtx, namespace, &crclient.DeleteOptions{})
		if err != nil {
			t.Fatalf("failed to delete namespace %q: %s", namespace.Name, err)
		}
		t.Logf("Waiting for the test namespace %q to be deleted", namespace.Name)
		err = wait.PollInfinite(1*time.Second, func() (done bool, err error) {
			latestNamespace := &corev1.Namespace{}
			key := crclient.ObjectKey{
				Name: namespace.Name,
			}
			if err := input.Client.Get(cleanupCtx, key, latestNamespace); err != nil {
				if errors.IsNotFound(err) {
					return true, nil
				}
				t.Logf("failed to get namespace %q: %s", latestNamespace.Name, err)
				return false, nil
			}
			return false, nil
		})
		if err != nil {
			t.Fatalf("failed to clean up namespace %q: %s", namespace.Name, err)
		}
	}()

	example := apifixtures.ExampleOptions{
		Namespace:                  namespace.Name,
		Name:                       "example-" + namespace.Name,
		ReleaseImage:               input.ReleaseImage,
		PullSecret:                 input.PullSecret,
		AWSCredentials:             input.AWSCredentials,
		SSHKey:                     input.SSHKey,
		NodePoolReplicas:           2,
		InfraID:                    input.Infra.InfraID,
		ApiserverSecurePort:        6443,
		ApiserverAdvertisedAddress: "172.20.0.1",
		ServiceCIDR:                "172.31.0.0/16",
		PodCIDR:                    "10.132.0.0/14",
		ComputeCIDR:                input.Infra.ComputeCIDR,
		AWS: apifixtures.ExampleAWSOptions{
			Region:          input.Infra.Region,
			Zone:            input.Infra.Zone,
			VPCID:           input.Infra.VPCID,
			SubnetID:        input.Infra.PrivateSubnetID,
			SecurityGroupID: input.Infra.SecurityGroupID,
			InstanceProfile: input.InstanceProfile,
			InstanceType:    input.InstanceType,
		},
	}.Resources()

	err = input.Client.Create(ctx, example.PullSecret)
	if err != nil {
		t.Fatalf("couldn't create pull secret: %s", err)
	}
	t.Logf("Created test pull secret %s", example.PullSecret.Name)

	err = input.Client.Create(ctx, example.AWSCredentials)
	if err != nil {
		t.Fatalf("couldn't create aws credentials secret: %s", err)
	}
	t.Logf("Created test aws credentials secret %s", example.AWSCredentials.Name)

	err = input.Client.Create(ctx, example.SSHKey)
	if err != nil {
		t.Fatalf("couldn't create ssh key secret: %s", err)
	}
	t.Logf("Created test ssh key secret %s", example.SSHKey.Name)

	err = input.Client.Create(ctx, example.Cluster)
	if err != nil {
		t.Fatalf("couldn't create cluster: %s", err)
	}
	t.Logf("Created test hostedcluster %s", example.Cluster.Name)

	// Perform some very basic assertions about the guest cluster
	t.Logf("Ensuring the guest cluster exposes a valid kubeconfig")

	t.Logf("Waiting for guest kubeconfig to become available")

	var guestKubeConfigSecret corev1.Secret
	waitForKubeConfigCtx, _ := context.WithTimeout(ctx, 5*time.Minute)
	err = wait.PollUntil(1*time.Second, func() (done bool, err error) {
		var currentCluster hyperv1.HostedCluster
		err = input.Client.Get(waitForKubeConfigCtx, crclient.ObjectKeyFromObject(example.Cluster), &currentCluster)
		if err != nil {
			return false, nil
		}
		if currentCluster.Status.KubeConfig == nil {
			return false, nil
		}
		key := crclient.ObjectKey{
			Namespace: currentCluster.Namespace,
			Name:      currentCluster.Status.KubeConfig.Name,
		}
		if err := input.Client.Get(waitForKubeConfigCtx, key, &guestKubeConfigSecret); err != nil {
			return false, nil
		}
		return true, nil
	}, waitForKubeConfigCtx.Done())
	if err != nil {
		t.Fatalf("guest kubeconfig didn't become available")
	}

	// TODO: this key should probably be published or an API constant
	guestKubeConfigSecretData, hasData := guestKubeConfigSecret.Data["kubeconfig"]
	if !hasData {
		t.Fatalf("guest kubeconfig secret is missing kubeconfig key")
	}

	guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
	if err != nil {
		t.Fatalf("couldn't load guest kubeconfig: %s", err)
	}

	t.Logf("Establishing a connection to the guest apiserver")
	var guestClient crclient.Client
	waitForGuestClientCtx, _ := context.WithTimeout(ctx, 5*time.Minute)
	err = wait.PollUntil(5*time.Second, func() (done bool, err error) {
		kubeClient, err := crclient.New(guestConfig, crclient.Options{Scheme: hyperapi.Scheme})
		if err != nil {
			return false, nil
		}
		guestClient = kubeClient
		return true, nil
	}, waitForGuestClientCtx.Done())
	if err != nil {
		t.Fatalf("failed to establish a connection to the guest apiserver: %s", err)
	}

	t.Logf("Ensuring guest nodes become ready")
	nodes := &corev1.NodeList{}
	waitForNodesCtx, _ := context.WithTimeout(ctx, 10*time.Minute)
	err = wait.PollUntil(5*time.Second, func() (done bool, err error) {
		err = guestClient.List(waitForNodesCtx, nodes)
		if err != nil {
			return false, nil
		}
		if len(nodes.Items) == 0 {
			return false, nil
		}
		var readyNodes []string
		for _, node := range nodes.Items {
			for _, cond := range node.Status.Conditions {
				if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
					readyNodes = append(readyNodes, node.Name)
				}
			}
		}
		if len(readyNodes) != example.Cluster.Spec.InitialComputeReplicas {
			return false, nil
		}
		t.Logf("found %d ready nodes", len(nodes.Items))
		return true, nil
	}, waitForNodesCtx.Done())
	if err != nil {
		t.Fatalf("failed to ensure guest nodes became ready: %s", err)
	}

	clusterOperators := &configv1.ClusterOperatorList{}
	waitForClusterOperatorsReadyCtx, _ := context.WithTimeout(ctx, 10*time.Minute)
	err = wait.PollUntil(10*time.Second, func() (done bool, err error) {
		err = guestClient.List(waitForClusterOperatorsReadyCtx, clusterOperators)
		if err != nil {
			t.Logf("failed to list cluster operators: %v", err)
			return false, nil
		}
		if len(clusterOperators.Items) == 0 {
			return false, nil
		}
		ready := true
		for _, clusterOperator := range clusterOperators.Items {
			available := false
			degraded := true
			for _, cond := range clusterOperator.Status.Conditions {
				if cond.Type == configv1.OperatorAvailable && cond.Status == configv1.ConditionTrue {
					available = true
				}
				if cond.Type == configv1.OperatorDegraded && cond.Status == configv1.ConditionFalse {
					degraded = false
				}
			}
			if !available || degraded {
				ready = false
				break
			}
		}
		if !ready {
			return false, nil
		}
		t.Logf("guest cluster operators are ready")
		return true, nil
	}, waitForClusterOperatorsReadyCtx.Done())
	if err != nil {
		t.Fatalf("failed to ensure guest cluster operators became ready: %v", err)
	}
}
