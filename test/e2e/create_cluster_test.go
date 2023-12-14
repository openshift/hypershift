//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftv1beta1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/hypershift/v1beta1"
	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"
	pkimanifests "github.com/openshift/hypershift/control-plane-pki-operator/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	authenticationv1 "k8s.io/api/authentication/v1"
	certificatesv1 "k8s.io/api/certificates/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	certificatesv1applyconfigurations "k8s.io/client-go/applyconfigurations/certificates/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// TestCreateCluster implements a test that creates a cluster with the code under test
// vs upgrading to the code under test as TestUpgradeControlPlane does.
func TestCreateCluster(t *testing.T) {
	t.Parallel()
	_, key, csr := certKeyRequest(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	zones := strings.Split(globalOpts.configurableClusterOptions.Zone.String(), ",")
	if len(zones) >= 3 {
		// CreateCluster also tests multi-zone workers work properly if a sufficient number of zones are configured
		t.Logf("Sufficient zones available for InfrastructureAvailabilityPolicy HighlyAvailable")
		clusterOpts.AWSPlatform.Zones = zones
		clusterOpts.InfrastructureAvailabilityPolicy = string(hyperv1.HighlyAvailable)
		clusterOpts.NodePoolReplicas = 1
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		t.Run("break-glass-credentials", func(t *testing.T) {
			// Sanity check the cluster by waiting for the nodes to report ready
			t.Logf("Waiting for guest client to become available")
			_ = e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

			hostedControlPlaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

			validateCertificateAuth := func(t *testing.T, root *rest.Config, crt, key []byte) {
				t.Log("validating that the client certificate provides the appropriate access")

				t.Log("amending the existing kubeconfig to use break-glass client certificate credentials")
				certConfig := rest.AnonymousClientConfig(root)
				certConfig.TLSClientConfig.CertData = crt
				certConfig.TLSClientConfig.KeyData = key

				breakGlassTenantClient, err := kubernetes.NewForConfig(certConfig)
				if err != nil {
					t.Fatalf("could not create client: %v", err)
				}

				t.Log("issuing SSR to identify the subject we are given using the client certificate")
				response, err := breakGlassTenantClient.AuthenticationV1().SelfSubjectReviews().Create(context.Background(), &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("could not send SSAR: %v", err)
				}

				t.Log("ensuring that the SSR identifies the client certificate as having system:masters power and correct username")
				if !sets.New[string](response.Status.UserInfo.Groups...).Has("system:masters") || !strings.HasPrefix(response.Status.UserInfo.Username, "customer-break-glass-") {
					t.Fatalf("did not get correct SSR response: %#v", response)
				}
			}

			t.Logf("fetching guest kubeconfig")
			guestKubeConfigSecretData, err := e2eutil.WaitForGuestKubeConfig(t, ctx, mgtClient, hostedCluster)
			g.Expect(err).NotTo(HaveOccurred(), "couldn't get kubeconfig")

			guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
			g.Expect(err).NotTo(HaveOccurred(), "couldn't load guest kubeconfig")

			t.Run("direct fetch", func(t *testing.T) {
				clientCertificate := pkimanifests.CustomerSystemAdminClientCertSecret(hostedControlPlaneNamespace)
				t.Logf("Grabbing customer break-glass credentials from client certificate secret %s/%s", clientCertificate.Namespace, clientCertificate.Name)
				if err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 3*time.Minute, true, func(ctx context.Context) (done bool, err error) {
					getErr := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(clientCertificate), clientCertificate)
					if apierrors.IsNotFound(getErr) {
						return false, nil
					}
					return getErr == nil, err
				}); err != nil {
					t.Fatalf("client cert didn't become available: %v", err)
				}

				validateCertificateAuth(t, guestConfig, clientCertificate.Data["tls.crt"], clientCertificate.Data["tls.key"])
			})

			t.Run("CSR flow", func(t *testing.T) {
				restConfig, err := e2eutil.GetConfig()
				if err != nil {
					t.Fatalf("could not get rest config for mgmt plane: %v", err)
				}
				kubeClient, err := kubernetes.NewForConfig(restConfig)
				if err != nil {
					t.Fatalf("could not create k8s client for mgmt plane: %v", err)
				}
				hypershiftClient, err := hypershiftclient.NewForConfig(restConfig)
				if err != nil {
					t.Fatalf("could not create hypershift client for mgmt plane: %v", err)
				}

				csrName := hostedControlPlaneNamespace
				signerName := certificates.SignerNameForHC(hostedCluster, certificates.CustomerBreakGlassSigner)
				t.Logf("creating CSR %q for signer %q, requesting client auth usages", csrName, signerName)
				csrCfg := certificatesv1applyconfigurations.CertificateSigningRequest(csrName)
				csrCfg.Spec = certificatesv1applyconfigurations.CertificateSigningRequestSpec().
					WithSignerName(signerName).
					WithRequest(csr...).
					WithUsages(certificatesv1.UsageClientAuth)
				if _, err := kubeClient.CertificatesV1().CertificateSigningRequests().Apply(ctx, csrCfg, metav1.ApplyOptions{FieldManager: "e2e-test"}); err != nil {
					t.Fatalf("failed to create CSR: %v", err)
				}

				t.Logf("creating CSRA %s/%s to trigger automatic approval of the CSR", csrName, csrName)
				csraCfg := hypershiftv1beta1applyconfigurations.CertificateSigningRequestApproval(hostedControlPlaneNamespace, csrName)
				if _, err := hypershiftClient.HypershiftV1beta1().CertificateSigningRequestApprovals(hostedControlPlaneNamespace).Apply(ctx, csraCfg, metav1.ApplyOptions{FieldManager: "e2e-test"}); err != nil {
					t.Fatalf("failed to create CSRA: %v", err)
				}

				t.Logf("waiting for CSR %q to be approved and signed", csrName)
				var signedCrt []byte
				var lastResourceVersion string
				if err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 5*time.Minute, true, func(ctx context.Context) (done bool, err error) {
					csr, err := kubeClient.CertificatesV1().CertificateSigningRequests().Get(ctx, csrName, metav1.GetOptions{})
					if apierrors.IsNotFound(err) {
						t.Logf("CSR %q does not exist yet", csrName)
						return false, nil
					}
					if err != nil && !apierrors.IsNotFound(err) {
						return true, err
					}
					if csr != nil && csr.Status.Certificate != nil {
						signedCrt = csr.Status.Certificate
						return true, nil
					}
					if csr.ObjectMeta.ResourceVersion != lastResourceVersion {
						t.Logf("CSR %q observed at RV %s", csrName, csr.ObjectMeta.ResourceVersion)
						for _, condition := range csr.Status.Conditions {
							msg := fmt.Sprintf("%s=%s", condition.Type, condition.Status)
							if condition.Reason != "" {
								msg += ": " + condition.Reason
							}
							if condition.Message != "" {
								msg += "(" + condition.Message + ")"
							}
							t.Logf("CSR %q status: %s", csr.Name, msg)
						}
						lastResourceVersion = csr.ObjectMeta.ResourceVersion
					}
					return false, nil
				}); err != nil {
					t.Fatalf("never saw CSR fulfilled: %v", err)
				}

				if len(signedCrt) == 0 {
					t.Fatal("got a zero-length signed cert back")
				}

				validateCertificateAuth(t, guestConfig, signedCrt, key)
			})
		})
	}).
		Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

func TestCreateClusterRequestServingIsolation(t *testing.T) {
	if !globalOpts.RequestServingIsolation {
		t.Skip("Skipping request serving isolation test")
	}
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("Request serving isolation test requires the AWS platform")
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	nodePools := e2eutil.SetupReqServingClusterNodePools(ctx, t, globalOpts.ManagementParentKubeconfig, globalOpts.ManagementClusterNamespace, globalOpts.ManagementClusterName)
	defer e2eutil.TearDownNodePools(ctx, t, globalOpts.ManagementParentKubeconfig, nodePools)

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	zones := strings.Split(globalOpts.configurableClusterOptions.Zone.String(), ",")
	if len(zones) >= 3 {
		// CreateCluster also tests multi-zone workers work properly if a sufficient number of zones are configured
		t.Logf("Sufficient zones available for InfrastructureAvailabilityPolicy HighlyAvailable")
		clusterOpts.AWSPlatform.Zones = zones
		clusterOpts.InfrastructureAvailabilityPolicy = string(hyperv1.HighlyAvailable)
		clusterOpts.NodePoolReplicas = 1
		clusterOpts.NodeSelector = map[string]string{"hypershift.openshift.io/control-plane": "true"}
	}

	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)
	clusterOpts.Annotations = append(clusterOpts.Annotations, fmt.Sprintf("%s=%s", hyperv1.TopologyAnnotation, hyperv1.DedicatedRequestServingComponentsTopology))

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		guestClient := e2eutil.WaitForGuestClient(t, testContext, mgtClient, hostedCluster)
		e2eutil.EnsurePSANotPrivileged(t, ctx, guestClient)
		e2eutil.EnsureAllReqServingPodsLandOnReqServingNodes(t, ctx, guestClient)
		e2eutil.EnsureOnlyRequestServingPodsOnRequestServingNodes(t, ctx, guestClient)
		e2eutil.EnsureNoHCPPodsLandOnDefaultNode(t, ctx, guestClient, hostedCluster)
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

func TestCreateClusterCustomConfig(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	// find kms key ARN using alias
	kmsKeyArn, err := e2eutil.GetKMSKeyArn(clusterOpts.AWSPlatform.AWSCredentialsFile, clusterOpts.AWSPlatform.Region, globalOpts.configurableClusterOptions.AWSKmsKeyAlias)
	if err != nil || kmsKeyArn == nil {
		t.Fatal("failed to retrieve kms key arn")
	}

	clusterOpts.AWSPlatform.EtcdKMSKeyARN = *kmsKeyArn

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {

		g.Expect(hostedCluster.Spec.SecretEncryption.KMS.AWS.ActiveKey.ARN).To(Equal(*kmsKeyArn))
		g.Expect(hostedCluster.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN).ToNot(BeEmpty())

		guestClient := e2eutil.WaitForGuestClient(t, testContext, mgtClient, hostedCluster)
		e2eutil.EnsureSecretEncryptedUsingKMS(t, ctx, hostedCluster, guestClient)
		// test oauth with identity provider
		e2eutil.EnsureOAuthWithIdentityProvider(t, ctx, mgtClient, hostedCluster)
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

func TestNoneCreateCluster(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Wait for the rollout to be reported complete
		t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
		// Since the None platform has no workers, CVO will not have expectations set,
		// which in turn means that the ClusterVersion object will never be populated.
		// Therefore only test if the control plane comes up (etc, apiserver, ...)
		e2eutil.WaitForConditionsOnHostedControlPlane(t, ctx, mgtClient, hostedCluster, globalOpts.LatestReleaseImage)

		// etcd restarts for me once always and apiserver two times before running stable
		// e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
	}).Execute(&clusterOpts, hyperv1.NonePlatform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

// TestCreateClusterProxy implements a test that creates a cluster behind a proxy with the code under test.
func TestCreateClusterProxy(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.AWSPlatform.EnableProxy = true
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)

	e2eutil.NewHypershiftTest(t, ctx, nil).
		Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

// TestCreateClusterPrivate implements a smoke test that creates a private cluster.
// Validations requiring guest cluster client are dropped here since the kas is not accessible when private.
// In the future we might want to leverage https://issues.redhat.com/browse/HOSTEDCP-697 to access guest cluster.
func TestCreateClusterPrivate(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)
	clusterOpts.AWSPlatform.EndpointAccess = string(hyperv1.Private)

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Private -> publicAndPrivate
		t.Run("SwitchFromPrivateToPublic", testSwitchFromPrivateToPublic(ctx, mgtClient, hostedCluster, &clusterOpts))
		// publicAndPrivate -> Private
		t.Run("SwitchFromPublicToPrivate", testSwitchFromPublicToPrivate(ctx, mgtClient, hostedCluster, &clusterOpts))
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

func testSwitchFromPrivateToPublic(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, clusterOpts *core.CreateOptions) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)

		err := e2eutil.UpdateObject(t, ctx, client, hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.Platform.AWS.EndpointAccess = hyperv1.PublicAndPrivate
		})
		g.Expect(err).ToNot(HaveOccurred(), "failed to update hostedcluster EndpointAccess")

		e2eutil.ValidatePublicCluster(t, ctx, client, hostedCluster, clusterOpts)
	}
}

func testSwitchFromPublicToPrivate(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, clusterOpts *core.CreateOptions) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		err := e2eutil.UpdateObject(t, ctx, client, hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.Platform.AWS.EndpointAccess = hyperv1.Private
		})
		g.Expect(err).ToNot(HaveOccurred(), "failed to update hostedcluster EndpointAccess")

		e2eutil.ValidatePrivateCluster(t, ctx, client, hostedCluster, clusterOpts)
	}
}
