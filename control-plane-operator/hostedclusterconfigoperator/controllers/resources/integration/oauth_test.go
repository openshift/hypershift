package integration

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/internal/testtrace"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	hccomanifests "github.com/openshift/hypershift/pkg/manifests/hcco"

	oauthv1 "github.com/openshift/api/oauth/v1"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func oauthProjectionObjects() []client.Object {
	return []client.Object{
		manifests.OAuthCABundle(),
		manifests.OAuthServerBrowserClient(),
		hccomanifests.OAuthServerChallengingClient(),
		manifests.OAuthServerCLIClient(),
		manifests.OAuthServingCertRole(),
		manifests.OAuthServingCertRoleBinding(),
	}
}

func TestReconcileOAuthProjections(t *testing.T) {
	for _, tc := range []struct {
		name                string
		address             string
		port                int32
		expectedBrowser     string
		expectedChallenging string
	}{
		{
			name:                "When OAuth is enabled with a DNS address, it should project the CA bundle, clients and RBAC in order",
			address:             "oauth.example.com",
			port:                443,
			expectedBrowser:     "https://oauth.example.com:443/oauth/token/display",
			expectedChallenging: "https://oauth.example.com:443/oauth/token/implicit",
		},
		{
			name:                "When OAuth is enabled with an IPv6 address, it should bracket the host in redirect URIs",
			address:             "fd00::1",
			port:                6443,
			expectedBrowser:     "https://[fd00::1]:6443/oauth/token/display",
			expectedChallenging: "https://[fd00::1]:6443/oauth/token/implicit",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			run := newProjectionRun(projectionFixture{controlPlane: controlPlaneInputs()})
			params := enabledParams()
			params.OAuthAddress, params.OAuthPort = tc.address, tc.port

			errs := reconcileOAuthProjections(t.Context(), run.hosted, run.controlPlane, params)
			operations := run.trace.Operations()

			g.Expect(errs).To(BeEmpty())
			caBundle := manifests.OAuthCABundle()
			g.Expect(run.hostedClient.Get(t.Context(), client.ObjectKeyFromObject(caBundle), caBundle)).To(Succeed())
			g.Expect(caBundle.Data).To(Equal(map[string]string{"ca-bundle.crt": "oauth-master-ca"}))
			browser, challenging, cli := manifests.OAuthServerBrowserClient(), hccomanifests.OAuthServerChallengingClient(), manifests.OAuthServerCLIClient()
			for _, oauthClient := range []*oauthv1.OAuthClient{browser, challenging, cli} {
				g.Expect(run.hostedClient.Get(t.Context(), client.ObjectKeyFromObject(oauthClient), oauthClient)).To(Succeed())
			}
			g.Expect(browser.RedirectURIs).To(Equal([]string{tc.expectedBrowser}))
			g.Expect(browser.Secret).NotTo(BeEmpty())
			g.Expect(challenging.RedirectURIs).To(Equal([]string{tc.expectedChallenging}))
			g.Expect(challenging.RespondWithChallenges).To(BeTrue())
			g.Expect(cli.RedirectURIs).To(Equal([]string{"http://127.0.0.1/callback", "http://[::1]/callback"}))
			role, roleBinding := manifests.OAuthServingCertRole(), manifests.OAuthServingCertRoleBinding()
			g.Expect(run.hostedClient.Get(t.Context(), client.ObjectKeyFromObject(role), role)).To(Succeed())
			g.Expect(run.hostedClient.Get(t.Context(), client.ObjectKeyFromObject(roleBinding), roleBinding)).To(Succeed())
			g.Expect(roleBinding.RoleRef).To(Equal(rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: role.Name}))

			var creates []string
			for _, operation := range operations {
				if strings.HasPrefix(operation, "create ") {
					creates = append(creates, operation)
				}
			}
			g.Expect(creates).To(Equal([]string{
				fmt.Sprintf("create hosted %s", testtrace.ObjectKey(manifests.OAuthCABundle())),
				fmt.Sprintf("create hosted %s", testtrace.ObjectKey(manifests.OAuthServerBrowserClient())),
				fmt.Sprintf("create hosted %s", testtrace.ObjectKey(hccomanifests.OAuthServerChallengingClient())),
				fmt.Sprintf("create hosted %s", testtrace.ObjectKey(manifests.OAuthServerCLIClient())),
				fmt.Sprintf("create hosted %s", testtrace.ObjectKey(manifests.OAuthServingCertRole())),
				fmt.Sprintf("create hosted %s", testtrace.ObjectKey(manifests.OAuthServingCertRoleBinding())),
			}))
			g.Expect(run.trace.Cluster("control-plane")).To(Equal([]string{"get control-plane " + testtrace.ObjectKey(oauthMasterCABundle())}))
		})
	}

	t.Run("When OAuth is disabled, it should neither read the control plane nor project anything", func(t *testing.T) {
		g := NewWithT(t)
		run := newProjectionRun(projectionFixture{controlPlane: controlPlaneInputs()})

		errs := reconcileOAuthProjections(t.Context(), run.hosted, run.controlPlane, disabledParams())

		g.Expect(errs).To(BeNil())
		g.Expect(run.trace.Operations()).To(BeEmpty())
	})

	t.Run("When the control-plane OAuth master CA is missing, it should report it and still project clients and RBAC", func(t *testing.T) {
		g := NewWithT(t)
		run := newProjectionRun(projectionFixture{controlPlane: without(controlPlaneInputs(), oauthMasterCABundle())})

		errs := reconcileOAuthProjections(t.Context(), run.hosted, run.controlPlane, enabledParams())

		g.Expect(errorMessages(errs)).To(Equal([]string{
			`failed to reconcile oauth serving cert CA bundle: cannot get oauth master ca bundle: configmaps "oauth-master-ca-bundle" not found`,
		}))
		expectAbsent(g, run.hostedClient, manifests.OAuthCABundle())
		for _, object := range oauthProjectionObjects()[1:] {
			g.Expect(run.hostedClient.Get(t.Context(), client.ObjectKeyFromObject(object), object)).To(Succeed())
		}
	})

	t.Run("When hosted writes fail, it should attempt every projection and return each wrapped failure in order", func(t *testing.T) {
		g := NewWithT(t)
		failures := map[string]error{}
		for _, object := range oauthProjectionObjects() {
			failures[testtrace.ObjectKey(object)] = fmt.Errorf("%s failure", object.GetName())
		}
		run := newProjectionRun(projectionFixture{controlPlane: controlPlaneInputs(), upsertFailures: failures})

		errs := reconcileOAuthProjections(t.Context(), run.hosted, run.controlPlane, enabledParams())

		g.Expect(errorMessages(errs)).To(Equal([]string{
			"failed to reconcile oauth serving cert CA bundle: failed to reconcile oauth server cert ca bundle: oauth-serving-cert failure",
			"failed to reconcile oauth browser client: openshift-browser-client failure",
			"failed to reconcile oauth challenging client: openshift-challenging-client failure",
			"failed to reconcile oauth cli client: openshift-cli-client failure",
			"failed to reconcile oauth serving cert role: system:openshift:oauth-servercert-trust failure",
			"failed to reconcile oauth serving cert rolebinding: system:openshift:oauth-servercert-trust failure",
		}))
		for _, err := range errs {
			g.Expect(errors.Unwrap(err)).NotTo(BeNil())
		}
	})
}

func TestReconcileOAuthServingCertCABundle(t *testing.T) {
	t.Run("When the control-plane OAuth master CA exists, it should project its ca.crt into the hosted bundle", func(t *testing.T) {
		g := NewWithT(t)
		existing := manifests.OAuthCABundle()
		existing.Data = map[string]string{"ca-bundle.crt": "stale", "extra": "removed"}
		run := newProjectionRun(projectionFixture{controlPlane: []client.Object{oauthMasterCABundle()}, hosted: []client.Object{existing}})

		g.Expect(reconcileOAuthServingCertCABundle(t.Context(), run.hosted, run.controlPlane, testNamespace)).To(Succeed())

		caBundle := manifests.OAuthCABundle()
		g.Expect(run.hostedClient.Get(t.Context(), client.ObjectKeyFromObject(caBundle), caBundle)).To(Succeed())
		g.Expect(caBundle.Data).To(Equal(map[string]string{"ca-bundle.crt": "oauth-master-ca"}))
	})

	t.Run("When the control-plane OAuth master CA is missing, it should wrap the not-found error without writing", func(t *testing.T) {
		g := NewWithT(t)
		run := newProjectionRun(projectionFixture{})

		err := reconcileOAuthServingCertCABundle(t.Context(), run.hosted, run.controlPlane, testNamespace)

		g.Expect(err).To(MatchError(`cannot get oauth master ca bundle: configmaps "oauth-master-ca-bundle" not found`))
		g.Expect(apierrors.IsNotFound(errors.Unwrap(err))).To(BeTrue())
		g.Expect(run.trace.Cluster("hosted")).To(BeEmpty())
	})

	t.Run("When the hosted write fails, it should wrap the write error", func(t *testing.T) {
		g := NewWithT(t)
		failure := errors.New("write failure")
		run := newProjectionRun(projectionFixture{controlPlane: []client.Object{oauthMasterCABundle()}, upsertFailures: map[string]error{testtrace.ObjectKey(manifests.OAuthCABundle()): failure}})

		err := reconcileOAuthServingCertCABundle(t.Context(), run.hosted, run.controlPlane, testNamespace)

		g.Expect(err).To(MatchError("failed to reconcile oauth server cert ca bundle: write failure"))
		g.Expect(errors.Is(err, failure)).To(BeTrue())
	})
}
