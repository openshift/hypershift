//go:build e2e
// +build e2e

package framework

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/support/conditions"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// GetConfig creates a REST config from current context
// No testing.T parameter - pure Ginkgo
func GetConfig() (*rest.Config, error) {
	return e2eutil.GetConfig()
}

// GetClient creates a controller-runtime client for Kubernetes
// No testing.T parameter - pure Ginkgo
func GetClient() (crclient.Client, error) {
	return e2eutil.GetClient()
}

// NewLogr creates a Ginkgo-compatible logger
// This is the pure Ginkgo replacement for util.NewLogr(t *testing.T)
func NewLogr() logr.Logger {
	// TODO: For pilot, we use a basic development logger that writes to Ginkgo
	// In production, this should use a GinkgoWriter-based zap logger
	logger, _ := zap.NewDevelopment()
	return zapr.NewLogger(logger)
}

// DefaultClusterOptions creates default cluster options with Ginkgo-compatible logger
// Pure Ginkgo version of Options.DefaultClusterOptions(t *testing.T)
func DefaultClusterOptions(o *e2eutil.Options) e2eutil.PlatformAgnosticOptions {
	// This is a surgical copy of util.Options.DefaultClusterOptions but uses NewLogr() instead of NewLogr(t)
	createOption := e2eutil.PlatformAgnosticOptions{
		RawCreateOptions: core.RawCreateOptions{
			ReleaseImage:                     o.LatestReleaseImage,
			NodePoolReplicas:                 2,
			ControlPlaneAvailabilityPolicy:   string(hyperv1.SingleReplica),
			InfrastructureAvailabilityPolicy: string(hyperv1.SingleReplica),
			NetworkType:                      string(o.ConfigurableClusterOptions.NetworkType),
			BaseDomain:                       o.ConfigurableClusterOptions.BaseDomain,
			PullSecretFile:                   o.ConfigurableClusterOptions.PullSecretFile,
			ControlPlaneOperatorImage:        o.ConfigurableClusterOptions.ControlPlaneOperatorImage,
			ExternalDNSDomain:                o.ConfigurableClusterOptions.ExternalDNSDomain,
			NodeUpgradeType:                  hyperv1.UpgradeTypeReplace,
			ServiceCIDR:                      []string{"172.31.0.0/16"},
			ClusterCIDR:                      []string{"10.132.0.0/14"},
			BeforeApply:                      o.BeforeApply,
			Log:                              NewLogr(), // ← Ginkgo-compatible logger
			Annotations: []string{
				fmt.Sprintf("%s=true", hyperv1.CleanupCloudResourcesAnnotation),
				fmt.Sprintf("%s=true", hyperv1.SkipReleaseImageValidation),
			},
			EtcdStorageClass: o.ConfigurableClusterOptions.EtcdStorageClass,
		},
		NonePlatform:      o.DefaultNoneOptions(),
		AWSPlatform:       o.DefaultAWSOptions(),
		KubevirtPlatform:  o.DefaultKubeVirtOptions(),
		AzurePlatform:     o.DefaultAzureOptions(),
		PowerVSPlatform:   o.DefaultPowerVSOptions(),
		OpenStackPlatform: o.DefaultOpenStackOptions(),
	}

	switch o.Platform {
	case hyperv1.AWSPlatform, hyperv1.AzurePlatform, hyperv1.NonePlatform, hyperv1.KubevirtPlatform, hyperv1.OpenStackPlatform:
		createOption.Arch = hyperv1.ArchitectureAMD64
	case hyperv1.PowerVSPlatform:
		createOption.Arch = hyperv1.ArchitecturePPC64LE
	}

	if o.ConfigurableClusterOptions.SSHKeyFile == "" {
		createOption.GenerateSSH = true
	} else {
		createOption.SSHKeyFile = o.ConfigurableClusterOptions.SSHKeyFile
	}

	if o.ConfigurableClusterOptions.Annotations != nil {
		for k, v := range o.ConfigurableClusterOptions.Annotations {
			createOption.Annotations = append(createOption.Annotations, fmt.Sprintf("%s=%s", k, v))
		}
	}

	return createOption
}

// WaitForGuestKubeConfig waits for the guest cluster kubeconfig to be published
// Pure Ginkgo version - no testing.T parameter
func WaitForGuestKubeConfig(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) []byte {
	GinkgoHelper()
	var guestKubeConfigSecretRef crclient.ObjectKey
	EventuallyObject(ctx, fmt.Sprintf("kubeconfig to be published for HostedCluster %s/%s", hostedCluster.Namespace, hostedCluster.Name),
		func(ctx context.Context) (*hyperv1.HostedCluster, error) {
			err := client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
			return hostedCluster, err
		},
		[]Predicate[*hyperv1.HostedCluster]{
			func(cluster *hyperv1.HostedCluster) (done bool, reasons string, err error) {
				guestKubeConfigSecretRef = crclient.ObjectKey{
					Namespace: hostedCluster.Namespace,
					Name:      ptr.Deref(hostedCluster.Status.KubeConfig, corev1.LocalObjectReference{}).Name,
				}
				return hostedCluster.Status.KubeConfig != nil, "expected a kubeconfig reference in status", nil
			},
		},
	)

	var data []byte
	EventuallyObject(ctx, "kubeconfig secret to have data",
		func(ctx context.Context) (*corev1.Secret, error) {
			var guestKubeConfigSecret corev1.Secret
			err := client.Get(ctx, guestKubeConfigSecretRef, &guestKubeConfigSecret)
			return &guestKubeConfigSecret, err
		},
		[]Predicate[*corev1.Secret]{
			func(secret *corev1.Secret) (done bool, reasons string, err error) {
				var hasData bool
				data, hasData = secret.Data["kubeconfig"]
				return hasData, "expected secret to contain kubeconfig in data", nil
			},
		},
	)
	return data
}

// WaitForGuestClient waits for and returns a controller-runtime client for the guest cluster
// Pure Ginkgo version - no testing.T parameter
func WaitForGuestClient(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) crclient.Client {
	GinkgoHelper()
	guestKubeConfigSecretData := WaitForGuestKubeConfig(ctx, client, hostedCluster)

	guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
	Expect(err).NotTo(HaveOccurred(), "couldn't load guest kubeconfig")

	// we know we're the only real clients for these test servers, so turn off client-side throttling
	guestConfig.QPS = -1
	guestConfig.Burst = -1

	kubeClient, err := kubernetes.NewForConfig(guestConfig)
	if err != nil {
		Fail(fmt.Sprintf("failed to create kube client for guest cluster: %v", err))
	}

	if e2eutil.IsLessThan(e2eutil.Version415) {
		// SelfSubjectReview API is only available in 4.15+
		// Use the old method to check if the API server is up
		err = wait.PollUntilContextTimeout(ctx, 35*time.Second, 10*time.Minute, true, func(ctx context.Context) (done bool, err error) {
			_, err = crclient.New(guestConfig, crclient.Options{Scheme: e2eutil.Scheme()})
			if err != nil {
				logf("attempt to connect failed: %s", err)
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			Fail(fmt.Sprintf("failed to connect to guest cluster: %v", err))
		}
	} else {
		EventuallyObject(ctx, "a successful connection to the guest API server",
			func(ctx context.Context) (*authenticationv1.SelfSubjectReview, error) {
				return kubeClient.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
			}, nil, WithTimeout(10*time.Minute),
		)
	}

	guestClient, err := crclient.New(guestConfig, crclient.Options{Scheme: e2eutil.Scheme()})
	if err != nil {
		Fail(fmt.Sprintf("could not create client for guest cluster: %v", err))
	}
	return guestClient
}

// ArtifactSubdirFor returns the artifact subdirectory name for the current Ginkgo spec
// Pure Ginkgo version - uses CurrentSpecReport() instead of testing.T.Name()
func ArtifactSubdirFor() string {
	// Get the current spec report from Ginkgo
	report := CurrentSpecReport()
	// Use the full text of the spec (like "CreateCluster should create and validate a hypershift cluster")
	// Replace "/" with "_" to make it filesystem-safe, just like the testing.T version
	return strings.ReplaceAll(report.FullText(), "/", "_")
}

// DeleteNamespace deletes a namespace and waits for it to be finalized
// Pure Ginkgo version - surgically duplicated from util/util.go:DeleteNamespace
func DeleteNamespace(ctx context.Context, client crclient.Client, namespace string) error {
	GinkgoHelper()

	if os.Getenv("EVENTUALLY_VERBOSE") != "false" {
		logf("Deleting namespace %s", namespace)
	}
	err := wait.PollUntilContextTimeout(ctx, 10*time.Second, 20*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		if err := client.Delete(ctx, ns, &crclient.DeleteOptions{}); err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			logf("Failed to delete namespace: %s, will retry: %v", namespace, err)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("failed to delete namespace: %w", err)
	}

	if os.Getenv("EVENTUALLY_VERBOSE") != "false" {
		logf("Waiting for namespace %s to be finalized", namespace)
	}
	err = wait.PollUntilContextTimeout(ctx, 10*time.Second, 20*time.Minute, true, func(ctx context.Context) (done bool, err error) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(ns), ns); err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			logf("Failed to get namespace: %s. %v", namespace, err)
			return false, nil
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("namespace still exists after deletion timeout: %v", err)
	}
	if os.Getenv("EVENTUALLY_VERBOSE") != "false" {
		logf("Deleted namespace %s", namespace)
	}
	return nil
}

// ValidateHostedClusterConditions waits for the HostedCluster to have all expected conditions
// Pure Ginkgo version - surgically duplicated from util/util.go:ValidateHostedClusterConditions
// This function waits for the cluster rollout to complete (ClusterVersionProgressing: False)
func ValidateHostedClusterConditions(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, hasWorkerNodes bool, timeout time.Duration) {
	GinkgoHelper()

	expectedConditions := conditions.ExpectedHCConditions(hostedCluster)

	// OCPBUGS-59885: Ignore KubeVirtNodesLiveMigratable in e2e; CI envs may lack RWX-capable PVCs, causing false failures
	delete(expectedConditions, hyperv1.KubeVirtNodesLiveMigratable)

	if !hasWorkerNodes {
		expectedConditions[hyperv1.ClusterVersionAvailable] = metav1.ConditionFalse
		expectedConditions[hyperv1.ClusterVersionSucceeding] = metav1.ConditionFalse
		expectedConditions[hyperv1.ClusterVersionProgressing] = metav1.ConditionTrue
		delete(expectedConditions, hyperv1.ValidKubeVirtInfraNetworkMTU)
	}

	if e2eutil.IsLessThan(e2eutil.Version415) {
		// ValidKubeVirtInfraNetworkMTU condition is not present in versions < 4.15
		delete(expectedConditions, hyperv1.ValidKubeVirtInfraNetworkMTU)
	}

	var predicates []Predicate[*hyperv1.HostedCluster]
	for conditionType, conditionStatus := range expectedConditions {
		predicates = append(predicates, ConditionPredicate[*hyperv1.HostedCluster](Condition{
			Type:   string(conditionType),
			Status: conditionStatus,
		}))
	}

	EventuallyObject(ctx, fmt.Sprintf("HostedCluster %s/%s to have valid conditions", hostedCluster.Namespace, hostedCluster.Name),
		func(ctx context.Context) (*hyperv1.HostedCluster, error) {
			hc := &hyperv1.HostedCluster{}
			err := client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hc)
			return hc, err
		}, predicates, WithTimeout(timeout), WithoutConditionDump(),
	)
}
