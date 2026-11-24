//go:build envtest

package envtest

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/hypershift-operator/controllers/webhookcerts"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	dto "github.com/prometheus/client_model/go"
)

const (
	webhookCertsControllerName   = "webhookcerts"
	webhookCertsOperatorService  = "operator"
	webhookCertsInjectAnnotation = "service.beta.openshift.io/inject-cabundle"
	webhookCertsKeepAnnotation   = "example.com/keep"
)

// newWebhookCertsConversionCRD returns a minimal CRD whose conversion webhook targets the given Service.
func newWebhookCertsConversionCRD(group, plural, kind, svcNamespace, svcName string, caBundle []byte) *apiextensionsv1.CustomResourceDefinition {
	schema := &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{Type: "object"}}
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name:        plural + "." + group,
			Annotations: map[string]string{webhookCertsInjectAnnotation: "true", webhookCertsKeepAnnotation: "true"},
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: plural, Kind: kind},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{Name: "v1beta1", Served: true, Storage: true, Schema: schema},
				{Name: "v1beta2", Served: true, Storage: false, Schema: schema},
			},
			Conversion: &apiextensionsv1.CustomResourceConversion{
				Strategy: apiextensionsv1.WebhookConverter,
				Webhook: &apiextensionsv1.WebhookConversion{
					ClientConfig: &apiextensionsv1.WebhookClientConfig{
						CABundle: caBundle,
						Service: &apiextensionsv1.ServiceReference{
							Namespace: svcNamespace,
							Name:      svcName,
							Port:      ptr.To[int32](443),
							Path:      ptr.To("/convert"),
						},
					},
					ConversionReviewVersions: []string{"v1"},
				},
			},
		},
	}
}

// webhookCertsControllerActivity returns whether the webhookcerts controller has an empty work queue and no
// reconcile in progress, and how many reconciles it has completed, using the controller-runtime metrics of the
// running manager. The work queue reports one depth series per priority, so all of them are added up.
// Missing metrics are reported as not idle.
func webhookCertsControllerActivity() (idle bool, reconciles float64, err error) {
	families, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		return false, 0, err
	}
	hasLabel := func(m *dto.Metric, name, value string) bool {
		for _, l := range m.GetLabel() {
			if l.GetName() == name && l.GetValue() == value {
				return true
			}
		}
		return false
	}
	var depth, activeWorkers float64
	var foundDepth, foundActiveWorkers bool
	for _, family := range families {
		for _, m := range family.GetMetric() {
			switch family.GetName() {
			case "workqueue_depth":
				if hasLabel(m, "name", webhookCertsControllerName) {
					depth += m.GetGauge().GetValue()
					foundDepth = true
				}
			case "controller_runtime_active_workers":
				if hasLabel(m, "controller", webhookCertsControllerName) {
					activeWorkers += m.GetGauge().GetValue()
					foundActiveWorkers = true
				}
			case "controller_runtime_reconcile_total":
				if hasLabel(m, "controller", webhookCertsControllerName) {
					reconciles += m.GetCounter().GetValue()
				}
			}
		}
	}
	return foundDepth && foundActiveWorkers && depth == 0 && activeWorkers == 0, reconciles, nil
}

// getWebhookCertsCRD returns the current state of the named CRD.
func getWebhookCertsCRD(g Gomega, c crclient.Client, name string) *apiextensionsv1.CustomResourceDefinition {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	g.Expect(c.Get(ctx, crclient.ObjectKey{Name: name}, crd)).To(Succeed())
	return crd
}

var _ = Describe("Webhook certs CRD conversion caBundle reconciliation", Label("webhookcerts"), func() {
	It("When a CRD targeting the operator service drifts after the controller has settled, it should be repaired through the CRD watch", func() {
		scheme := runtime.NewScheme()
		Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
		Expect(apiextensionsv1.AddToScheme(scheme)).To(Succeed())
		c, err := crclient.New(cfg, crclient.Options{Scheme: scheme})
		Expect(err).NotTo(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "webhookcerts-watch-"}}
		Expect(c.Create(ctx, ns)).To(Succeed())
		DeferCleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			Expect(crclient.IgnoreNotFound(c.Delete(cleanupCtx, ns))).To(Succeed(), "failed to delete test namespace %s", ns.Name)
		})

		By("creating the webhook CA and serving cert Secrets as the operator bootstrap does")
		Expect(webhookcerts.EnsureWebhookCerts(ctx, c, ns.Name, webhookCertsOperatorService)).To(Succeed())
		caSecret := &corev1.Secret{}
		Expect(c.Get(ctx, crclient.ObjectKey{Namespace: ns.Name, Name: webhookcerts.CASecretName}, caSecret)).To(Succeed())
		webhookCA := caSecret.Data[certs.CASignerCertMapKey]
		Expect(webhookCA).NotTo(BeEmpty())
		_, _, foreignCA, err := webhookcerts.GenerateInitialWebhookCerts("openshift-service-ca", "service-ca")
		Expect(err).NotTo(HaveOccurred())

		By("creating a CRD served by the operator Service and a CRD served by another Service, both with a stale caBundle")
		group := ns.Name + ".webhookcerts.hypershift.openshift.io"
		managed := newWebhookCertsConversionCRD(group, "widgets", "Widget", ns.Name, webhookCertsOperatorService, foreignCA)
		foreign := newWebhookCertsConversionCRD(group, "gadgets", "Gadget", ns.Name, "other-webhook-service", foreignCA)
		for _, crd := range []*apiextensionsv1.CustomResourceDefinition{managed, foreign} {
			Expect(c.Create(ctx, crd)).To(Succeed())
			DeferCleanup(func(name string) {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				Expect(crclient.IgnoreNotFound(c.Delete(cleanupCtx, &apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: name}}))).To(Succeed(),
					"failed to delete test CRD %s", name)
			}, crd.Name)
		}

		By("starting the webhookcerts controller in a manager")
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:                 scheme,
			Metrics:                metricsserver.Options{BindAddress: "0"},
			HealthProbeBindAddress: "0",
			Controller:             config.Controller{SkipNameValidation: ptr.To(true)},
		})
		Expect(err).NotTo(HaveOccurred())
		reconciler := &webhookcerts.WebhookCertReconciler{Namespace: ns.Name, ServiceName: webhookCertsOperatorService}
		Expect(reconciler.SetupWithManager(mgr, upsert.New(false))).To(Succeed())
		mgrCtx, stopManager := context.WithCancel(ctx)
		managerDone := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			managerDone <- mgr.Start(mgrCtx)
		}()
		// Registered last, so it runs first: stop the manager before the CRDs and namespace are deleted.
		DeferCleanup(func() {
			stopManager()
			Eventually(managerDone, time.Minute).Should(Receive(Not(HaveOccurred())))
		})

		expectRepaired := func(g Gomega, crd *apiextensionsv1.CustomResourceDefinition) {
			g.Expect(crd.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(webhookCA))
			g.Expect(crd.Annotations).NotTo(HaveKey(webhookCertsInjectAnnotation))
			g.Expect(crd.Annotations).To(HaveKeyWithValue(webhookCertsKeepAnnotation, "true"))
		}

		By("waiting for the startup reconciliation to repair the CRD served by the operator Service")
		Eventually(func(g Gomega) { expectRepaired(g, getWebhookCertsCRD(g, c, managed.Name)) }, time.Minute, 100*time.Millisecond).Should(Succeed())

		By("waiting for the controller to stay idle so that no startup event or retry can repair the next change")
		lastReconciles, idleSince := -1.0, time.Time{}
		Eventually(func(g Gomega) {
			idle, reconciles, err := webhookCertsControllerActivity()
			g.Expect(err).NotTo(HaveOccurred())
			if !idle || reconciles != lastReconciles {
				lastReconciles, idleSince = reconciles, time.Now()
			}
			g.Expect(idle).To(BeTrue(), "the webhookcerts controller is still processing work")
			g.Expect(time.Since(idleSince)).To(BeNumerically(">=", time.Second), "the webhookcerts controller has not been idle for a second")
		}, time.Minute, 50*time.Millisecond).Should(Succeed())

		By("changing the caBundle and re-adding the injection annotation on the CRD served by the operator Service")
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			crd := &apiextensionsv1.CustomResourceDefinition{}
			if err := c.Get(ctx, crclient.ObjectKey{Name: managed.Name}, crd); err != nil {
				return err
			}
			crd.Annotations[webhookCertsInjectAnnotation] = "true"
			crd.Spec.Conversion.Webhook.ClientConfig.CABundle = foreignCA
			return c.Update(ctx, crd)
		})).To(Succeed())

		By("waiting for the running controller to repair the drifted CRD")
		Eventually(func(g Gomega) { expectRepaired(g, getWebhookCertsCRD(g, c, managed.Name)) }, 30*time.Second, 100*time.Millisecond).Should(Succeed(),
			"CRD %s was not repaired after drifting; the CRD watch did not trigger a reconciliation", managed.Name)

		By("checking that the CRD served by another Service was not modified")
		foreignAfter := getWebhookCertsCRD(Default, c, foreign.Name)
		Expect(foreignAfter.Spec.Conversion.Webhook.ClientConfig.CABundle).To(Equal(foreignCA))
		Expect(foreignAfter.Annotations).To(HaveKeyWithValue(webhookCertsInjectAnnotation, "true"))
	})
})
