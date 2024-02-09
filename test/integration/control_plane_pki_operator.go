//go:build integration || e2e

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	certificatesv1alpha1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/certificates/v1alpha1"
	authenticationv1 "k8s.io/api/authentication/v1"
	certificatesv1 "k8s.io/api/certificates/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	certificatesv1applyconfigurations "k8s.io/client-go/applyconfigurations/certificates/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	controllerruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"
	pkimanifests "github.com/openshift/hypershift/control-plane-pki-operator/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/test/integration/framework"
)

func RunTestControlPlanePKIOperatorBreakGlassCredentials(t *testing.T, ctx context.Context, hostedCluster *hypershiftv1beta1.HostedCluster, mgmt, guest *framework.Clients) {
	_, key, csr := framework.CertKeyRequest(t)
	t.Run("break-glass-credentials", func(t *testing.T) {
		hostedControlPlaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		clientForCertKey := func(t *testing.T, root *rest.Config, crt, key []byte) *kubernetes.Clientset {
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

		validateCertificateAuth := func(t *testing.T, root *rest.Config, crt, key []byte, usernameValid func(string) bool) {
			t.Log("validating that the client certificate provides the appropriate access")

			breakGlassTenantClient := clientForCertKey(t, root, crt, key)

			t.Log("issuing SSR to identify the subject we are given using the client certificate")
			response, err := breakGlassTenantClient.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("could not send SSR: %v", err)
			}

			t.Log("ensuring that the SSR identifies the client certificate as having system:masters power and correct username")
			if !sets.New[string](response.Status.UserInfo.Groups...).Has("system:masters") ||
				!usernameValid(response.Status.UserInfo.Username) {
				t.Fatalf("did not get correct SSR response: %#v", response)
			}
		}

		t.Run("direct fetch", func(t *testing.T) {
			clientCertificate := pkimanifests.CustomerSystemAdminClientCertSecret(hostedControlPlaneNamespace)
			t.Logf("Grabbing customer break-glass credentials from client certificate secret %s/%s", clientCertificate.Namespace, clientCertificate.Name)
			if err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 3*time.Minute, true, func(ctx context.Context) (done bool, err error) {
				getErr := mgmt.CRClient.Get(ctx, controllerruntimeclient.ObjectKeyFromObject(clientCertificate), clientCertificate)
				if apierrors.IsNotFound(getErr) {
					return false, nil
				}
				return getErr == nil, err
			}); err != nil {
				t.Fatalf("client cert didn't become available: %v", err)
			}

			validateCertificateAuth(t, guest.Cfg, clientCertificate.Data["tls.crt"], clientCertificate.Data["tls.key"], func(s string) bool {
				return strings.HasPrefix(s, certificates.CommonNamePrefix(certificates.CustomerBreakGlassSigner))
			})
		})

		t.Run("CSR flow", func(t *testing.T) {
			csrName := hostedControlPlaneNamespace
			signerName := certificates.SignerNameForHC(hostedCluster, certificates.CustomerBreakGlassSigner)
			t.Logf("creating CSR %q for signer %q, requesting client auth usages", csrName, signerName)
			csrCfg := certificatesv1applyconfigurations.CertificateSigningRequest(csrName)
			csrCfg.Spec = certificatesv1applyconfigurations.CertificateSigningRequestSpec().
				WithSignerName(signerName).
				WithRequest(csr...).
				WithUsages(certificatesv1.UsageClientAuth)
			if _, err := mgmt.KubeClient.CertificatesV1().CertificateSigningRequests().Apply(ctx, csrCfg, metav1.ApplyOptions{FieldManager: "e2e-test"}); err != nil {
				t.Fatalf("failed to create CSR: %v", err)
			}

			t.Logf("creating CSRA %s/%s to trigger automatic approval of the CSR", hostedControlPlaneNamespace, csrName)
			csraCfg := certificatesv1alpha1applyconfigurations.CertificateSigningRequestApproval(hostedControlPlaneNamespace, csrName)
			if _, err := mgmt.HyperShiftClient.CertificatesV1alpha1().CertificateSigningRequestApprovals(hostedControlPlaneNamespace).Apply(ctx, csraCfg, metav1.ApplyOptions{FieldManager: "e2e-test"}); err != nil {
				t.Fatalf("failed to create CSRA: %v", err)
			}

			t.Logf("waiting for CSR %q to be approved and signed", csrName)
			var signedCrt []byte
			var lastResourceVersion string
			lastTimestamp := time.Now()
			if err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 10*time.Minute, true, func(ctx context.Context) (done bool, err error) {
				csr, err := mgmt.KubeClient.CertificatesV1().CertificateSigningRequests().Get(ctx, csrName, metav1.GetOptions{})
				if apierrors.IsNotFound(err) {
					t.Logf("CSR %q does not exist yet", csrName)
					return false, nil
				}
				if err != nil && !apierrors.IsNotFound(err) {
					return true, err
				}
				if csr.ObjectMeta.ResourceVersion != lastResourceVersion {
					t.Logf("CSR %q observed at RV %s after %s", csrName, csr.ObjectMeta.ResourceVersion, time.Since(lastTimestamp))
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
					lastTimestamp = time.Now()
				}

				if csr != nil && csr.Status.Certificate != nil {
					signedCrt = csr.Status.Certificate
					return true, nil
				}

				return false, nil
			}); err != nil {
				t.Fatalf("never saw CSR fulfilled: %v", err)
			}

			if len(signedCrt) == 0 {
				t.Fatal("got a zero-length signed cert back")
			}

			validateCertificateAuth(t, guest.Cfg, signedCrt, key, func(s string) bool {
				return s == framework.CommonName()
			})

			t.Run("revocation", func(t *testing.T) {
				crrName := "customer-break-glass-revocation"
				t.Logf("creating CRR %s/%s to trigger signer certificate revocation", hostedControlPlaneNamespace, crrName)
				crrCfg := certificatesv1alpha1applyconfigurations.CertificateRevocationRequest(crrName, hostedControlPlaneNamespace).
					WithSpec(certificatesv1alpha1applyconfigurations.CertificateRevocationRequestSpec().WithSignerClass(string(certificates.CustomerBreakGlassSigner)))
				if _, err := mgmt.HyperShiftClient.CertificatesV1alpha1().CertificateRevocationRequests(hostedControlPlaneNamespace).Apply(ctx, crrCfg, metav1.ApplyOptions{FieldManager: "e2e-test"}); err != nil {
					t.Fatalf("failed to create CRR: %v", err)
				}

				t.Logf("waiting for CRR %s/%s to be fulfilled", hostedControlPlaneNamespace, crrName)
				var lastResourceVersion string
				lastTimestamp := time.Now()
				if err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 10*time.Minute, true, func(ctx context.Context) (done bool, err error) {
					crr, err := mgmt.HyperShiftClient.CertificatesV1alpha1().CertificateRevocationRequests(hostedControlPlaneNamespace).Get(ctx, crrName, metav1.GetOptions{})
					if apierrors.IsNotFound(err) {
						t.Logf("CRR %q does not exist yet", csrName)
						return false, nil
					}
					if err != nil && !apierrors.IsNotFound(err) {
						return true, err
					}
					var complete bool
					if crr.ObjectMeta.ResourceVersion != lastResourceVersion {
						t.Logf("CRR observed at RV %s after %s", crr.ObjectMeta.ResourceVersion, time.Since(lastTimestamp))
						for _, condition := range crr.Status.Conditions {
							msg := fmt.Sprintf("%s=%s", condition.Type, condition.Status)
							if condition.Reason != "" {
								msg += ": " + condition.Reason
							}
							if condition.Message != "" {
								msg += "(" + condition.Message + ")"
							}
							t.Logf("CRR status: %s", msg)
							if condition.Type == certificatesv1alpha1.PreviousCertificatesRevokedType && condition.Status == metav1.ConditionTrue {
								complete = true
							}
						}
						lastResourceVersion = crr.ObjectMeta.ResourceVersion
						lastTimestamp = time.Now()
					}
					if complete {
						t.Log("CRR complete")
						return true, nil
					}
					return false, nil
				}); err != nil {
					t.Fatalf("never saw CRR complete: %v", err)
				}

				t.Logf("creating a client using the a certificate from the revoked signer")
				previousCertClient := clientForCertKey(t, guest.Cfg, signedCrt, key)

				time.Sleep(4 * time.Second)

				t.Log("issuing SSR to confirm that we're not authorized to contact the server")
				response, err := previousCertClient.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
				if !apierrors.IsUnauthorized(err) {
					t.Fatalf("expected an unauthorized error, got %v, response %#v", err, response)
				}
			})
		})
	})
}
