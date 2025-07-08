package sharedingress

import (
	ctx "context"
	"reflect"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	api "github.com/openshift/hypershift/support/api"
	testutil "github.com/openshift/hypershift/support/testutil"
	upsert "github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"

	appsv1 "k8s.io/api/apps/v1"
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

			r := SharedIngressReconciler{
				Client:         fakeClient,
				createOrUpdate: upsert.New(false).CreateOrUpdate,
			}
			config, _, err := r.generateConfig(ctx.Background())
			g.Expect(err).ToNot(HaveOccurred())
			testutil.CompareWithFixture(t, config, testutil.WithExtension(".cfg"))
		})
	}
}

func TestReconcileRouterDeployment(t *testing.T) {
	type args struct {
		deployment *appsv1.Deployment
		configMap  *corev1.ConfigMap
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "Valid config map and deployment",
			args: args{
				deployment: &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-deployment",
						Namespace: "test-namespace",
					},
				},
				configMap: &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-configmap",
						Namespace: "test-namespace",
					},
					Data: map[string]string{
						routerConfigKey: "test-config",
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ReconcileRouterDeployment(tt.args.deployment, tt.args.configMap)
			if (err != nil) != tt.wantErr {
				t.Errorf("ReconcileRouterDeployment() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if *tt.args.deployment.Spec.Replicas != 2 {
					t.Errorf("Expected replicas to be 2, got %d", *tt.args.deployment.Spec.Replicas)
				}
				if tt.args.deployment.Spec.Template.Annotations[routerConfigHashKey] != util.ComputeHash(tt.args.configMap.Data[routerConfigKey]) {
					t.Errorf("Expected annotation %s to be %s, got %s", routerConfigHashKey, util.ComputeHash(tt.args.configMap.Data[routerConfigKey]), tt.args.deployment.Spec.Template.Annotations[routerConfigHashKey])
				}
				expectedAffinity := &corev1.Affinity{
					PodAntiAffinity: &corev1.PodAntiAffinity{
						PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
							{
								Weight: 100,
								PodAffinityTerm: corev1.PodAffinityTerm{
									LabelSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											"app": "router",
										},
									},
									TopologyKey: "topology.kubernetes.io/zone",
								},
							},
						},
					},
				}
				if !reflect.DeepEqual(tt.args.deployment.Spec.Template.Spec.Affinity, expectedAffinity) {
					t.Errorf("Expected affinity to be %v, got %v", expectedAffinity, tt.args.deployment.Spec.Template.Spec.Affinity)
				}
			}
		})
	}
}
