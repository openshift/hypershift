package sharedingressconfiggenerator

import (
	"bytes"
	ctx "context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	api "github.com/openshift/hypershift/support/api"
	testutil "github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	fake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGenerateConfig(t *testing.T) {
	// Library to create Routes and SVCs.
	namedRoute := func(r *routev1.Route, mods ...func(*routev1.Route)) *routev1.Route {
		r.Labels = map[string]string{
			util.HCPRouteLabel: "test-ns-clustername",
		}
		for _, m := range mods {
			m(r)
		}
		return r
	}
	route := func(name, namespace string, mods ...func(*routev1.Route)) *routev1.Route {
		r := &routev1.Route{}
		r.Name = name
		r.Namespace = namespace
		return namedRoute(r, mods...)
	}
	withHost := func(host string) func(*routev1.Route) {
		return func(r *routev1.Route) {
			r.Spec.Host = host
		}
	}
	withSvc := func(svc string) func(*routev1.Route) {
		return func(r *routev1.Route) {
			r.Spec.To.Name = svc
			r.Spec.To.Kind = "Service"
		}
	}

	namedSVC := func(svc *corev1.Service, mods ...func(*corev1.Service)) *corev1.Service {
		for _, m := range mods {
			m(svc)
		}
		return svc
	}
	svc := func(name, namespace string, mods ...func(*corev1.Service)) *corev1.Service {
		s := &corev1.Service{}
		s.Name = name
		s.Namespace = namespace
		return namedSVC(s, mods...)
	}
	withClusterIP := func(ip string) func(*corev1.Service) {
		return func(s *corev1.Service) {
			s.Spec.ClusterIP = ip
		}
	}
	withPort := func(port int32) func(*corev1.Service) {
		return func(s *corev1.Service) {
			s.Spec.Ports = []corev1.ServicePort{
				{
					Port: port,
				},
			}
		}
	}

	// Test cases.
	testNamespace1 := "test-hc1"
	testNamespace2 := "test-hc2"
	testCases := []struct {
		name                   string
		hostedClusterResources []struct {
			hostedCluster *hyperv1.HostedCluster
			routes        []client.Object
			svcs          []client.Object
		}
	}{
		{
			name: "When there's two HostedClusters with all their Routes and SVCs it should generate the config with frontends and backends for both",
			hostedClusterResources: []struct {
				hostedCluster *hyperv1.HostedCluster
				routes        []client.Object
				svcs          []client.Object
			}{
				{
					hostedCluster: &hyperv1.HostedCluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "hc1",
							Namespace: "test",
						},
						Spec: hyperv1.HostedClusterSpec{
							ClusterID:            "hc1-UUID",
							KubeAPIServerDNSName: "kube-apiserver-public-custom.example.com",
						},
					},
					routes: []client.Object{
						route(ignitionserver.Route("").Name, testNamespace1, withHost("ignition-server.example.com"), withSvc("ignition-server-proxy")),
						route(manifests.KonnectivityServerRoute("").Name, testNamespace1, withHost("konnectivity.example.com"), withSvc("konnectivity-server")),
						route(manifests.OauthServerExternalPublicRoute("").Name, testNamespace1, withHost("oauth-public.example.com"), withSvc("openshift-oauth")),
						route(manifests.KubeAPIServerExternalPublicRoute("").Name, testNamespace1, withHost("kube-apiserver-public.example.com"), withSvc("kube-apiserver")),
					},
					svcs: []client.Object{
						svc("ignition-server-proxy", testNamespace1, withClusterIP("1.1.1.1")),
						svc("konnectivity-server", testNamespace1, withClusterIP("2.2.2.2")),
						svc("openshift-oauth", testNamespace1, withClusterIP("3.3.3.3")),
						svc("kube-apiserver", testNamespace1, withClusterIP("4.4.4.4"), withPort(int32(6443))),
					},
				},
				{
					hostedCluster: &hyperv1.HostedCluster{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "hc2",
							Namespace: "test",
						},
						Spec: hyperv1.HostedClusterSpec{
							ClusterID: "hc2-UUID",
							Networking: hyperv1.ClusterNetworking{
								APIServer: &hyperv1.APIServerNetworking{
									AllowedCIDRBlocks: []hyperv1.CIDRBlock{
										"1.1.1.1/32",
										"192.168.1.1/24",
									},
								},
							},
						},
					},
					routes: []client.Object{
						route(ignitionserver.Route("").Name, testNamespace2, withHost("ignition-server.example.com"), withSvc("ignition-server-proxy")),
						route(manifests.KonnectivityServerRoute("").Name, testNamespace2, withHost("konnectivity.example.com"), withSvc("konnectivity-server")),
						route(manifests.OauthServerExternalPublicRoute("").Name, testNamespace2, withHost("oauth-public.example.com"), withSvc("openshift-oauth")),
						route(manifests.KubeAPIServerExternalPublicRoute("").Name, testNamespace2, withHost("kube-apiserver-public.example.com"), withSvc("kube-apiserver")),
					},
					svcs: []client.Object{
						svc("ignition-server-proxy", testNamespace2, withClusterIP("1.1.1.1")),
						svc("konnectivity-server", testNamespace2, withClusterIP("2.2.2.2")),
						svc("openshift-oauth", testNamespace2, withClusterIP("3.3.3.3")),
						svc("kube-apiserver", testNamespace2, withClusterIP("4.4.4.4"), withPort(int32(6443))),
					},
				},
			},
		},
	}

	indexFunc := func(obj client.Object) []string {
		svc, ok := obj.(*corev1.Service)
		if !ok {
			return nil
		}
		return []string{svc.Name}
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			builder := fake.NewClientBuilder().WithScheme(api.Scheme)
			objects := []client.Object{}
			for i := range tc.hostedClusterResources {
				objects = append(objects, tc.hostedClusterResources[i].hostedCluster)
				objects = append(objects, tc.hostedClusterResources[i].routes...)
				objects = append(objects, tc.hostedClusterResources[i].svcs...)
			}
			fakeClient := builder.WithObjects(objects...).
				WithIndex(&corev1.Service{}, "metadata.name", indexFunc).
				Build()

			var buffer bytes.Buffer
			err := generateRouterConfig(ctx.Background(), fakeClient, &buffer)
			g.Expect(err).ToNot(HaveOccurred())

			testutil.CompareWithFixture(t, buffer.Bytes(), testutil.WithExtension(".cfg"))
		})
	}
}
