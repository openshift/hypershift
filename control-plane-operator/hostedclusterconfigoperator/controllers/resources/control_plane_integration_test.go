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
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/internal/testtrace"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	hccomanifests "github.com/openshift/hypershift/pkg/manifests/hcco"
	pkgoauth "github.com/openshift/hypershift/pkg/oauth"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/releaseinfo"

	configv1 "github.com/openshift/api/config/v1"
	imageapi "github.com/openshift/api/image/v1"
	oauthv1 "github.com/openshift/api/oauth/v1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"k8s.io/utils/ptr"

	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type integrationFixture struct {
	hcp             *hyperv1.HostedControlPlane
	cpGetFailures   map[string]error
	upsertFailures  map[string]error
	oauthAddress    string
	oauthPort       int32
	removeFromCP    []client.Object
	replaceInCP     []client.Object
	additionalGuest []client.Object
}

type integrationRun struct {
	reconciler *reconciler
	hosted     client.WithWatch
	cp         client.WithWatch
	trace      *testtrace.Recorder
}

func integrationControlPlaneObjects() []client.Object {
	return []client.Object{
		fakeRootCASecret(),
		fakeOpenShiftAPIServerService(),
		fakeOpenShiftOAuthAPIServerService(),
		fakeKubeadminPasswordSecret(),
		fakeOAuthMasterCABundle(),
		fakeControlPlaneKonnectivityCAConfigMap(),
		fakeKonnectivityAgentSecret(),
	}
}

func integrationReleaseImage() *releaseinfo.ReleaseImage {
	return &releaseinfo.ReleaseImage{ImageStream: &imageapi.ImageStream{
		ObjectMeta: metav1.ObjectMeta{Name: "4.21.10"},
		Spec: imageapi.ImageStreamSpec{Tags: []imageapi.TagReference{{
			Name: "cli",
			From: &corev1.ObjectReference{Name: "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:cli-fake"},
		}}},
	}}
}

func sameObjectKey(a, b client.Object) bool {
	return fmt.Sprintf("%T", a) == fmt.Sprintf("%T", b) && client.ObjectKeyFromObject(a) == client.ObjectKeyFromObject(b)
}

func newIntegrationRun(fixture integrationFixture) integrationRun {
	hcp := fixture.hcp
	if hcp == nil {
		hcp = fakeHCP()
	}
	controlPlaneObjects := []client.Object{hcp.DeepCopy()}
	for _, object := range integrationControlPlaneObjects() {
		if slices.ContainsFunc(fixture.removeFromCP, func(removed client.Object) bool { return sameObjectKey(removed, object) }) {
			continue
		}
		if index := slices.IndexFunc(fixture.replaceInCP, func(replacement client.Object) bool { return sameObjectKey(replacement, object) }); index >= 0 {
			object = fixture.replaceInCP[index]
		}
		controlPlaneObjects = append(controlPlaneObjects, object)
	}
	hostedObjects := append([]client.Object{globalconfig.ProxyConfig(), manifests.NamespaceKubeSystem(), manifests.OpenShiftUserCABundle()}, fixture.additionalGuest...)
	trace := &testtrace.Recorder{}
	statusSubresources := []client.Object{&hyperv1.HostedControlPlane{}}
	hostedClient := testtrace.NewClient(trace, "hosted", testtrace.ClientOptions{Objects: hostedObjects, StatusSubresources: statusSubresources})
	controlPlaneClient := testtrace.NewClient(trace, "control-plane", testtrace.ClientOptions{Objects: controlPlaneObjects, GetFailures: fixture.cpGetFailures, StatusSubresources: statusSubresources})
	oauthAddress, oauthPort := fixture.oauthAddress, fixture.oauthPort
	if oauthAddress == "" {
		oauthAddress, oauthPort = "oauth.example.com", 443
	}
	return integrationRun{
		reconciler: &reconciler{
			client:                 hostedClient,
			uncachedClient:         fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			cpClient:               controlPlaneClient,
			CreateOrUpdateProvider: testtrace.FailingUpserter{Failures: fixture.upsertFailures},
			platformType:           hyperv1.NonePlatform,
			clusterSignerCA:        "foobar",
			hcpName:                hcp.Name,
			hcpNamespace:           hcp.Namespace,
			oauthAddress:           oauthAddress,
			oauthPort:              oauthPort,
			GetPodLogs: func(context.Context, *clientset.Clientset, string, string, string) ([]byte, error) {
				return nil, nil
			},
		},
		hosted: hostedClient,
		cp:     controlPlaneClient,
		trace:  trace,
	}
}

// integrationErrorPrefixes are the root-visible wrappers owned by the control-plane
// integration projections; other entries in the phase belong to sibling domains.
var integrationErrorPrefixes = []string{
	"failed to reconcile openshift apiserver service:",
	"failed to reconcile openshift apiserver endpoints:",
	"failed to reconcile openshift oauth apiserver",
	"failed to reconcile kubeadmin password hash secret:",
	"failed to reconcile oauth serving cert CA bundle:",
	"failed to reconcile oauth browser client:",
	"failed to reconcile oauth challenging client:",
	"failed to reconcile oauth cli client:",
	"failed to reconcile oauth serving cert role:",
	"failed to reconcile oauth serving cert rolebinding:",
}

func integrationErrors(errs []error) []error {
	var owned []error
	for _, err := range errs {
		for _, prefix := range integrationErrorPrefixes {
			if strings.HasPrefix(err.Error(), prefix) {
				owned = append(owned, err)
				break
			}
		}
	}
	return owned
}

func errorMessages(errs []error) []string {
	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	return messages
}

// controlPlaneIntegrationOperations filters the trace to control-plane operations on objects
// read or written by the integration projections (excluding the HCP status and Konnectivity
// inputs owned by sibling domains).
func controlPlaneIntegrationOperations(operations []string, namespace string) []string {
	owned := []string{
		fmt.Sprintf("*v1.Secret %s/%s", namespace, cpomanifests.RootCASecret(namespace).Name),
		fmt.Sprintf("*v1.Service %s/%s", namespace, manifests.OpenShiftAPIServerService(namespace).Name),
		fmt.Sprintf("*v1.Service %s/%s", namespace, manifests.OpenShiftOAuthAPIServerService(namespace).Name),
		fmt.Sprintf("*v1.Secret %s/%s", namespace, hccomanifests.KubeadminPasswordSecret(namespace).Name),
		fmt.Sprintf("*v1.ConfigMap %s/%s", namespace, cpomanifests.OpenShiftOAuthMasterCABundle(namespace).Name),
	}
	var filtered []string
	for _, operation := range operations {
		for _, suffix := range owned {
			if strings.Contains(operation, " control-plane ") && strings.HasSuffix(operation, suffix) {
				filtered = append(filtered, operation)
				break
			}
		}
	}
	return filtered
}

func controlPlaneWrites(operations []string) []string {
	var writes []string
	for _, operation := range operations {
		if strings.Contains(operation, " control-plane ") && !strings.HasPrefix(operation, "get ") {
			writes = append(writes, operation)
		}
	}
	return writes
}

func oidcHCP() *hyperv1.HostedControlPlane {
	hcp := fakeHCP()
	hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{Authentication: &configv1.AuthenticationSpec{Type: configv1.AuthenticationTypeOIDC}}
	return hcp
}

func expectAPIService(g Gomega, hosted client.Client, group string, service *corev1.Service, caBundle string) {
	apiService := &apiregistrationv1.APIService{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("v1.%s.openshift.io", group)}}
	g.Expect(hosted.Get(context.Background(), client.ObjectKeyFromObject(apiService), apiService)).WithOffset(1).To(Succeed(), "APIService for group %s", group)
	g.Expect(apiService.Spec.CABundle).WithOffset(1).To(Equal([]byte(caBundle)))
	g.Expect(apiService.Spec.Group).WithOffset(1).To(Equal(group + ".openshift.io"))
	g.Expect(apiService.Spec.Version).WithOffset(1).To(Equal("v1"))
	g.Expect(apiService.Spec.GroupPriorityMinimum).WithOffset(1).To(Equal(int32(9900)))
	g.Expect(apiService.Spec.VersionPriority).WithOffset(1).To(Equal(int32(15)))
	g.Expect(apiService.Spec.Service).WithOffset(1).To(Equal(&apiregistrationv1.ServiceReference{Namespace: service.Namespace, Name: service.Name, Port: ptr.To[int32](443)}))
}

//nolint:staticcheck // SA1019: corev1.Endpoints is intentionally used for backward compatibility
func expectEndpoints(g Gomega, hosted client.Client, endpoints *corev1.Endpoints, ip string) {
	g.Expect(hosted.Get(context.Background(), client.ObjectKeyFromObject(endpoints), endpoints)).WithOffset(1).To(Succeed())
	//nolint:staticcheck // SA1019: corev1.EndpointSubset is intentionally used for backward compatibility
	g.Expect(endpoints.Subsets).WithOffset(1).To(Equal([]corev1.EndpointSubset{{
		Addresses: []corev1.EndpointAddress{{IP: ip}},
		Ports:     []corev1.EndpointPort{{Name: "https", Port: 443, Protocol: corev1.ProtocolTCP}},
	}}))
}

func expectNotFound(g Gomega, hosted client.Client, objects ...client.Object) {
	for _, object := range objects {
		err := hosted.Get(context.Background(), client.ObjectKeyFromObject(object), object)
		g.Expect(apierrors.IsNotFound(err)).WithOffset(1).To(BeTrue(), "%T %s should not exist, got %v", object, client.ObjectKeyFromObject(object), err)
	}
}

func oauthAPIServerHostedObjects() []client.Object {
	objects := []client.Object{manifests.OpenShiftOAuthAPIServerClusterService(), manifests.OpenShiftOAuthAPIServerClusterEndpoints()}
	for _, group := range manifests.OpenShiftOAuthAPIServerAPIServiceGroups() {
		objects = append(objects, manifests.OpenShiftOAuthAPIServerAPIService(group))
	}
	return objects
}

func oauthProjectionHostedObjects() []client.Object {
	return []client.Object{
		manifests.OAuthCABundle(),
		manifests.OAuthServerBrowserClient(),
		hccomanifests.OAuthServerChallengingClient(),
		manifests.OAuthServerCLIClient(),
		manifests.OAuthServingCertRole(),
		manifests.OAuthServingCertRoleBinding(),
	}
}

func TestReconcileAPIServicesAndOAuth(t *testing.T) {
	const namespace = "bar"
	rootCAGet := fmt.Sprintf("get control-plane *v1.Secret %s/root-ca", namespace)
	kubeadminKey := fmt.Sprintf("*v1.Secret %s/%s", namespace, hccomanifests.KubeadminPasswordSecret(namespace).Name)

	t.Run("When all control-plane inputs are present and OAuth is enabled, it should project APIServices, services, endpoints and the kubeadmin hash through narrow cross-cluster access", func(t *testing.T) {
		g := NewWithT(t)
		staleEndpoints := manifests.OpenShiftAPIServerClusterEndpoints()
		//nolint:staticcheck // SA1019: corev1.EndpointSubset is intentionally used for backward compatibility
		staleEndpoints.Subsets = []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}}}}
		openshiftService, oauthService := fakeOpenShiftAPIServerService(), fakeOpenShiftOAuthAPIServerService()
		openshiftService.Spec.ClusterIP, oauthService.Spec.ClusterIP = "172.30.0.10", "172.30.0.11"
		run := newIntegrationRun(integrationFixture{replaceInCP: []client.Object{openshiftService, oauthService}, additionalGuest: []client.Object{staleEndpoints}})
		hcp := fakeHCP()

		errs := run.reconciler.reconcileAPIServicesAndOAuth(t.Context(), hcp, controllerruntime.LoggerFrom(t.Context()), integrationReleaseImage())
		operations := run.trace.Operations()

		g.Expect(errs).To(BeEmpty())
		for _, group := range manifests.OpenShiftAPIServerAPIServiceGroups() {
			expectAPIService(g, run.hosted, group, manifests.OpenShiftAPIServerClusterService(), "12345")
		}
		for _, group := range manifests.OpenShiftOAuthAPIServerAPIServiceGroups() {
			expectAPIService(g, run.hosted, group, manifests.OpenShiftOAuthAPIServerClusterService(), "12345")
		}
		for _, service := range []*corev1.Service{manifests.OpenShiftAPIServerClusterService(), manifests.OpenShiftOAuthAPIServerClusterService()} {
			g.Expect(run.hosted.Get(t.Context(), client.ObjectKeyFromObject(service), service)).To(Succeed())
			g.Expect(service.Spec.Ports).To(Equal([]corev1.ServicePort{{Name: "https", Port: 443}}))
		}
		expectEndpoints(g, run.hosted, manifests.OpenShiftAPIServerClusterEndpoints(), "172.30.0.10")
		expectEndpoints(g, run.hosted, manifests.OpenShiftOAuthAPIServerClusterEndpoints(), "172.30.0.11")

		hash := manifests.KubeadminPasswordHashSecret()
		g.Expect(run.hosted.Get(t.Context(), client.ObjectKeyFromObject(hash), hash)).To(Succeed())
		g.Expect(hash.Data["kubeadmin"]).NotTo(BeEmpty())
		password := hccomanifests.KubeadminPasswordSecret(namespace)
		g.Expect(run.cp.Get(t.Context(), client.ObjectKeyFromObject(password), password)).To(Succeed())
		g.Expect(password.Annotations).To(HaveKeyWithValue(pkgoauth.KubeadminSecretHashAnnotation, string(hash.Data["kubeadmin"])))

		g.Expect(controlPlaneIntegrationOperations(operations, namespace)).To(Equal([]string{
			rootCAGet,
			fmt.Sprintf("get control-plane *v1.Service %s/openshift-apiserver", namespace),
			rootCAGet,
			fmt.Sprintf("get control-plane *v1.Service %s/openshift-oauth-apiserver", namespace),
			"get control-plane " + kubeadminKey,
			"get control-plane " + kubeadminKey,
			"update control-plane " + kubeadminKey,
		}))
		g.Expect(controlPlaneWrites(operations)).To(Equal([]string{"update control-plane " + kubeadminKey}))
	})

	t.Run("When OAuth is disabled, it should project only OpenShift API server resources and leave the kubeadmin hash untouched", func(t *testing.T) {
		g := NewWithT(t)
		existingHash := manifests.KubeadminPasswordHashSecret()
		existingHash.Data = map[string][]byte{"kubeadmin": []byte("existing-hash")}
		run := newIntegrationRun(integrationFixture{hcp: oidcHCP(), additionalGuest: []client.Object{existingHash}})

		errs := run.reconciler.reconcileAPIServicesAndOAuth(t.Context(), oidcHCP(), controllerruntime.LoggerFrom(t.Context()), integrationReleaseImage())
		operations := run.trace.Operations()

		g.Expect(errs).To(BeEmpty())
		for _, group := range manifests.OpenShiftAPIServerAPIServiceGroups() {
			expectAPIService(g, run.hosted, group, manifests.OpenShiftAPIServerClusterService(), "12345")
		}
		expectEndpoints(g, run.hosted, manifests.OpenShiftAPIServerClusterEndpoints(), "1.1.1.1")
		expectNotFound(g, run.hosted, oauthAPIServerHostedObjects()...)
		hash := manifests.KubeadminPasswordHashSecret()
		g.Expect(run.hosted.Get(t.Context(), client.ObjectKeyFromObject(hash), hash)).To(Succeed())
		g.Expect(hash.Data["kubeadmin"]).To(Equal([]byte("existing-hash")))
		g.Expect(controlPlaneIntegrationOperations(operations, namespace)).To(Equal([]string{
			rootCAGet,
			fmt.Sprintf("get control-plane *v1.Service %s/openshift-apiserver", namespace),
		}))
		g.Expect(controlPlaneWrites(operations)).To(BeEmpty())
	})

	t.Run("When the control-plane root CA is missing, it should skip every APIService and continue with services, endpoints and the kubeadmin hash", func(t *testing.T) {
		g := NewWithT(t)
		run := newIntegrationRun(integrationFixture{removeFromCP: []client.Object{fakeRootCASecret()}})

		errs := run.reconciler.reconcileAPIServicesAndOAuth(t.Context(), fakeHCP(), controllerruntime.LoggerFrom(t.Context()), integrationReleaseImage())
		operations := run.trace.Operations()
		g.Expect(controlPlaneWrites(operations)).To(Equal([]string{"update control-plane " + kubeadminKey}))

		owned := integrationErrors(errs)
		g.Expect(errorMessages(owned)).To(Equal([]string{
			`failed to reconcile openshift apiserver service: failed to get root ca from control plane: secrets "root-ca" not found`,
			`failed to reconcile openshift oauth apiserver apiservices: failed to get root ca from control plane: secrets "root-ca" not found`,
		}))
		for _, err := range owned {
			g.Expect(apierrors.IsNotFound(errors.Unwrap(errors.Unwrap(err)))).To(BeTrue())
		}
		apiServices := &apiregistrationv1.APIServiceList{}
		g.Expect(run.hosted.List(t.Context(), apiServices)).To(Succeed())
		g.Expect(apiServices.Items).To(BeEmpty())
		expectEndpoints(g, run.hosted, manifests.OpenShiftAPIServerClusterEndpoints(), "1.1.1.1")
		expectEndpoints(g, run.hosted, manifests.OpenShiftOAuthAPIServerClusterEndpoints(), "1.1.1.1")
		g.Expect(run.hosted.Get(t.Context(), client.ObjectKeyFromObject(manifests.OpenShiftOAuthAPIServerClusterService()), manifests.OpenShiftOAuthAPIServerClusterService())).To(Succeed())
		g.Expect(run.hosted.Get(t.Context(), client.ObjectKeyFromObject(manifests.KubeadminPasswordHashSecret()), manifests.KubeadminPasswordHashSecret())).To(Succeed())
	})

	t.Run("When the control-plane OpenShift API server service is missing, it should report it, skip its endpoints and continue", func(t *testing.T) {
		g := NewWithT(t)
		run := newIntegrationRun(integrationFixture{removeFromCP: []client.Object{fakeOpenShiftAPIServerService()}})

		errs := run.reconciler.reconcileAPIServicesAndOAuth(t.Context(), fakeHCP(), controllerruntime.LoggerFrom(t.Context()), integrationReleaseImage())
		operations := run.trace.Operations()
		g.Expect(controlPlaneWrites(operations)).To(Equal([]string{"update control-plane " + kubeadminKey}))

		g.Expect(errorMessages(integrationErrors(errs))).To(Equal([]string{
			`failed to reconcile openshift apiserver endpoints: failed to get openshift apiserver service from control plane: services "openshift-apiserver" not found`,
		}))
		expectNotFound(g, run.hosted, manifests.OpenShiftAPIServerClusterEndpoints())
		g.Expect(run.hosted.Get(t.Context(), client.ObjectKeyFromObject(manifests.OpenShiftAPIServerClusterService()), manifests.OpenShiftAPIServerClusterService())).To(Succeed())
		expectEndpoints(g, run.hosted, manifests.OpenShiftOAuthAPIServerClusterEndpoints(), "1.1.1.1")
	})

	t.Run("When the control-plane OpenShift API server service has no cluster IP, it should report readiness and keep existing endpoints", func(t *testing.T) {
		g := NewWithT(t)
		notReady := fakeOpenShiftAPIServerService()
		notReady.Spec.ClusterIP = ""
		existing := manifests.OpenShiftAPIServerClusterEndpoints()
		//nolint:staticcheck // SA1019: corev1.EndpointSubset is intentionally used for backward compatibility
		existing.Subsets = []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "1.1.1.1"}}, Ports: []corev1.EndpointPort{{Name: "https", Port: 443, Protocol: corev1.ProtocolTCP}}}}
		run := newIntegrationRun(integrationFixture{replaceInCP: []client.Object{notReady}, additionalGuest: []client.Object{existing}})

		errs := run.reconciler.reconcileAPIServicesAndOAuth(t.Context(), fakeHCP(), controllerruntime.LoggerFrom(t.Context()), integrationReleaseImage())
		operations := run.trace.Operations()

		g.Expect(errorMessages(integrationErrors(errs))).To(Equal([]string{
			"failed to reconcile openshift apiserver endpoints: openshift apiserver service in control plane does not yet have a cluster IP",
		}))
		expectEndpoints(g, run.hosted, manifests.OpenShiftAPIServerClusterEndpoints(), "1.1.1.1")
		g.Expect(slices.ContainsFunc(operations, func(operation string) bool {
			return strings.HasPrefix(operation, "update hosted *v1.Endpoints default/openshift-apiserver")
		})).To(BeFalse())
	})

	t.Run("When the control-plane OAuth API server service is missing, it should report it without affecting OpenShift API server endpoints or the kubeadmin hash", func(t *testing.T) {
		g := NewWithT(t)
		run := newIntegrationRun(integrationFixture{removeFromCP: []client.Object{fakeOpenShiftOAuthAPIServerService()}})

		errs := run.reconciler.reconcileAPIServicesAndOAuth(t.Context(), fakeHCP(), controllerruntime.LoggerFrom(t.Context()), integrationReleaseImage())
		operations := run.trace.Operations()
		g.Expect(controlPlaneWrites(operations)).To(Equal([]string{"update control-plane " + kubeadminKey}))

		g.Expect(errorMessages(integrationErrors(errs))).To(Equal([]string{
			`failed to reconcile openshift oauth apiserver endpoints: failed to get openshift oauth apiserver service from control plane: services "openshift-oauth-apiserver" not found`,
		}))
		expectNotFound(g, run.hosted, manifests.OpenShiftOAuthAPIServerClusterEndpoints())
		expectEndpoints(g, run.hosted, manifests.OpenShiftAPIServerClusterEndpoints(), "1.1.1.1")
		g.Expect(run.hosted.Get(t.Context(), client.ObjectKeyFromObject(manifests.KubeadminPasswordHashSecret()), manifests.KubeadminPasswordHashSecret())).To(Succeed())
	})

	t.Run("When the control-plane OAuth API server service has no cluster IP, it should report readiness and skip OAuth endpoints", func(t *testing.T) {
		g := NewWithT(t)
		notReady := fakeOpenShiftOAuthAPIServerService()
		notReady.Spec.ClusterIP = ""
		run := newIntegrationRun(integrationFixture{replaceInCP: []client.Object{notReady}})

		errs := run.reconciler.reconcileAPIServicesAndOAuth(t.Context(), fakeHCP(), controllerruntime.LoggerFrom(t.Context()), integrationReleaseImage())
		operations := run.trace.Operations()
		g.Expect(controlPlaneWrites(operations)).To(Equal([]string{"update control-plane " + kubeadminKey}))

		g.Expect(errorMessages(integrationErrors(errs))).To(Equal([]string{
			"failed to reconcile openshift oauth apiserver endpoints: openshift oauth apiserver service in control plane does not yet have a cluster IP",
		}))
		expectNotFound(g, run.hosted, manifests.OpenShiftOAuthAPIServerClusterEndpoints())
		expectEndpoints(g, run.hosted, manifests.OpenShiftAPIServerClusterEndpoints(), "1.1.1.1")
	})

	t.Run("When reading the control-plane kubeadmin password fails, it should wrap the error and leave the hosted hash alone", func(t *testing.T) {
		g := NewWithT(t)
		unavailable := errors.New("cp unavailable")
		run := newIntegrationRun(integrationFixture{cpGetFailures: map[string]error{kubeadminKey: unavailable}})

		errs := run.reconciler.reconcileAPIServicesAndOAuth(t.Context(), fakeHCP(), controllerruntime.LoggerFrom(t.Context()), integrationReleaseImage())
		operations := run.trace.Operations()

		owned := integrationErrors(errs)
		g.Expect(errorMessages(owned)).To(Equal([]string{"failed to reconcile kubeadmin password hash secret: failed to get kubeadmin password secret: cp unavailable"}))
		g.Expect(errors.Is(owned[0], unavailable)).To(BeTrue())
		expectNotFound(g, run.hosted, manifests.KubeadminPasswordHashSecret())
		g.Expect(controlPlaneWrites(operations)).To(BeEmpty())
	})

	t.Run("When projection writes fail, it should continue the stage and return each wrapped failure in order", func(t *testing.T) {
		g := NewWithT(t)
		apiServiceFailure, endpointsFailure, oauthServiceFailure, hashFailure := errors.New("apiservice failure"), errors.New("endpoints failure"), errors.New("oauth service failure"), errors.New("hash failure")
		run := newIntegrationRun(integrationFixture{upsertFailures: map[string]error{
			"*v1.APIService /v1.apps.openshift.io":          apiServiceFailure,
			"*v1.Endpoints default/openshift-apiserver":     endpointsFailure,
			"*v1.Service default/openshift-oauth-apiserver": oauthServiceFailure,
			"*v1.Secret kube-system/kubeadmin":              hashFailure,
		}})

		errs := run.reconciler.reconcileAPIServicesAndOAuth(t.Context(), fakeHCP(), controllerruntime.LoggerFrom(t.Context()), integrationReleaseImage())
		operations := run.trace.Operations()

		owned := integrationErrors(errs)
		g.Expect(errorMessages(owned)).To(Equal([]string{
			"failed to reconcile openshift apiserver service: failed to reconcile openshift apiserver apiservice (apps): apiservice failure",
			"failed to reconcile openshift apiserver endpoints: endpoints failure",
			"failed to reconcile openshift oauth apiserver service: oauth service failure",
			"failed to reconcile kubeadmin password hash secret: hash failure",
		}))
		for index, failure := range []error{apiServiceFailure, endpointsFailure, oauthServiceFailure, hashFailure} {
			g.Expect(errors.Is(owned[index], failure)).To(BeTrue())
		}
		expectAPIService(g, run.hosted, "project", manifests.OpenShiftAPIServerClusterService(), "12345")
		expectEndpoints(g, run.hosted, manifests.OpenShiftOAuthAPIServerClusterEndpoints(), "1.1.1.1")
		g.Expect(controlPlaneWrites(operations)).To(BeEmpty())
	})
}

func TestReconcileNetworkingAndSecrets(t *testing.T) {
	const namespace = "bar"
	oauthCAGet := fmt.Sprintf("get control-plane *v1.ConfigMap %s/%s", namespace, cpomanifests.OpenShiftOAuthMasterCABundle(namespace).Name)

	for _, tc := range []struct {
		name            string
		address         string
		port            int32
		expectedBrowser string
		expectedChallen string
	}{
		{
			name:            "When OAuth is enabled with a DNS address, it should project the OAuth CA bundle, clients and serving-cert RBAC after the proxy CA",
			address:         "oauth.example.com",
			port:            443,
			expectedBrowser: "https://oauth.example.com:443/oauth/token/display",
			expectedChallen: "https://oauth.example.com:443/oauth/token/implicit",
		},
		{
			name:            "When OAuth is enabled with an IPv6 address, it should bracket the host in client redirect URIs",
			address:         "fd00::1",
			port:            6443,
			expectedBrowser: "https://[fd00::1]:6443/oauth/token/display",
			expectedChallen: "https://[fd00::1]:6443/oauth/token/implicit",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			run := newIntegrationRun(integrationFixture{oauthAddress: tc.address, oauthPort: tc.port})

			errs := run.reconciler.reconcileNetworkingAndSecrets(t.Context(), fakeHCP(), controllerruntime.LoggerFrom(t.Context()), fakePullSecret())
			operations := run.trace.Operations()

			g.Expect(errs).To(BeEmpty())
			caBundle := manifests.OAuthCABundle()
			g.Expect(run.hosted.Get(t.Context(), client.ObjectKeyFromObject(caBundle), caBundle)).To(Succeed())
			g.Expect(caBundle.Data).To(Equal(map[string]string{"ca-bundle.crt": "test"}))
			browser, challenging, cli := manifests.OAuthServerBrowserClient(), hccomanifests.OAuthServerChallengingClient(), manifests.OAuthServerCLIClient()
			for _, oauthClient := range []*oauthv1.OAuthClient{browser, challenging, cli} {
				g.Expect(run.hosted.Get(t.Context(), client.ObjectKeyFromObject(oauthClient), oauthClient)).To(Succeed())
			}
			g.Expect(browser.RedirectURIs).To(Equal([]string{tc.expectedBrowser}))
			g.Expect(browser.Secret).NotTo(BeEmpty())
			g.Expect(challenging.RedirectURIs).To(Equal([]string{tc.expectedChallen}))
			g.Expect(challenging.RespondWithChallenges).To(BeTrue())
			g.Expect(cli.RedirectURIs).To(Equal([]string{"http://127.0.0.1/callback", "http://[::1]/callback"}))
			role, roleBinding := manifests.OAuthServingCertRole(), manifests.OAuthServingCertRoleBinding()
			g.Expect(run.hosted.Get(t.Context(), client.ObjectKeyFromObject(role), role)).To(Succeed())
			g.Expect(role.Rules).NotTo(BeEmpty())
			g.Expect(run.hosted.Get(t.Context(), client.ObjectKeyFromObject(roleBinding), roleBinding)).To(Succeed())
			g.Expect(roleBinding.RoleRef).To(Equal(rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: role.Name}))

			g.Expect(controlPlaneIntegrationOperations(operations, namespace)).To(Equal([]string{oauthCAGet}))
			ordered := []string{
				"create hosted *v1.Secret openshift-config/pull-secret",
				"create hosted *v1.Secret openshift/pull-secret",
				fmt.Sprintf("delete hosted *v1.ConfigMap %s/%s", manifests.OpenShiftUserCABundle().Namespace, manifests.OpenShiftUserCABundle().Name),
				fmt.Sprintf("create hosted *v1.ConfigMap %s/%s", caBundle.Namespace, caBundle.Name),
				fmt.Sprintf("create hosted *v1.OAuthClient /%s", browser.Name),
				fmt.Sprintf("create hosted *v1.OAuthClient /%s", challenging.Name),
				fmt.Sprintf("create hosted *v1.OAuthClient /%s", cli.Name),
				fmt.Sprintf("create hosted *v1.Role %s/%s", role.Namespace, role.Name),
				fmt.Sprintf("create hosted *v1.RoleBinding %s/%s", roleBinding.Namespace, roleBinding.Name),
				fmt.Sprintf("create hosted *v1.ConfigMap %s/%s", manifests.OpenShiftControllerManagerServiceCA().Namespace, manifests.OpenShiftControllerManagerServiceCA().Name),
			}
			previous := -1
			for _, expected := range ordered {
				found := slices.Index(operations[previous+1:], expected)
				g.Expect(found).To(BeNumerically(">=", 0), "operation %q after index %d; trace: %v", expected, previous, operations)
				previous += found + 1
			}
		})
	}

	t.Run("When OAuth is disabled, it should not read the OAuth CA or project any OAuth resources", func(t *testing.T) {
		g := NewWithT(t)
		run := newIntegrationRun(integrationFixture{hcp: oidcHCP()})

		errs := run.reconciler.reconcileNetworkingAndSecrets(t.Context(), oidcHCP(), controllerruntime.LoggerFrom(t.Context()), fakePullSecret())
		operations := run.trace.Operations()

		g.Expect(errs).To(BeEmpty())
		expectNotFound(g, run.hosted, oauthProjectionHostedObjects()...)
		g.Expect(controlPlaneIntegrationOperations(operations, namespace)).To(BeEmpty())
	})

	t.Run("When the control-plane OAuth master CA is missing, it should report it and still project clients and RBAC", func(t *testing.T) {
		g := NewWithT(t)
		run := newIntegrationRun(integrationFixture{removeFromCP: []client.Object{fakeOAuthMasterCABundle()}})

		errs := run.reconciler.reconcileNetworkingAndSecrets(t.Context(), fakeHCP(), controllerruntime.LoggerFrom(t.Context()), fakePullSecret())

		g.Expect(errorMessages(integrationErrors(errs))).To(Equal([]string{
			`failed to reconcile oauth serving cert CA bundle: cannot get oauth master ca bundle: configmaps "oauth-master-ca-bundle" not found`,
		}))
		expectNotFound(g, run.hosted, manifests.OAuthCABundle())
		for _, object := range oauthProjectionHostedObjects()[1:] {
			g.Expect(run.hosted.Get(t.Context(), client.ObjectKeyFromObject(object), object)).To(Succeed())
		}
	})

	t.Run("When OAuth projection writes fail, it should attempt every remaining projection and return each wrapped failure in order", func(t *testing.T) {
		g := NewWithT(t)
		browserFailure, roleFailure := errors.New("browser failure"), errors.New("role failure")
		run := newIntegrationRun(integrationFixture{upsertFailures: map[string]error{
			fmt.Sprintf("*v1.OAuthClient /%s", manifests.OAuthServerBrowserClient().Name):                                    browserFailure,
			fmt.Sprintf("*v1.Role %s/%s", manifests.OAuthServingCertRole().Namespace, manifests.OAuthServingCertRole().Name): roleFailure,
		}})

		errs := run.reconciler.reconcileNetworkingAndSecrets(t.Context(), fakeHCP(), controllerruntime.LoggerFrom(t.Context()), fakePullSecret())

		owned := integrationErrors(errs)
		g.Expect(errorMessages(owned)).To(Equal([]string{
			"failed to reconcile oauth browser client: browser failure",
			"failed to reconcile oauth serving cert role: role failure",
		}))
		g.Expect(errors.Is(owned[0], browserFailure)).To(BeTrue())
		g.Expect(errors.Is(owned[1], roleFailure)).To(BeTrue())
		for _, object := range []client.Object{hccomanifests.OAuthServerChallengingClient(), manifests.OAuthServerCLIClient(), manifests.OAuthServingCertRoleBinding()} {
			g.Expect(run.hosted.Get(t.Context(), client.ObjectKeyFromObject(object), object)).To(Succeed())
		}
	})
}
