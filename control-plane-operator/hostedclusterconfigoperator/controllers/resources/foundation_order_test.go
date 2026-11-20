package resources

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/globalconfig"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"

	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type foundationUpserter struct {
	upsert.CreateOrUpdateFN
}

func (provider foundationUpserter) CreateOrUpdate(ctx context.Context, guest client.Client, obj client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return provider.CreateOrUpdateFN(ctx, guest, obj, mutate)
}

func TestReconcile(t *testing.T) {
	t.Run("When foundation phases fail, it should preserve global interleaving and wrapped error order", func(t *testing.T) {
		assert := NewWithT(t)
		keys := []string{
			"*v1.CustomResourceDefinition/apirequestcounts.apiserver.openshift.io",
			"*v1.ClusterVersion/version",
			"*v1.ClusterOperator/openshift-apiserver",
			"*v1.Image/cluster",
			"*v1.Namespace/openshift-apiserver",
		}
		failures := make(map[string]error)
		for index, key := range keys {
			failures[key] = fmt.Errorf("foundation failure %d", index)
		}
		var attempts []string
		var operations []string
		hcp := fakeHCP()
		hcp.Generation = 7
		hcp.Spec.Platform.Type = hyperv1.NonePlatform
		hcp.Spec.DNS = hyperv1.DNSSpec{BaseDomain: "example.com", BaseDomainPrefix: ptr.To("configured"), PublicZoneID: "public-zone", PrivateZoneID: "private-zone"}
		hcp.Spec.InfraID = "configured-infra"
		hcp.Spec.KubeAPIServerDNSName = "api.configured.example.com"
		hcp.Spec.InfrastructureAvailabilityPolicy = hyperv1.HighlyAvailable
		hcp.Spec.IssuerURL = "https://issuer.example.com"
		hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
			Proxy:     &configv1.ProxySpec{HTTPProxy: "http://proxy.example.com:8080", TrustedCA: configv1.ConfigMapNameReference{Name: "custom-ca"}},
			Ingress:   &configv1.IngressSpec{Domain: "apps.configured.example.com"},
			APIServer: &configv1.APIServerSpec{Audit: configv1.Audit{Profile: configv1.WriteRequestBodiesAuditProfileType}},
		}
		controlPlaneObjects := []client.Object{hcp, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "custom-ca", Namespace: hcp.Namespace}, Data: map[string]string{"ca-bundle.crt": "fixture"}}}
		for _, object := range cpObjects {
			if _, isHCP := object.(*hyperv1.HostedControlPlane); !isHCP {
				controlPlaneObjects = append(controlPlaneObjects, object.DeepCopyObject().(client.Object))
			}
		}
		recyclerFailure, buildFailure, projectFailure := errors.New("recycler failure"), errors.New("observed Build failure"), errors.New("observed Project failure")
		guestClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(initialObjects...).WithStatusSubresource(&configv1.Infrastructure{}).WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, target client.WithWatch, key client.ObjectKey, object client.Object, opts ...client.GetOption) error {
			operations = append(operations, fmt.Sprintf("get hosted %T %s/%s", object, key.Namespace, key.Name))
			return target.Get(ctx, key, object, opts...)
		}}).Build()
		controlPlaneClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(controlPlaneObjects...).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
		root := &reconciler{
			client:         guestClient,
			uncachedClient: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			cpClient:       controlPlaneClient,
			platformType:   hyperv1.NonePlatform, clusterSignerCA: "foobar", hcpName: "foo", hcpNamespace: "bar",
			releaseProvider:       &fakereleaseprovider.FakeReleaseProvider{Components: map[string]string{"cli": "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:cli-fake"}},
			ImageMetaDataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProviderHCCO{},
			CreateOrUpdateProvider: foundationUpserter{func(ctx context.Context, guest client.Client, obj client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				key := fmt.Sprintf("%T/%s", obj, obj.GetName())
				attempts = append(attempts, key)
				cluster := "hosted"
				if guest == controlPlaneClient {
					cluster = "control-plane"
				}
				operations = append(operations, fmt.Sprintf("upsert %s %T %s/%s", cluster, obj, obj.GetNamespace(), obj.GetName()))
				if cluster == "hosted" && client.ObjectKeyFromObject(obj) == client.ObjectKeyFromObject(manifests.RecyclerServiceAccount()) {
					if _, ok := obj.(*corev1.ServiceAccount); ok {
						return controllerutil.OperationResultNone, recyclerFailure
					}
				}
				if guest == controlPlaneClient {
					if obj.GetName() == globalconfig.ObservedBuildConfig(hcp.Namespace).Name {
						return controllerutil.OperationResultNone, buildFailure
					}
					if obj.GetName() == globalconfig.ObservedProjectConfig(hcp.Namespace).Name {
						return controllerutil.OperationResultNone, projectFailure
					}
				}
				if failure := failures[key]; failure != nil {
					return controllerutil.OperationResultNone, failure
				}
				return controllerutil.CreateOrUpdate(ctx, guest, obj, mutate)
			}},
		}
		_, err := root.Reconcile(t.Context(), controllerruntime.Request{})
		assert.Expect(err).To(HaveOccurred())
		for _, failure := range failures {
			assert.Expect(errors.Is(err, failure)).To(BeTrue())
		}
		expectedErrors := []string{
			"failed to reconcile crds: failed to reconcile request count crd: foundation failure 0",
			"failed to reconcile clusterversion: failed to reconcile clusterVersion: foundation failure 1",
			"failed to reconcile clusterOperators: failed to reconcile *v1.ClusterOperator openshift-apiserver: foundation failure 2",
			"failed to reconcile global configuration: failed to reconcile image config: foundation failure 3",
			"failed to reconcile namespaces: failed to reconcile namespace openshift-apiserver: foundation failure 4",
			"failed to reconcile pv recycler service account: recycler failure",
			"observed Build failure",
			"observed Project failure",
		}
		assert.Expect(err).To(MatchError("[" + strings.Join(expectedErrors, ", ") + "]"))
		for _, failure := range []error{recyclerFailure, buildFailure, projectFailure} {
			assert.Expect(errors.Is(err, failure)).To(BeTrue())
		}
		aggregate := func() utilerrors.Aggregate {
			var target utilerrors.Aggregate
			_ = errors.As(err, &target)
			return target
		}()
		assert.Expect(aggregate.Errors()).To(HaveLen(len(expectedErrors)))
		assert.Expect(aggregate.Errors()[len(expectedErrors)-2]).To(BeIdenticalTo(buildFailure))
		assert.Expect(aggregate.Errors()[len(expectedErrors)-1]).To(BeIdenticalTo(projectFailure))
		persistedHCP := &hyperv1.HostedControlPlane{}
		assert.Expect(controlPlaneClient.Get(t.Context(), client.ObjectKeyFromObject(hcp), persistedHCP)).To(Succeed())
		condition := meta.FindStatusCondition(persistedHCP.Status.Conditions, string(hyperv1.ConfigOperatorReconciliationSucceeded))
		assert.Expect(condition).NotTo(BeNil())
		assert.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		assert.Expect(condition.Reason).To(Equal(hyperv1.ReconcileErrorReason))
		assert.Expect(condition.Message).To(Equal(err.Error()))
		assert.Expect(condition.ObservedGeneration).To(Equal(hcp.Generation))
		assert.Expect(persistedHCP.Spec).To(Equal(hcp.Spec))
		infra, dns, ingress, proxy, auth, apiServer := globalconfig.InfrastructureConfig(), globalconfig.DNSConfig(), globalconfig.IngressConfig(), globalconfig.ProxyConfig(), globalconfig.AuthenticationConfiguration(), globalconfig.APIServerConfiguration()
		for _, object := range []client.Object{infra, dns, ingress, proxy, auth, apiServer} {
			assert.Expect(guestClient.Get(t.Context(), client.ObjectKeyFromObject(object), object)).To(Succeed())
		}
		assert.Expect(infra.Status.InfrastructureName).To(Equal("configured-infra"))
		assert.Expect(infra.Status.APIServerURL).To(Equal("https://api.configured.example.com:1234"))
		assert.Expect(infra.Status.InfrastructureTopology).To(Equal(configv1.HighlyAvailableTopologyMode))
		assert.Expect(dns.Spec.BaseDomain).To(Equal("configured.example.com"))
		assert.Expect(dns.Spec.PublicZone.ID).To(Equal("public-zone"))
		assert.Expect(dns.Spec.PrivateZone.ID).To(Equal("private-zone"))
		assert.Expect(ingress.Spec).To(Equal(*hcp.Spec.Configuration.Ingress))
		assert.Expect(proxy.Spec).To(Equal(*hcp.Spec.Configuration.Proxy))
		assert.Expect(auth.Spec.ServiceAccountIssuer).To(Equal(hcp.Spec.IssuerURL))
		assert.Expect(apiServer.Spec).To(Equal(*hcp.Spec.Configuration.APIServer))
		phases := []string{
			keys[0], "*v1.Endpoints/kubernetes", "*v1.ValidatingAdmissionPolicy/", "*v1.ConfigMap/openshift-install", "*v1.PrometheusRule/",
			keys[1], keys[2], "*v1.ClusterOperator/operator-lifecycle-manager-packageserver", keys[3], "*v1.Ingress/cluster", keys[4], "*v1.Namespace/openshift-route-controller-manager", "*v1.ClusterRole/",
		}
		previous := -1
		for _, phase := range phases {
			found := -1
			for index, attempt := range attempts {
				if strings.HasPrefix(attempt, phase) {
					found = index
					break
				}
			}
			assert.Expect(found).To(BeNumerically(">", previous), "phase %s; attempts: %v", phase, attempts)
			previous = found
		}
		orderedOperations := []string{
			"upsert hosted *v1.Secret openshift-config/pull-secret",
			"upsert hosted *v1.Secret openshift/pull-secret",
			fmt.Sprintf("upsert hosted *v1.ConfigMap %s/%s", manifests.OpenShiftUserCABundle().Namespace, manifests.OpenShiftUserCABundle().Name),
			fmt.Sprintf("upsert hosted *v1.ConfigMap %s/%s", manifests.OAuthCABundle().Namespace, manifests.OAuthCABundle().Name),
			fmt.Sprintf("upsert hosted *v1.Storage /%s", manifests.Storage().Name),
			fmt.Sprintf("upsert hosted *v1.ServiceAccount %s/%s", manifests.RecyclerServiceAccount().Namespace, manifests.RecyclerServiceAccount().Name),
			"get hosted *v1.Build /cluster",
			fmt.Sprintf("upsert control-plane *v1.ConfigMap %s/%s", hcp.Namespace, globalconfig.ObservedBuildConfig(hcp.Namespace).Name),
			"get hosted *v1.Project /cluster",
			fmt.Sprintf("upsert control-plane *v1.ConfigMap %s/%s", hcp.Namespace, globalconfig.ObservedProjectConfig(hcp.Namespace).Name),
		}
		previous = -1
		for _, operation := range orderedOperations {
			found := -1
			for index, attempt := range operations {
				if attempt == operation {
					found = index
					break
				}
			}
			assert.Expect(found).To(BeNumerically(">", previous), "operation %s; trace: %v", operation, operations)
			previous = found
		}
	})

	t.Run("When control-plane integration projections run or fail, it should keep their phase positions and individual root aggregate entries", func(t *testing.T) {
		assert := NewWithT(t)
		newRoot := func(failures map[string]error) (*reconciler, client.Client, *[]string) {
			var attempts []string
			controlPlaneObjects := make([]client.Object, 0, len(cpObjects))
			for _, object := range cpObjects {
				controlPlaneObjects = append(controlPlaneObjects, object.DeepCopyObject().(client.Object))
			}
			controlPlaneClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(controlPlaneObjects...).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
			return &reconciler{
				client:         fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(initialObjects...).WithStatusSubresource(&configv1.Infrastructure{}).Build(),
				uncachedClient: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
				cpClient:       controlPlaneClient,
				platformType:   hyperv1.NonePlatform, clusterSignerCA: "foobar", hcpName: "foo", hcpNamespace: "bar",
				oauthAddress: "oauth.example.com", oauthPort: 443,
				releaseProvider:       &fakereleaseprovider.FakeReleaseProvider{Components: map[string]string{"cli": "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:cli-fake"}},
				ImageMetaDataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProviderHCCO{},
				CreateOrUpdateProvider: foundationUpserter{func(ctx context.Context, target client.Client, obj client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
					cluster := "hosted"
					if target == controlPlaneClient {
						cluster = "control-plane"
					}
					key := fmt.Sprintf("%T %s/%s", obj, obj.GetNamespace(), obj.GetName())
					attempts = append(attempts, fmt.Sprintf("upsert %s %s", cluster, key))
					if failure := failures[key]; failure != nil {
						return controllerutil.OperationResultNone, failure
					}
					return controllerutil.CreateOrUpdate(ctx, target, obj, mutate)
				}},
			}, controlPlaneClient, &attempts
		}
		aggregateErrors := func(err error) []error {
			if err == nil {
				return nil
			}
			var aggregate utilerrors.Aggregate
			assert.Expect(errors.As(err, &aggregate)).To(BeTrue())
			return aggregate.Errors()
		}

		baseline, _, baselineAttempts := newRoot(nil)
		_, baselineErr := baseline.Reconcile(t.Context(), controllerruntime.Request{})
		baselineErrors := aggregateErrors(baselineErr)
		assert.Expect(integrationErrors(baselineErrors)).To(BeEmpty())

		ordered := []string{
			"upsert hosted *v1.Secret openshift-kube-apiserver-operator/kube-control-plane-signer",
			"upsert hosted *v1.ConfigMap openshift-config-managed/kubelet-serving-ca",
			"upsert hosted *v1.DaemonSet kube-system/konnectivity-agent",
			"upsert hosted *v1.Deployment kube-system/kas-connection-checker",
		}
		for _, group := range manifests.OpenShiftAPIServerAPIServiceGroups() {
			ordered = append(ordered, fmt.Sprintf("upsert hosted *v1.APIService /v1.%s.openshift.io", group))
		}
		ordered = append(ordered, "upsert hosted *v1.Service default/openshift-apiserver", "upsert hosted *v1.Endpoints default/openshift-apiserver")
		for _, group := range manifests.OpenShiftOAuthAPIServerAPIServiceGroups() {
			ordered = append(ordered, fmt.Sprintf("upsert hosted *v1.APIService /v1.%s.openshift.io", group))
		}
		ordered = append(ordered,
			"upsert hosted *v1.Service default/openshift-oauth-apiserver",
			"upsert hosted *v1.Endpoints default/openshift-oauth-apiserver",
			"upsert hosted *v1.Secret kube-system/kubeadmin",
			"upsert control-plane *v1.Secret bar/kubeadmin-password",
			"upsert hosted *v1.ServiceMonitor openshift-kube-apiserver/openshift-kube-apiserver",
			"upsert hosted *v1.Network /cluster",
			"upsert hosted *v1.Secret openshift/pull-secret",
			"upsert hosted *v1.ConfigMap openshift-config-managed/oauth-serving-cert",
			"upsert hosted *v1.OAuthClient /openshift-browser-client",
			"upsert hosted *v1.OAuthClient /openshift-challenging-client",
			"upsert hosted *v1.OAuthClient /openshift-cli-client",
			"upsert hosted *v1.Role openshift-config-managed/system:openshift:oauth-servercert-trust",
			"upsert hosted *v1.RoleBinding openshift-config-managed/system:openshift:oauth-servercert-trust",
			"upsert hosted *v1.ConfigMap openshift-controller-manager/openshift-service-ca",
		)
		previous := -1
		for _, expected := range ordered {
			found := slices.Index((*baselineAttempts)[previous+1:], expected)
			assert.Expect(found).To(BeNumerically(">=", 0), "attempt %q after index %d; attempts: %v", expected, previous, *baselineAttempts)
			previous += found + 1
		}

		apiServiceFailure, endpointsFailure, hashFailure, browserFailure, roleFailure := errors.New("apiservice failure"), errors.New("endpoints failure"), errors.New("hash failure"), errors.New("browser failure"), errors.New("role failure")
		failing, controlPlaneClient, _ := newRoot(map[string]error{
			"*v1.APIService /v1.apps.openshift.io":                                      apiServiceFailure,
			"*v1.Endpoints default/openshift-apiserver":                                 endpointsFailure,
			"*v1.Secret kube-system/kubeadmin":                                          hashFailure,
			"*v1.OAuthClient /openshift-browser-client":                                 browserFailure,
			"*v1.Role openshift-config-managed/system:openshift:oauth-servercert-trust": roleFailure,
		})
		_, err := failing.Reconcile(t.Context(), controllerruntime.Request{})
		assert.Expect(err).To(HaveOccurred())
		failingErrors := aggregateErrors(err)
		assert.Expect(errorMessages(integrationErrors(failingErrors))).To(Equal([]string{
			"failed to reconcile openshift apiserver service: failed to reconcile openshift apiserver apiservice (apps): apiservice failure",
			"failed to reconcile openshift apiserver endpoints: endpoints failure",
			"failed to reconcile kubeadmin password hash secret: hash failure",
			"failed to reconcile oauth browser client: browser failure",
			"failed to reconcile oauth serving cert role: role failure",
		}))
		assert.Expect(failingErrors).To(HaveLen(len(baselineErrors) + 5))
		for _, failure := range []error{apiServiceFailure, endpointsFailure, hashFailure, browserFailure, roleFailure} {
			assert.Expect(errors.Is(err, failure)).To(BeTrue())
		}
		persistedHCP := &hyperv1.HostedControlPlane{}
		assert.Expect(controlPlaneClient.Get(t.Context(), client.ObjectKeyFromObject(fakeHCP()), persistedHCP)).To(Succeed())
		condition := meta.FindStatusCondition(persistedHCP.Status.Conditions, string(hyperv1.ConfigOperatorReconciliationSucceeded))
		assert.Expect(condition).NotTo(BeNil())
		assert.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		assert.Expect(condition.Message).To(Equal(err.Error()))
	})
}
