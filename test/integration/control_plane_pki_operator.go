//go:build integration || e2e

package integration

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	certificatesv1alpha1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/certificates/v1alpha1"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/test/e2e/util"
	authenticationv1 "k8s.io/api/authentication/v1"
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	certificatesv1applyconfigurations "k8s.io/client-go/applyconfigurations/certificates/v1"
	"k8s.io/client-go/kubernetes"
	corev1Client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	controllerruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"
	pkimanifests "github.com/openshift/hypershift/control-plane-pki-operator/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/integration/framework"
)

func RunTestControlPlanePKIOperatorBreakGlassCredentials(t *testing.T, ctx context.Context, hostedCluster *hypershiftv1beta1.HostedCluster, mgmt, guest *framework.Clients) {
	t.Run("break-glass-credentials", func(t *testing.T) {
		if hostedCluster.Spec.Platform.Type == hypershiftv1beta1.KubevirtPlatform {
			t.Skip("skipping break-glass credentials test on KubeVirt platform")
		}
		// control-plane-pki-operator is only available in 4.15 and later
		e2eutil.AtLeast(t, e2eutil.Version415)
		hostedControlPlaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		for _, testCase := range []struct {
			clientCertificate *corev1.Secret
			signer            certificates.SignerClass
		}{
			{
				clientCertificate: pkimanifests.CustomerSystemAdminClientCertSecret(hostedControlPlaneNamespace),
				signer:            certificates.CustomerBreakGlassSigner,
			},
			{
				clientCertificate: pkimanifests.SRESystemAdminClientCertSecret(hostedControlPlaneNamespace),
				signer:            certificates.SREBreakGlassSigner,
			},
		} {
			testCase := testCase
			t.Run(string(testCase.signer), func(t *testing.T) {
				t.Parallel()
				t.Run("direct fetch", func(t *testing.T) {
					t.Logf("Grabbing customer break-glass credentials from client certificate secret %s/%s", testCase.clientCertificate.Namespace, testCase.clientCertificate.Name)
					if err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 3*time.Minute, true, func(ctx context.Context) (done bool, err error) {
						getErr := mgmt.CRClient.Get(ctx, controllerruntimeclient.ObjectKeyFromObject(testCase.clientCertificate), testCase.clientCertificate)
						if apierrors.IsNotFound(getErr) {
							return false, nil
						}
						return getErr == nil, err
					}); err != nil {
						t.Fatalf("client cert didn't become available: %v", err)
					}

					validateCertificateAuth(t, ctx, guest.Cfg, testCase.clientCertificate.Data["tls.crt"], testCase.clientCertificate.Data["tls.key"], func(s string) bool {
						return strings.HasPrefix(s, certificates.CommonNamePrefix(testCase.signer))
					}, mgmt.KubeClient.CoreV1().Secrets(testCase.clientCertificate.Namespace))
				})

				t.Run("CSR flow", func(t *testing.T) {
					t.Run("invalid CN flagged in status", func(t *testing.T) {
						validateInvalidCN(t, ctx, hostedCluster, mgmt, guest, testCase.signer)
					})
					signedCrt := validateCSRFlow(t, ctx, hostedCluster, mgmt, guest, testCase.signer)

					t.Run("revocation", func(t *testing.T) {
						validateRevocation(t, ctx, hostedCluster, mgmt, guest, testCase.signer, signedCrt)
					})
				})
			})
		}
		t.Run("independent signers", func(t *testing.T) {
			t.Log("generating new break-glass credentials for more than one signer")
			customerSignedCrt := validateCSRFlow(t, ctx, hostedCluster, mgmt, guest, certificates.CustomerBreakGlassSigner)
			sreSignedCrt := validateCSRFlow(t, ctx, hostedCluster, mgmt, guest, certificates.SREBreakGlassSigner)

			t.Logf("revoking the %q signer", certificates.CustomerBreakGlassSigner)
			validateRevocation(t, ctx, hostedCluster, mgmt, guest, certificates.CustomerBreakGlassSigner, customerSignedCrt)

			t.Logf("ensuring the break-glass credentials from %q signer still work", certificates.SREBreakGlassSigner)
			_, sreKey, _, _ := framework.CertKeyRequest(t, certificates.SREBreakGlassSigner)
			validateCertificateAuth(t, ctx, guest.Cfg, sreSignedCrt, sreKey, func(s string) bool {
				return s == framework.CommonNameFor(certificates.SREBreakGlassSigner)
			}, mgmt.KubeClient.CoreV1().Secrets(manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)))
		})
	})
}

func base36sum224(data []byte) string {
	hash := sha256.Sum224(data)
	var i big.Int
	i.SetBytes(hash[:])
	return i.Text(36)
}

func clientForCertKey(t *testing.T, root *rest.Config, crt, key []byte) *kubernetes.Clientset {
	t.Log("amending the existing kubeconfig to use break-glass client certificate credentials")
	certConfig := rest.AnonymousClientConfig(root)
	certConfig.TLSClientConfig.CertData = crt
	certConfig.TLSClientConfig.KeyData = key

	breakGlassTenantClient, err := kubernetes.NewForConfig(certConfig)
	if err != nil {
		t.Fatalf("could not create client: %v", err)
	}

	return breakGlassTenantClient
}

func validateCertificateAuth(t *testing.T, ctx context.Context, root *rest.Config, crt, key []byte, usernameValid func(string) bool, mgmtDebugClient corev1Client.SecretInterface) {
	t.Log("validating that the client certificate provides the appropriate access")
	breakGlassTenantClient := clientForCertKey(t, root, crt, key)

	t.Log("issuing SSR to identify the subject we are given using the client certificate")
	response, err := breakGlassTenantClient.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsUnauthorized(err) {
			t.Logf("got an unauthorized error for SSR, debugging certificates")
			t.Logf("client certificate: %s", string(crt))
			caBundle, err := mgmtDebugClient.Get(ctx, cpomanifests.TotalClientCABundle("").Name, metav1.GetOptions{})
			if err != nil {
				t.Logf("failed to get total client CA bundle: %v", err)
			}
			t.Logf("server total certificate trust bundle: %s", string(caBundle.Data[certs.CASignerCertMapKey]))
			caBundle, err = mgmtDebugClient.Get(ctx, pkimanifests.TotalKASClientCABundle("").Name, metav1.GetOptions{})
			if err != nil {
				t.Logf("failed to get KAS total client CA bundle: %v", err)
			}
			t.Logf("KAS certificate trust bundle: %s", string(caBundle.Data[certs.OCPCASignerCertMapKey]))
		}
		t.Fatalf("could not send SSR: %v", err)
	}

	t.Log("ensuring that the SSR identifies the client certificate as having system:masters power and correct username")
	if !sets.New[string](response.Status.UserInfo.Groups...).Has("system:masters") ||
		!usernameValid(response.Status.UserInfo.Username) {
		t.Fatalf("did not get correct SSR response: %#v", response)
	}
}

func validateInvalidCN(t *testing.T, ctx context.Context, hostedCluster *hypershiftv1beta1.HostedCluster, mgmt, guest *framework.Clients, signer certificates.SignerClass) {
	hostedControlPlaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	_, _, _, wrongCsr := framework.CertKeyRequest(t, signer)
	signerName := certificates.SignerNameForHC(hostedCluster, signer)
	wrongCSRName := base36sum224(append(append([]byte(hostedControlPlaneNamespace), []byte(signer)...), []byte(t.Name())...))
	t.Logf("creating invalid CSR %q for signer %q, requesting client auth usages", wrongCSRName, signerName)
	wrongCSRCfg := certificatesv1applyconfigurations.CertificateSigningRequest(wrongCSRName)
	wrongCSRCfg.Spec = certificatesv1applyconfigurations.CertificateSigningRequestSpec().
		WithSignerName(signerName).
		WithRequest(wrongCsr...).
		WithUsages(certificatesv1.UsageClientAuth)
	if _, err := mgmt.KubeClient.CertificatesV1().CertificateSigningRequests().Apply(ctx, wrongCSRCfg, metav1.ApplyOptions{FieldManager: "e2e-test"}); err != nil {
		t.Fatalf("failed to create CSR: %v", err)
	}

	t.Logf("creating CSRA %s/%s to trigger automatic approval of the CSR", hostedControlPlaneNamespace, wrongCSRName)
	wrongCSRACfg := certificatesv1alpha1applyconfigurations.CertificateSigningRequestApproval(wrongCSRName, hostedControlPlaneNamespace)
	if _, err := mgmt.HyperShiftClient.CertificatesV1alpha1().CertificateSigningRequestApprovals(hostedControlPlaneNamespace).Apply(ctx, wrongCSRACfg, metav1.ApplyOptions{FieldManager: "e2e-test"}); err != nil {
		t.Fatalf("failed to create CSRA: %v", err)
	}

	util.EventuallyObject(
		t, ctx, fmt.Sprintf("waiting for CSR %q to have invalid CN exposed in status", wrongCSRName),
		func(ctx context.Context) (*certificatesv1.CertificateSigningRequest, error) {
			return mgmt.KubeClient.CertificatesV1().CertificateSigningRequests().Get(ctx, wrongCSRName, metav1.GetOptions{})
		},
		[]util.Predicate[*certificatesv1.CertificateSigningRequest]{
			util.ConditionPredicate[*certificatesv1.CertificateSigningRequest](util.Condition{
				Type:   string(certificatesv1.CertificateFailed),
				Status: metav1.ConditionTrue,
				Reason: "SignerValidationFailure",
			}),
		},
	)
}

func validateCSRFlow(t *testing.T, ctx context.Context, hostedCluster *hypershiftv1beta1.HostedCluster, mgmt, guest *framework.Clients, signer certificates.SignerClass) []byte {
	hostedControlPlaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	_, key, csr, _ := framework.CertKeyRequest(t, signer)
	signerName := certificates.SignerNameForHC(hostedCluster, signer)
	csrName := base36sum224(append(append([]byte(hostedControlPlaneNamespace), []byte(signer)...), []byte(t.Name())...))
	t.Logf("creating CSR %q for signer %q, requesting client auth usages", csrName, signer)
	csrCfg := certificatesv1applyconfigurations.CertificateSigningRequest(csrName)
	csrCfg.Spec = certificatesv1applyconfigurations.CertificateSigningRequestSpec().
		WithSignerName(signerName).
		WithRequest(csr...).
		WithUsages(certificatesv1.UsageClientAuth)
	if _, err := mgmt.KubeClient.CertificatesV1().CertificateSigningRequests().Apply(ctx, csrCfg, metav1.ApplyOptions{FieldManager: "e2e-test"}); err != nil {
		t.Fatalf("failed to create CSR: %v", err)
	}

	t.Logf("creating CSRA %s/%s to trigger automatic approval of the CSR", hostedControlPlaneNamespace, csrName)
	csraCfg := certificatesv1alpha1applyconfigurations.CertificateSigningRequestApproval(csrName, hostedControlPlaneNamespace)
	if _, err := mgmt.HyperShiftClient.CertificatesV1alpha1().CertificateSigningRequestApprovals(hostedControlPlaneNamespace).Apply(ctx, csraCfg, metav1.ApplyOptions{FieldManager: "e2e-test"}); err != nil {
		t.Fatalf("failed to create CSRA: %v", err)
	}

	var signedCrt []byte
	util.EventuallyObject(
		t, ctx, fmt.Sprintf("CSR %q to be approved and signed", csrName),
		func(ctx context.Context) (*certificatesv1.CertificateSigningRequest, error) {
			return mgmt.KubeClient.CertificatesV1().CertificateSigningRequests().Get(ctx, csrName, metav1.GetOptions{})
		},
		[]util.Predicate[*certificatesv1.CertificateSigningRequest]{
			func(csr *certificatesv1.CertificateSigningRequest) (done bool, reasons string, err error) {
				if csr != nil && csr.Status.Certificate != nil {
					signedCrt = csr.Status.Certificate
					return true, "certificate present", nil
				}
				return false, "no certificate present", nil
			},
		},
	)

	if len(signedCrt) == 0 {
		t.Fatal("got a zero-length signed cert back")
	}

	validateCertificateAuth(t, ctx, guest.Cfg, signedCrt, key, func(s string) bool {
		return s == framework.CommonNameFor(signer)
	}, mgmt.KubeClient.CoreV1().Secrets(manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)))

	return signedCrt
}

func validateRevocation(t *testing.T, ctx context.Context, hostedCluster *hypershiftv1beta1.HostedCluster, mgmt, guest *framework.Clients, signer certificates.SignerClass, signedCrt []byte) {
	if len(signedCrt) == 0 {
		t.Fatalf("programmer error: zero-length signed cert but we haven't failed yet!")
	}

	hostedControlPlaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	_, key, _, _ := framework.CertKeyRequest(t, signer)
	crrName := base36sum224(append(append([]byte(hostedControlPlaneNamespace), []byte(signer)...), []byte(t.Name())...))
	t.Logf("creating CRR %s/%s to trigger signer certificate revocation", hostedControlPlaneNamespace, crrName)
	crrCfg := certificatesv1alpha1applyconfigurations.CertificateRevocationRequest(crrName, hostedControlPlaneNamespace).
		WithSpec(certificatesv1alpha1applyconfigurations.CertificateRevocationRequestSpec().WithSignerClass(string(signer)))
	if _, err := mgmt.HyperShiftClient.CertificatesV1alpha1().CertificateRevocationRequests(hostedControlPlaneNamespace).Apply(ctx, crrCfg, metav1.ApplyOptions{FieldManager: "e2e-test"}); err != nil {
		t.Fatalf("failed to create CRR: %v", err)
	}

	util.EventuallyObject(
		t, ctx, fmt.Sprintf("CRR %s/%s to complete", hostedControlPlaneNamespace, crrName),
		func(ctx context.Context) (*certificatesv1alpha1.CertificateRevocationRequest, error) {
			return mgmt.HyperShiftClient.CertificatesV1alpha1().CertificateRevocationRequests(hostedControlPlaneNamespace).Get(ctx, crrName, metav1.GetOptions{})
		},
		[]util.Predicate[*certificatesv1alpha1.CertificateRevocationRequest]{
			util.ConditionPredicate[*certificatesv1alpha1.CertificateRevocationRequest](util.Condition{
				Type:   certificatesv1alpha1.PreviousCertificatesRevokedType,
				Status: metav1.ConditionTrue,
			}),
		},
	)

	t.Logf("creating a client using the a certificate from the revoked signer")
	previousCertClient := clientForCertKey(t, guest.Cfg, signedCrt, key)

	t.Log("issuing SSR to confirm that we're not authorized to contact the server")
	response, err := previousCertClient.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if !apierrors.IsUnauthorized(err) {
		t.Fatalf("expected an unauthorized error, got %v, response %#v", err, response)
	}
}
