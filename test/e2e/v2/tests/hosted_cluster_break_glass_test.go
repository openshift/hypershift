//go:build e2ev2

package tests

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"
	pkimanifests "github.com/openshift/hypershift/control-plane-pki-operator/manifests"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

func RegisterHostedClusterBreakGlassTests(getTestCtx internal.TestContextGetter) {
	for _, signerClass := range []certificates.SignerClass{certificates.CustomerBreakGlassSigner, certificates.SREBreakGlassSigner} {
		signer := signerClass
		Context(string(signer), func() {
			It("should authenticate directly fetched credentials as a system administrator", func() {
				tc := getTestCtx()
				hc, err := tc.GetHostedCluster()
				Expect(err).NotTo(HaveOccurred())
				config, err := tc.GetHostedClusterRESTConfig(hc)
				Expect(err).NotTo(HaveOccurred())
				secret := pkimanifests.CustomerSystemAdminClientCertSecret(tc.ControlPlaneNamespace)
				if signer == certificates.SREBreakGlassSigner {
					secret = pkimanifests.SRESystemAdminClientCertSecret(tc.ControlPlaneNamespace)
				}
				Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(secret), secret)).To(Succeed())
				assertBreakGlassIdentity(tc, config, secret.Data[corev1.TLSCertKey], secret.Data[corev1.TLSPrivateKeyKey], certificates.CommonNamePrefix(signer), false)
			})
			It("should reject an invalid common name, issue a valid CSR and revoke its certificate", func() {
				tc := getTestCtx()
				hc, err := tc.GetHostedCluster()
				Expect(err).NotTo(HaveOccurred())
				config, err := tc.GetHostedClusterRESTConfig(hc)
				Expect(err).NotTo(HaveOccurred())
				invalidRequest, _, err := breakGlassRequest("invalid-name")
				Expect(err).NotTo(HaveOccurred())
				invalidCSR := createBreakGlassCSR(tc, hc, signer, invalidRequest)
				Eventually(func(g Gomega) {
					g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(invalidCSR), invalidCSR)).To(Succeed())
					g.Expect(invalidCSR.Status.Certificate).To(BeEmpty())
					g.Expect(invalidCSR.Status.Conditions).To(ContainElement(And(
						HaveField("Type", certificatesv1.CertificateFailed),
						HaveField("Status", corev1.ConditionTrue),
						HaveField("Reason", "SignerValidationFailure"))))
				}, 5*time.Minute, time.Second).Should(Succeed())
				certificate, key := issueBreakGlassCertificate(tc, hc, config, signer)
				revokeBreakGlassCertificate(tc, config, signer, certificate, key)
			})
		})
	}
	It("should preserve SRE access when customer credentials are revoked", func() {
		tc := getTestCtx()
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		config, err := tc.GetHostedClusterRESTConfig(hc)
		Expect(err).NotTo(HaveOccurred())
		customerCertificate, customerKey := issueBreakGlassCertificate(tc, hc, config, certificates.CustomerBreakGlassSigner)
		sreCertificate, sreKey := issueBreakGlassCertificate(tc, hc, config, certificates.SREBreakGlassSigner)
		revokeBreakGlassCertificate(tc, config, certificates.CustomerBreakGlassSigner, customerCertificate, customerKey)
		assertBreakGlassIdentity(tc, config, sreCertificate, sreKey, certificates.CommonNamePrefix(certificates.SREBreakGlassSigner)+"e2e-test", true)
	})
}

func breakGlassRequest(commonName string) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	request, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: commonName, Organization: []string{"system:masters"}}}, key)
	if err != nil {
		return nil, nil, err
	}
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: request}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}), nil
}

func createBreakGlassCSR(tc *internal.TestContext, hc *hyperv1.HostedCluster, signer certificates.SignerClass, request []byte) *certificatesv1.CertificateSigningRequest {
	csr := &certificatesv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "hypershift-break-glass-"},
		Spec:       certificatesv1.CertificateSigningRequestSpec{SignerName: certificates.SignerNameForHC(hc, signer), Request: request, Usages: []certificatesv1.KeyUsage{certificatesv1.UsageClientAuth}},
	}
	Expect(tc.MgmtClient.Create(tc.Context, csr)).To(Succeed())
	DeferCleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), 15*time.Minute)
		defer cancel()
		cleanupTC := *tc
		cleanupTC.Context = cleanupContext
		tc := &cleanupTC
		err := tc.MgmtClient.Delete(tc.Context, csr)
		if !apierrors.IsNotFound(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	})
	approval := &certificatesv1alpha1.CertificateSigningRequestApproval{ObjectMeta: metav1.ObjectMeta{Name: csr.Name, Namespace: tc.ControlPlaneNamespace}}
	Expect(tc.MgmtClient.Create(tc.Context, approval)).To(Succeed())
	DeferCleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), 15*time.Minute)
		defer cancel()
		cleanupTC := *tc
		cleanupTC.Context = cleanupContext
		tc := &cleanupTC
		err := tc.MgmtClient.Delete(tc.Context, approval)
		if !apierrors.IsNotFound(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	})
	return csr
}

func issueBreakGlassCertificate(tc *internal.TestContext, hc *hyperv1.HostedCluster, config *rest.Config, signer certificates.SignerClass) ([]byte, []byte) {
	commonName := certificates.CommonNamePrefix(signer) + "e2e-test"
	request, key, err := breakGlassRequest(commonName)
	Expect(err).NotTo(HaveOccurred())
	csr := createBreakGlassCSR(tc, hc, signer, request)
	Eventually(func(g Gomega) {
		g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(csr), csr)).To(Succeed())
		g.Expect(csr.Status.Certificate).NotTo(BeEmpty())
	}, 5*time.Minute, time.Second).Should(Succeed())
	assertBreakGlassIdentity(tc, config, csr.Status.Certificate, key, commonName, true)
	return csr.Status.Certificate, key
}

func breakGlassClient(config *rest.Config, certificate, key []byte) (*kubernetes.Clientset, error) {
	if config == nil || len(certificate) == 0 || len(key) == 0 {
		return nil, fmt.Errorf("break-glass client requires an API config, certificate and private key")
	}
	certificateConfig := rest.AnonymousClientConfig(config)
	certificateConfig.CertData = certificate
	certificateConfig.KeyData = key
	certificateConfig.Timeout = 10 * time.Second
	return kubernetes.NewForConfig(certificateConfig)
}

func assertBreakGlassIdentity(tc *internal.TestContext, config *rest.Config, certificate, key []byte, username string, exact bool) {
	Expect(certificate).NotTo(BeEmpty())
	Expect(key).NotTo(BeEmpty())
	client, err := breakGlassClient(config, certificate, key)
	Expect(err).NotTo(HaveOccurred())
	Eventually(func(g Gomega) {
		response, err := client.AuthenticationV1().SelfSubjectReviews().Create(tc.Context, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(response.Status.UserInfo.Groups).To(ContainElement("system:masters"))
		if exact {
			g.Expect(response.Status.UserInfo.Username).To(Equal(username))
		} else {
			g.Expect(strings.HasPrefix(response.Status.UserInfo.Username, username)).To(BeTrue())
		}
	}, 3*time.Minute, time.Second).Should(Succeed())
}

func revokeBreakGlassCertificate(tc *internal.TestContext, config *rest.Config, signer certificates.SignerClass, certificate, key []byte) {
	request := &certificatesv1alpha1.CertificateRevocationRequest{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "hypershift-break-glass-", Namespace: tc.ControlPlaneNamespace},
		Spec:       certificatesv1alpha1.CertificateRevocationRequestSpec{SignerClass: string(signer)},
	}
	Expect(tc.MgmtClient.Create(tc.Context, request)).To(Succeed())
	DeferCleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), 15*time.Minute)
		defer cancel()
		cleanupTC := *tc
		cleanupTC.Context = cleanupContext
		tc := &cleanupTC
		err := tc.MgmtClient.Delete(tc.Context, request)
		if !apierrors.IsNotFound(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	})
	Eventually(func(g Gomega) {
		g.Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(request), request)).To(Succeed())
		g.Expect(meta.IsStatusConditionTrue(request.Status.Conditions, certificatesv1alpha1.PreviousCertificatesRevokedType)).To(BeTrue())
	}, 10*time.Minute, time.Second).Should(Succeed())
	client, err := breakGlassClient(config, certificate, key)
	Expect(err).NotTo(HaveOccurred())
	Eventually(func() bool {
		_, err := client.AuthenticationV1().SelfSubjectReviews().Create(tc.Context, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
		return apierrors.IsUnauthorized(err)
	}, 3*time.Minute, time.Second).Should(BeTrue(), "revoked credentials must be rejected by the hosted API")
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:BreakGlass] Hosted Cluster Break Glass Lifecycle", Label("hosted-cluster-break-glass"), Serial, func() {
	BeforeEach(func() {
		tc := internal.GetTestContext()
		skipIfNativeOriginBelow(tc, semver.MustParse("4.15.0"))
		tc.SkipIfPlatform(hyperv1.KubevirtPlatform)
		tc.SkipIfPlatform(hyperv1.GCPPlatform)
	})
	RegisterHostedClusterBreakGlassTests(internal.GetTestContext)
})

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:BreakGlass] Highly Available Hosted Cluster Break Glass Lifecycle", Label("hosted-cluster-break-glass-ha"), Serial, func() {
	BeforeEach(func() {
		tc := internal.GetTestContext()
		skipIfNativeOriginBelow(tc, semver.MustParse("4.15.0"))
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		Expect(hc.Spec.ControllerAvailabilityPolicy).To(Equal(hyperv1.HighlyAvailable), "HA break-glass fixture must have three API replicas and zero workers")
		deployment := &appsv1.Deployment{}
		Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: "kube-apiserver"}, deployment)).To(Succeed())
		Expect(deployment.Status.ReadyReplicas).To(Equal(int32(3)))
		client, err := tc.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())
		nodes := &corev1.NodeList{}
		Expect(client.List(tc.Context, nodes)).To(Succeed())
		Expect(nodes.Items).To(BeEmpty(), "HA break-glass scenario requires zero workers")
	})
	RegisterHostedClusterBreakGlassTests(internal.GetTestContext)
})
