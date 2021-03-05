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

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"

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
)

// ControlPlaneUpgradeOptions are the raw user input used to construct the test input.
type ControlPlaneUpgradeOptions struct {
	AWSCredentialsFile string
	PullSecretFile     string
	SSHKeyFile         string
	InfraFile          string
	InstanceProfile    string
	FromReleaseImage   string
	ToReleaseImage     string
}

var controlPlaneUpgradeOptions ControlPlaneUpgradeOptions

func init() {
	flag.StringVar(&controlPlaneUpgradeOptions.AWSCredentialsFile, "e2e.cp-upgrade.aws-credentials-file", "", "path to AWS credentials")
	flag.StringVar(&controlPlaneUpgradeOptions.PullSecretFile, "e2e.cp-upgrade.pull-secret-file", "", "path to pull secret")
	flag.StringVar(&controlPlaneUpgradeOptions.SSHKeyFile, "e2e.cp-upgrade.ssh-key-file", filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa.pub"), "path to SSH public key")
	flag.StringVar(&controlPlaneUpgradeOptions.InfraFile, "e2e.cp-upgrade.infra-json", "", "File containing infra description")
	flag.StringVar(&controlPlaneUpgradeOptions.InstanceProfile, "e2e.cp-upgrade.instance-profile", "hypershift-worker-profile", "Name of instance profile to use")
	flag.StringVar(&controlPlaneUpgradeOptions.FromReleaseImage, "e2e.cp-upgrade.from-release-image", "", "OCP release image to test")
	flag.StringVar(&controlPlaneUpgradeOptions.ToReleaseImage, "e2e.cp-upgrade.to-release-image", "", "OCP release image to upgrade to")
}

// ControlPlaneUpgradeInput are the validated options for running the test.
type ControlPlaneUpgradeInput struct {
	Client           crclient.Client
	FromReleaseImage string
	ToReleaseImage   string
	AWSCredentials   []byte
	PullSecret       []byte
	SSHKey           []byte
	Infra            awsinfra.CreateInfraOutput
	InstanceProfile  string
	InstanceType     string
}

// GetContext builds a QuickStartInput from the options.
func (o ControlPlaneUpgradeOptions) GetInput() (*ControlPlaneUpgradeInput, error) {
	pullSecret, err := ioutil.ReadFile(o.PullSecretFile)
	if err != nil {
		return nil, fmt.Errorf("couldn't read pull secret file %q: %w", o.PullSecretFile, err)
	}
	if len(pullSecret) == 0 {
		return nil, fmt.Errorf("pull secret is required")
	}

	awsCredentials, err := ioutil.ReadFile(o.AWSCredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("couldn't read aws credentials file %q: %w", o.AWSCredentialsFile, err)
	}
	if len(awsCredentials) == 0 {
		return nil, fmt.Errorf("AWS credentials are required")
	}

	sshKey, err := ioutil.ReadFile(o.SSHKeyFile)
	if err != nil {
		return nil, fmt.Errorf("couldn't read SSH key file %q: %w", o.SSHKeyFile, err)
	}
	if len(sshKey) == 0 {
		return nil, fmt.Errorf("SSH key is required")
	}

	if len(o.FromReleaseImage) == 0 {
		return nil, fmt.Errorf("release image is required")
	}
	if len(o.ToReleaseImage) == 0 {
		return nil, fmt.Errorf("release image to upgrade to is required")
	}

	infraBytes, err := ioutil.ReadFile(o.InfraFile)
	if err != nil {
		return nil, fmt.Errorf("couldn't read infra file %q: %w", o.InfraFile, err)
	}
	infra := awsinfra.CreateInfraOutput{}
	if err = json.Unmarshal(infraBytes, &infra); err != nil {
		return nil, fmt.Errorf("could parse infra file %q: %w", o.InfraFile, err)
	}

	client, err := crclient.New(ctrl.GetConfigOrDie(), crclient.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create kube client: %w", err)
	}

	return &ControlPlaneUpgradeInput{
		PullSecret:       pullSecret,
		AWSCredentials:   awsCredentials,
		SSHKey:           sshKey,
		Infra:            infra,
		InstanceProfile:  o.InstanceProfile,
		FromReleaseImage: o.FromReleaseImage,
		ToReleaseImage:   o.ToReleaseImage,
		Client:           client,
	}, nil
}

func TestControlPlaneUpgrade(t *testing.T) {
	if os.Getenv("OPENSHIFT_CI") == "true" {
		t.Skipf("upgrade test is not yet enabled in CI")
	}
	if len(os.Getenv("UPGRADE_TEST")) == 0 {
		t.Skipf("upgrade test is currently disabled by default, set UPGRADE_TEST=true to execute")
	}

	ctx, cancel := context.WithCancel(GlobalTestContext)
	defer cancel()

	g := NewWithT(t)

	input, err := controlPlaneUpgradeOptions.GetInput()
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test context")

	t.Logf("Testing upgrade from %s to %s", input.FromReleaseImage, input.ToReleaseImage)

	client := input.Client

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "e2e-",
			Labels: map[string]string{
				"hypershift-e2e-component": "hostedclusters-namespace",
			},
		},
	}
	err = client.Create(ctx, namespace)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")
	g.Expect(namespace.Name).ToNot(BeEmpty(), "generated namespace has no name")

	t.Logf("Created test namespace %s", namespace.Name)

	// Clean up the namespace after the test
	defer func() {
		cleanupCtx := context.Background()
		err := client.Delete(cleanupCtx, namespace, &crclient.DeleteOptions{})
		g.Expect(err).NotTo(HaveOccurred(), "failed to delete test namespace")

		t.Logf("Waiting for the test namespace %q to be deleted", namespace.Name)
		err = wait.PollInfinite(1*time.Second, func() (done bool, err error) {
			latestNamespace := &corev1.Namespace{}
			key := crclient.ObjectKey{
				Name: namespace.Name,
			}
			if err := client.Get(cleanupCtx, key, latestNamespace); err != nil {
				if errors.IsNotFound(err) {
					return true, nil
				}
				t.Logf("failed to get namespace %q: %s", latestNamespace.Name, err)
				return false, nil
			}
			return false, nil
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed to clean up test namespace")
	}()

	example := apifixtures.ExampleOptions{
		Namespace:        namespace.Name,
		Name:             "example-" + namespace.Name,
		ReleaseImage:     input.FromReleaseImage,
		PullSecret:       input.PullSecret,
		AWSCredentials:   input.AWSCredentials,
		SSHKey:           input.SSHKey,
		InfraID:          input.Infra.InfraID,
		ComputeCIDR:      input.Infra.ComputeCIDR,
		NodePoolReplicas: 0,
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

	err = client.Create(ctx, example.PullSecret)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create pull secret")

	t.Logf("Created test pull secret %s", example.PullSecret.Name)

	err = client.Create(ctx, example.AWSCredentials)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create aws credentials secret")

	t.Logf("Created test aws credentials secret %s", example.AWSCredentials.Name)

	err = client.Create(ctx, example.SSHKey)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create ssh key secret")

	t.Logf("Created test ssh key secret %s", example.SSHKey.Name)

	err = client.Create(ctx, example.Cluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create cluster resource")

	t.Logf("Created test hostedcluster %s", example.Cluster.Name)

	t.Logf("Waiting for guest kubeconfig to become available")
	var guestKubeConfigSecret corev1.Secret
	{
		timeoutCtx, _ := context.WithTimeout(ctx, 5*time.Minute)
		err := wait.PollUntil(1*time.Second, func() (done bool, err error) {
			var currentCluster hyperv1.HostedCluster
			err = client.Get(timeoutCtx, crclient.ObjectKeyFromObject(example.Cluster), &currentCluster)
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
			if err := client.Get(timeoutCtx, key, &guestKubeConfigSecret); err != nil {
				return false, nil
			}
			return true, nil
		}, timeoutCtx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "guest kubeconfig didn't become available")
	}

	g.Expect(guestKubeConfigSecret.Data).To(HaveKey("kubeconfig"), "guest kubeconfig secret is missing kubeconfig key")
	guestKubeConfigSecretData, _ := guestKubeConfigSecret.Data["kubeconfig"]

	guestRESTConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
	g.Expect(err).NotTo(HaveOccurred(), "failed to build kube client from guest kubeconfig")

	t.Logf("Establishing a connection to the guest apiserver")
	var guestClient crclient.Client
	{
		timeoutCtx, _ := context.WithTimeout(ctx, 5*time.Minute)
		err = wait.PollUntil(5*time.Second, func() (done bool, err error) {
			kubeClient, err := crclient.New(guestRESTConfig, crclient.Options{Scheme: hyperapi.Scheme})
			if err != nil {
				return false, nil
			}
			guestClient = kubeClient
			return true, nil
		}, timeoutCtx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "failed to establish a connection to the guest apiserver")
	}
	g.Expect(guestClient).NotTo(BeNil(), "failed to create a guest client")

	waitForImageRollout := func(ctx context.Context, clusterName types.NamespacedName, image string) (bool, error) {
		var cluster hyperv1.HostedCluster
		err = client.Get(ctx, clusterName, &cluster)
		if err != nil {
			t.Logf("error getting cluster: %s", err)
			return false, nil
		}

		isAvailable := meta.IsStatusConditionPresentAndEqual(cluster.Status.Conditions, string(hyperv1.Available), metav1.ConditionTrue)

		rolloutComplete := cluster.Status.Version != nil &&
			cluster.Status.Version.Desired.Image == image &&
			len(cluster.Status.Version.History) > 0 &&
			cluster.Status.Version.History[0].Image == cluster.Status.Version.Desired.Image &&
			cluster.Status.Version.History[0].State == configv1.CompletedUpdate

		if isAvailable && rolloutComplete {
			return true, nil
		}
		t.Logf("Waiting for cluster rollout (image=%s, isAvailable=%v, rolloutComplete=%v)", image, isAvailable, rolloutComplete)
		return false, nil
	}

	// Wait for the first rollout to be complete
	t.Logf("Waiting for cluster rollout")
	{
		timeoutCtx, _ := context.WithTimeout(ctx, 4*time.Minute)
		err := wait.PollUntil(1*time.Second, func() (done bool, err error) {
			return waitForImageRollout(timeoutCtx, crclient.ObjectKeyFromObject(example.Cluster), example.Cluster.Spec.Release.Image)
		}, timeoutCtx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "timed out waiting for hostedcluster rollout")
	}

	// Update the cluster image
	t.Logf("Updating cluster image")
	var cluster hyperv1.HostedCluster
	err = client.Get(ctx, crclient.ObjectKeyFromObject(example.Cluster), &cluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	cluster.Spec.Release.Image = input.ToReleaseImage
	err = client.Update(ctx, &cluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get update hostedcluster image")

	// Wait for the new rollout to be complete
	t.Logf("Waiting for cluster rollout")
	{
		timeoutCtx, _ := context.WithTimeout(ctx, 4*time.Minute)
		err := wait.PollUntil(1*time.Second, func() (done bool, err error) {
			return waitForImageRollout(timeoutCtx, crclient.ObjectKeyFromObject(&cluster), cluster.Spec.Release.Image)
		}, timeoutCtx.Done())
		g.Expect(err).NotTo(HaveOccurred(), "timed out waiting for updated hostedcluster rollout")
	}
}
