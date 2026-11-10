package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/internal/testtrace"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	pkgoauth "github.com/openshift/hypershift/pkg/oauth"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func expectAPIServices(g Gomega, hosted client.Client, server apiServerProjection, caBundle string) {
	for _, group := range server.groups {
		apiService := &apiregistrationv1.APIService{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("v1.%s.openshift.io", group)}}
		g.Expect(hosted.Get(context.Background(), client.ObjectKeyFromObject(apiService), apiService)).WithOffset(1).To(Succeed(), "APIService for group %s", group)
		service := server.clusterService()
		g.Expect(apiService.Spec).WithOffset(1).To(Equal(apiregistrationv1.APIServiceSpec{
			CABundle:             []byte(caBundle),
			Group:                group + ".openshift.io",
			Version:              "v1",
			GroupPriorityMinimum: 9900,
			VersionPriority:      15,
			Service:              &apiregistrationv1.ServiceReference{Namespace: service.Namespace, Name: service.Name, Port: ptr.To[int32](443)},
		}))
	}
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

func expectAbsent(g Gomega, hosted client.Client, objects ...client.Object) {
	for _, object := range objects {
		err := hosted.Get(context.Background(), client.ObjectKeyFromObject(object), object)
		g.Expect(apierrors.IsNotFound(err)).WithOffset(1).To(BeTrue(), "%s should not exist, got %v", testtrace.ObjectKey(object), err)
	}
}

func oauthAPIServerObjects() []client.Object {
	objects := []client.Object{manifests.OpenShiftOAuthAPIServerClusterService(), manifests.OpenShiftOAuthAPIServerClusterEndpoints()}
	for _, group := range manifests.OpenShiftOAuthAPIServerAPIServiceGroups() {
		objects = append(objects, manifests.OpenShiftOAuthAPIServerAPIService(group))
	}
	return objects
}

func TestReconcileAPIServiceProjections(t *testing.T) {
	rootCAGet := fmt.Sprintf("get control-plane *v1.Secret %s/root-ca", testNamespace)
	kubeadminKey := testtrace.ObjectKey(kubeadminPasswordSecret())
	kubeadminWrite := []string{"get control-plane " + kubeadminKey, "update control-plane " + kubeadminKey}

	t.Run("When all control-plane inputs are present and OAuth is enabled, it should project every API server resource and read only the declared control-plane inputs", func(t *testing.T) {
		g := NewWithT(t)
		staleEndpoints := manifests.OpenShiftAPIServerClusterEndpoints()
		//nolint:staticcheck // SA1019: corev1.EndpointSubset is intentionally used for backward compatibility
		staleEndpoints.Subsets = []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}}}}
		run := newProjectionRun(projectionFixture{controlPlane: controlPlaneInputs(), hosted: []client.Object{staleEndpoints}})

		errs := reconcileAPIServiceProjections(t.Context(), run.hosted, run.controlPlane, enabledParams())
		controlPlaneOperations := run.trace.Cluster("control-plane")

		g.Expect(errs).To(BeEmpty())
		expectAPIServices(g, run.hostedClient, openshiftAPIServer, "root-ca-bundle")
		expectAPIServices(g, run.hostedClient, openshiftOAuthAPIServer, "root-ca-bundle")
		expectEndpoints(g, run.hostedClient, manifests.OpenShiftAPIServerClusterEndpoints(), "172.30.0.10")
		expectEndpoints(g, run.hostedClient, manifests.OpenShiftOAuthAPIServerClusterEndpoints(), "172.30.0.11")
		for _, service := range []*corev1.Service{manifests.OpenShiftAPIServerClusterService(), manifests.OpenShiftOAuthAPIServerClusterService()} {
			g.Expect(run.hostedClient.Get(t.Context(), client.ObjectKeyFromObject(service), service)).To(Succeed())
			g.Expect(service.Spec.Ports).To(Equal([]corev1.ServicePort{{Name: "https", Port: 443}}))
		}
		hash := manifests.KubeadminPasswordHashSecret()
		g.Expect(run.hostedClient.Get(t.Context(), client.ObjectKeyFromObject(hash), hash)).To(Succeed())
		password := kubeadminPasswordSecret()
		g.Expect(run.cpClient.Get(t.Context(), client.ObjectKeyFromObject(password), password)).To(Succeed())
		g.Expect(password.Annotations).To(HaveKeyWithValue(pkgoauth.KubeadminSecretHashAnnotation, string(hash.Data["kubeadmin"])))
		g.Expect(controlPlaneOperations).To(Equal(append([]string{
			rootCAGet,
			fmt.Sprintf("get control-plane *v1.Service %s/openshift-apiserver", testNamespace),
			rootCAGet,
			fmt.Sprintf("get control-plane *v1.Service %s/openshift-oauth-apiserver", testNamespace),
			"get control-plane " + kubeadminKey,
		}, kubeadminWrite...)))
	})

	t.Run("When OAuth is disabled, it should project only OpenShift API server resources and not touch the kubeadmin hash", func(t *testing.T) {
		g := NewWithT(t)
		existingHash := manifests.KubeadminPasswordHashSecret()
		existingHash.Data = map[string][]byte{"kubeadmin": []byte("existing-hash")}
		run := newProjectionRun(projectionFixture{controlPlane: controlPlaneInputs(), hosted: []client.Object{existingHash}})

		errs := reconcileAPIServiceProjections(t.Context(), run.hosted, run.controlPlane, disabledParams())
		controlPlaneOperations := run.trace.Cluster("control-plane")

		g.Expect(errs).To(BeEmpty())
		expectAPIServices(g, run.hostedClient, openshiftAPIServer, "root-ca-bundle")
		expectEndpoints(g, run.hostedClient, manifests.OpenShiftAPIServerClusterEndpoints(), "172.30.0.10")
		expectAbsent(g, run.hostedClient, oauthAPIServerObjects()...)
		hash := manifests.KubeadminPasswordHashSecret()
		g.Expect(run.hostedClient.Get(t.Context(), client.ObjectKeyFromObject(hash), hash)).To(Succeed())
		g.Expect(hash.Data["kubeadmin"]).To(Equal([]byte("existing-hash")))
		g.Expect(controlPlaneOperations).To(Equal([]string{rootCAGet, fmt.Sprintf("get control-plane *v1.Service %s/openshift-apiserver", testNamespace)}))
	})

	for _, tc := range []struct {
		name             string
		controlPlane     []client.Object
		cpGetFailures    map[string]error
		expectedMessages []string
		expectAbsent     []client.Object
	}{
		{
			name:         "When the control-plane root CA is missing, it should skip all APIServices and continue with the remaining projections",
			controlPlane: without(controlPlaneInputs(), rootCASecret()),
			expectedMessages: []string{
				`failed to reconcile openshift apiserver service: failed to get root ca from control plane: secrets "root-ca" not found`,
				`failed to reconcile openshift oauth apiserver apiservices: failed to get root ca from control plane: secrets "root-ca" not found`,
			},
			expectAbsent: append([]client.Object{manifests.OpenShiftAPIServerAPIService("apps")}, manifests.OpenShiftOAuthAPIServerAPIService("oauth")),
		},
		{
			name:             "When the control-plane OpenShift API server service is missing, it should report it and skip its endpoints",
			controlPlane:     without(controlPlaneInputs(), openshiftAPIServerControlPlaneService("")),
			expectedMessages: []string{`failed to reconcile openshift apiserver endpoints: failed to get openshift apiserver service from control plane: services "openshift-apiserver" not found`},
			expectAbsent:     []client.Object{manifests.OpenShiftAPIServerClusterEndpoints()},
		},
		{
			name:             "When the control-plane OpenShift API server service has no cluster IP, it should report it as not ready",
			controlPlane:     replacing(controlPlaneInputs(), openshiftAPIServerControlPlaneService("")),
			expectedMessages: []string{"failed to reconcile openshift apiserver endpoints: openshift apiserver service in control plane does not yet have a cluster IP"},
			expectAbsent:     []client.Object{manifests.OpenShiftAPIServerClusterEndpoints()},
		},
		{
			name:             "When the control-plane OAuth API server service is missing, it should report it and skip only the OAuth endpoints",
			controlPlane:     without(controlPlaneInputs(), oauthAPIServerControlPlaneService("")),
			expectedMessages: []string{`failed to reconcile openshift oauth apiserver endpoints: failed to get openshift oauth apiserver service from control plane: services "openshift-oauth-apiserver" not found`},
			expectAbsent:     []client.Object{manifests.OpenShiftOAuthAPIServerClusterEndpoints()},
		},
		{
			name:             "When the control-plane OAuth API server service has no cluster IP, it should report it as not ready",
			controlPlane:     replacing(controlPlaneInputs(), oauthAPIServerControlPlaneService("")),
			expectedMessages: []string{"failed to reconcile openshift oauth apiserver endpoints: openshift oauth apiserver service in control plane does not yet have a cluster IP"},
			expectAbsent:     []client.Object{manifests.OpenShiftOAuthAPIServerClusterEndpoints()},
		},
		{
			name:             "When reading the control-plane kubeadmin password fails, it should wrap the error and project no hash",
			controlPlane:     controlPlaneInputs(),
			cpGetFailures:    map[string]error{kubeadminKey: errors.New("cp unavailable")},
			expectedMessages: []string{"failed to reconcile kubeadmin password hash secret: failed to get kubeadmin password secret: cp unavailable"},
			expectAbsent:     []client.Object{manifests.KubeadminPasswordHashSecret()},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			run := newProjectionRun(projectionFixture{controlPlane: tc.controlPlane, cpGetFailures: tc.cpGetFailures})

			errs := reconcileAPIServiceProjections(t.Context(), run.hosted, run.controlPlane, enabledParams())

			g.Expect(errorMessages(errs)).To(Equal(tc.expectedMessages))
			expectAbsent(g, run.hostedClient, tc.expectAbsent...)
			for _, object := range []client.Object{manifests.OpenShiftAPIServerClusterService(), manifests.OpenShiftOAuthAPIServerClusterService()} {
				g.Expect(run.hostedClient.Get(t.Context(), client.ObjectKeyFromObject(object), object)).To(Succeed())
			}
		})
	}

	t.Run("When hosted writes fail, it should continue every projection and return each wrapped failure in order", func(t *testing.T) {
		g := NewWithT(t)
		apiServiceFailure, serviceFailure, endpointsFailure, hashFailure := errors.New("apiservice failure"), errors.New("service failure"), errors.New("endpoints failure"), errors.New("hash failure")
		run := newProjectionRun(projectionFixture{controlPlane: controlPlaneInputs(), upsertFailures: map[string]error{
			testtrace.ObjectKey(manifests.OpenShiftOAuthAPIServerAPIService("user")): apiServiceFailure,
			testtrace.ObjectKey(manifests.OpenShiftAPIServerClusterService()):        serviceFailure,
			testtrace.ObjectKey(manifests.OpenShiftOAuthAPIServerClusterEndpoints()): endpointsFailure,
			testtrace.ObjectKey(manifests.KubeadminPasswordHashSecret()):             hashFailure,
		}})

		errs := reconcileAPIServiceProjections(t.Context(), run.hosted, run.controlPlane, enabledParams())

		g.Expect(errorMessages(errs)).To(Equal([]string{
			"failed to reconcile openshift apiserver service: service failure",
			"failed to reconcile openshift oauth apiserver apiservices: failed to reconcile openshift oauth apiserver apiservice (user): apiservice failure",
			"failed to reconcile openshift oauth apiserver endpoints: endpoints failure",
			"failed to reconcile kubeadmin password hash secret: hash failure",
		}))
		for index, failure := range []error{serviceFailure, apiServiceFailure, endpointsFailure, hashFailure} {
			g.Expect(errors.Is(errs[index], failure)).To(BeTrue())
		}
		expectAPIServices(g, run.hostedClient, openshiftAPIServer, "root-ca-bundle")
		g.Expect(run.trace.Cluster("control-plane")).NotTo(ContainElement(HavePrefix("update ")))
	})
}

func TestReconcileAPIServices(t *testing.T) {
	t.Run("When the control-plane root CA is missing, it should return before writing any APIService", func(t *testing.T) {
		g := NewWithT(t)
		run := newProjectionRun(projectionFixture{})

		err := reconcileAPIServices(t.Context(), run.hosted, run.controlPlane, testNamespace, openshiftAPIServer)

		g.Expect(err).To(MatchError(`failed to get root ca from control plane: secrets "root-ca" not found`))
		g.Expect(apierrors.IsNotFound(errors.Unwrap(err))).To(BeTrue())
		g.Expect(run.trace.Cluster("hosted")).To(BeEmpty())
	})

	t.Run("When some groups fail, it should still project the other groups and aggregate one entry per failed group", func(t *testing.T) {
		g := NewWithT(t)
		appsFailure, routeFailure := errors.New("apps failure"), errors.New("route failure")
		run := newProjectionRun(projectionFixture{controlPlane: []client.Object{rootCASecret()}, upsertFailures: map[string]error{
			testtrace.ObjectKey(manifests.OpenShiftAPIServerAPIService("apps")):  appsFailure,
			testtrace.ObjectKey(manifests.OpenShiftAPIServerAPIService("route")): routeFailure,
		}})

		err := reconcileAPIServices(t.Context(), run.hosted, run.controlPlane, testNamespace, openshiftAPIServer)

		var aggregate utilerrors.Aggregate
		g.Expect(errors.As(err, &aggregate)).To(BeTrue())
		g.Expect(errorMessages(aggregate.Errors())).To(Equal([]string{
			"failed to reconcile openshift apiserver apiservice (apps): apps failure",
			"failed to reconcile openshift apiserver apiservice (route): route failure",
		}))
		apiService := manifests.OpenShiftAPIServerAPIService("project")
		g.Expect(run.hostedClient.Get(t.Context(), client.ObjectKeyFromObject(apiService), apiService)).To(Succeed())
		g.Expect(apiService.Spec.CABundle).To(Equal([]byte("root-ca-bundle")))
	})
}

func TestReconcileClusterService(t *testing.T) {
	t.Run("When the service does not exist, it should create it with the https port", func(t *testing.T) {
		g := NewWithT(t)
		run := newProjectionRun(projectionFixture{})

		g.Expect(reconcileClusterService(t.Context(), run.hosted, openshiftOAuthAPIServer)).To(Succeed())

		service := manifests.OpenShiftOAuthAPIServerClusterService()
		g.Expect(run.hostedClient.Get(t.Context(), client.ObjectKeyFromObject(service), service)).To(Succeed())
		g.Expect(service.Spec.Ports).To(Equal([]corev1.ServicePort{{Name: "https", Port: 443}}))
	})

	t.Run("When the hosted write fails, it should return the error unwrapped", func(t *testing.T) {
		g := NewWithT(t)
		failure := errors.New("write failure")
		run := newProjectionRun(projectionFixture{upsertFailures: map[string]error{testtrace.ObjectKey(manifests.OpenShiftAPIServerClusterService()): failure}})

		g.Expect(reconcileClusterService(t.Context(), run.hosted, openshiftAPIServer)).To(BeIdenticalTo(failure))
	})
}

func TestReconcileEndpoints(t *testing.T) {
	t.Run("When the control-plane service has a cluster IP, it should replace stale hosted endpoint data", func(t *testing.T) {
		g := NewWithT(t)
		stale := manifests.OpenShiftAPIServerClusterEndpoints()
		//nolint:staticcheck // SA1019: corev1.EndpointSubset is intentionally used for backward compatibility
		stale.Subsets = []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}}}}
		run := newProjectionRun(projectionFixture{controlPlane: []client.Object{openshiftAPIServerControlPlaneService("172.30.0.10")}, hosted: []client.Object{stale}})

		g.Expect(reconcileEndpoints(t.Context(), run.hosted, run.controlPlane, testNamespace, openshiftAPIServer)).To(Succeed())

		expectEndpoints(g, run.hostedClient, manifests.OpenShiftAPIServerClusterEndpoints(), "172.30.0.10")
	})

	t.Run("When the control-plane service has no cluster IP, it should report not ready and leave existing endpoints untouched", func(t *testing.T) {
		g := NewWithT(t)
		existing := manifests.OpenShiftOAuthAPIServerClusterEndpoints()
		//nolint:staticcheck // SA1019: corev1.EndpointSubset is intentionally used for backward compatibility
		existing.Subsets = []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}}, Ports: []corev1.EndpointPort{{Name: "https", Port: 443, Protocol: corev1.ProtocolTCP}}}}
		run := newProjectionRun(projectionFixture{controlPlane: []client.Object{oauthAPIServerControlPlaneService("")}, hosted: []client.Object{existing}})

		err := reconcileEndpoints(t.Context(), run.hosted, run.controlPlane, testNamespace, openshiftOAuthAPIServer)
		hostedOperations := run.trace.Cluster("hosted")

		g.Expect(err).To(MatchError("openshift oauth apiserver service in control plane does not yet have a cluster IP"))
		g.Expect(hostedOperations).To(BeEmpty())
		expectEndpoints(g, run.hostedClient, manifests.OpenShiftOAuthAPIServerClusterEndpoints(), "10.0.0.1")
	})

	t.Run("When the control-plane service is missing, it should wrap the not-found error", func(t *testing.T) {
		g := NewWithT(t)
		run := newProjectionRun(projectionFixture{})

		err := reconcileEndpoints(t.Context(), run.hosted, run.controlPlane, testNamespace, openshiftAPIServer)

		g.Expect(err).To(MatchError(`failed to get openshift apiserver service from control plane: services "openshift-apiserver" not found`))
		g.Expect(apierrors.IsNotFound(errors.Unwrap(err))).To(BeTrue())
	})

	t.Run("When the hosted write fails, it should return the error unwrapped", func(t *testing.T) {
		g := NewWithT(t)
		failure := errors.New("write failure")
		run := newProjectionRun(projectionFixture{
			controlPlane:   []client.Object{openshiftAPIServerControlPlaneService("172.30.0.10")},
			upsertFailures: map[string]error{testtrace.ObjectKey(manifests.OpenShiftAPIServerClusterEndpoints()): failure},
		})

		g.Expect(reconcileEndpoints(t.Context(), run.hosted, run.controlPlane, testNamespace, openshiftAPIServer)).To(BeIdenticalTo(failure))
	})
}
