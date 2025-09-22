package hostedcontrolplane

import (
	"context"
	_ "embed"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/autoscaler"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ingress"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oauth"
	etcdv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/etcd"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/fg"
	ignitionserverv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/ignitionserver"
	ignitionproxyv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/ignitionserver_proxy"
	kasv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	"github.com/openshift/hypershift/support/api"
	autoscalercommon "github.com/openshift/hypershift/support/autoscaler"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/releaseinfo"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/image/docker10"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	"github.com/go-logr/zapr"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap/zaptest"
)

type fakeEC2Client struct {
	ec2iface.EC2API
}

func (*fakeEC2Client) DescribeVpcEndpointsWithContext(aws.Context, *ec2.DescribeVpcEndpointsInput, ...request.Option) (*ec2.DescribeVpcEndpointsOutput, error) {
	return &ec2.DescribeVpcEndpointsOutput{}, fmt.Errorf("not ready")
}

func TestReconcileKubeadminPassword(t *testing.T) {
	targetNamespace := "test"

	testsCases := []struct {
		name                 string
		hcp                  *hyperv1.HostedControlPlane
		expectedOutputSecret *corev1.Secret
	}{
		{
			name: "When OAuth config specified results in no kubeadmin secret",
			hcp: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: targetNamespace,
					Name:      "cluster1",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						OAuth: &configv1.OAuthSpec{
							IdentityProviders: []configv1.IdentityProvider{
								{
									IdentityProviderConfig: configv1.IdentityProviderConfig{
										Type: configv1.IdentityProviderTypeOpenID,
										OpenID: &configv1.OpenIDIdentityProvider{
											ClientID: "clientid1",
											ClientSecret: configv1.SecretNameReference{
												Name: "clientid1-secret-name",
											},
											Issuer: "https://example.com/identity",
											Claims: configv1.OpenIDClaims{
												Email:             []string{"email"},
												Name:              []string{"clientid1-secret-name"},
												PreferredUsername: []string{"preferred_username"},
											},
										},
									},
									Name:          "IAM",
									MappingMethod: "lookup",
								},
							},
						},
					},
				},
			},
			expectedOutputSecret: nil,
		},
		{
			name: "When Oauth config not specified results in default kubeadmin secret",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: targetNamespace,
					Name:      "cluster1",
				},
			},
			expectedOutputSecret: common.KubeadminPasswordSecret(targetNamespace),
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			fakeClient := fake.NewClientBuilder().Build()
			r := &HostedControlPlaneReconciler{
				Client: fakeClient,
				Log:    ctrl.LoggerFrom(context.TODO()),
			}
			err := r.reconcileKubeadminPassword(context.Background(), tc.hcp, tc.hcp.Spec.Configuration != nil && tc.hcp.Spec.Configuration.OAuth != nil, controllerutil.CreateOrUpdate)
			g.Expect(err).NotTo(HaveOccurred())

			actualSecret := common.KubeadminPasswordSecret(targetNamespace)
			err = fakeClient.Get(context.Background(), client.ObjectKeyFromObject(actualSecret), actualSecret)
			if tc.expectedOutputSecret != nil {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(actualSecret.Data).To(HaveKey("password"))
				g.Expect(actualSecret.Data["password"]).ToNot(BeEmpty())
			} else {
				if !apierrors.IsNotFound(err) {
					g.Expect(err).NotTo(HaveOccurred())
				}
			}
		})
	}
}

func TestBuildOAuthVolumeTemplates(t *testing.T) {
	testsCases := []struct {
		name                   string
		params                 oauth.OAuthConfigParams
		expectedLoginSecret    string
		expectedProviderSecret string
		expectedErrorSecret    string
	}{
		{
			name: "When OAuthTemplates has secret names specified, they should be used in volume",
			params: oauth.OAuthConfigParams{
				OAuthTemplates: configv1.OAuthTemplates{
					Login: configv1.SecretNameReference{
						Name: "custom-login-template-secret",
					},
					ProviderSelection: configv1.SecretNameReference{
						Name: "custom-provider-selection-template-secret",
					},
					Error: configv1.SecretNameReference{
						Name: "custom-error-template-secret",
					},
				},
			},
			expectedLoginSecret:    "custom-login-template-secret",
			expectedProviderSecret: "custom-provider-selection-template-secret",
			expectedErrorSecret:    "custom-error-template-secret",
		},
		{
			name:                   "When OAuthTemplates is empty, it should use default secrets",
			params:                 oauth.OAuthConfigParams{},
			expectedLoginSecret:    manifests.OAuthServerDefaultLoginTemplateSecret("").Name,
			expectedProviderSecret: manifests.OAuthServerDefaultProviderSelectionTemplateSecret("").Name,
			expectedErrorSecret:    manifests.OAuthServerDefaultErrorTemplateSecret("").Name,
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			loginVolume := &corev1.Volume{}
			providerVolume := &corev1.Volume{}
			errorVolume := &corev1.Volume{}

			oauth.BuildOAuthVolumeLoginTemplate(loginVolume, &tc.params)
			oauth.BuildOAuthVolumeProvidersTemplate(providerVolume, &tc.params)
			oauth.BuildOAuthVolumeErrorTemplate(errorVolume, &tc.params)

			// Check Login Template
			actualLoginSecretName := loginVolume.Secret.SecretName
			if actualLoginSecretName != tc.expectedLoginSecret {
				t.Errorf("Expected login secret name %s, but got %s", tc.expectedLoginSecret, actualLoginSecretName)
			}

			// Check Provider Template
			actualProviderSecretName := providerVolume.Secret.SecretName
			if actualProviderSecretName != tc.expectedProviderSecret {
				t.Errorf("Expected provider secret name %s, but got %s", tc.expectedProviderSecret, actualProviderSecretName)
			}

			// Check Error Template
			actualErrorSecretName := errorVolume.Secret.SecretName
			if actualErrorSecretName != tc.expectedErrorSecret {
				t.Errorf("Expected error secret name %s, but got %s", tc.expectedErrorSecret, actualErrorSecretName)
			}
		})
	}
}

func TestReconcileOAuthService(t *testing.T) {
	targetNamespace := "test"
	apiPort := int32(config.KASSVCPort)
	hostname := "test.example.com"
	allowCIDR := []hyperv1.CIDRBlock{"1.2.3.4/24"}
	ipFamilyPolicy := corev1.IPFamilyPolicyPreferDualStack

	ownerRef := metav1.OwnerReference{
		APIVersion:         "hypershift.openshift.io/v1beta1",
		Kind:               "HostedControlPlane",
		Name:               "test",
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
	oauthPublicService := func(m ...func(*corev1.Service)) corev1.Service {
		svc := corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:       targetNamespace,
				Name:            manifests.OauthServerService(targetNamespace).Name,
				OwnerReferences: []metav1.OwnerReference{ownerRef},
			},
			Spec: corev1.ServiceSpec{
				Type:           corev1.ServiceTypeClusterIP,
				IPFamilyPolicy: &ipFamilyPolicy,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       apiPort,
						TargetPort: intstr.FromInt32(apiPort),
					},
				},
				Selector: map[string]string{
					"app": "oauth-openshift",
					"hypershift.openshift.io/control-plane-component": "oauth-openshift",
				},
			},
		}
		for _, m := range m {
			m(&svc)
		}
		return svc
	}
	oauthExternalPublicRoute := func(m ...func(*routev1.Route)) routev1.Route {
		route := routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: targetNamespace,
				Name:      "oauth",
				Labels: map[string]string{
					"hypershift.openshift.io/hosted-control-plane": targetNamespace,
				},
				OwnerReferences: []metav1.OwnerReference{ownerRef},
			},
			Spec: routev1.RouteSpec{
				Host: hostname,
				To: routev1.RouteTargetReference{
					Kind: "Service",
					Name: manifests.OauthServerService("").Name,
				},
				TLS: &routev1.TLSConfig{
					Termination:                   routev1.TLSTerminationPassthrough,
					InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
				},
			},
		}
		for _, m := range m {
			m(&route)
		}
		return route
	}
	oauthInternalRoute := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      "oauth-internal",
			Labels: map[string]string{
				"hypershift.openshift.io/hosted-control-plane": targetNamespace,
				"hypershift.openshift.io/internal-route":       "true",
			},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: routev1.RouteSpec{
			Host: "oauth.apps.test.hypershift.local",
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: manifests.OauthServerService("").Name,
			},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationPassthrough,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
			},
		},
	}
	testsCases := []struct {
		name                    string
		endpointAccess          hyperv1.AWSEndpointAccessType
		oauthPublishingStrategy hyperv1.ServicePublishingStrategy

		expectedServices []corev1.Service
		expectedRoutes   []routev1.Route
	}{
		{
			name:           "Route strategy, Public",
			endpointAccess: hyperv1.Public,
			oauthPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{
					Hostname: hostname,
				},
			},
			expectedServices: []corev1.Service{
				oauthPublicService(func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
				}),
			},
			expectedRoutes: []routev1.Route{
				oauthExternalPublicRoute(),
			},
		},
		{
			name:           "Route strategy, PublicPrivate",
			endpointAccess: hyperv1.PublicAndPrivate,
			oauthPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{
					Hostname: hostname,
				},
			},

			expectedServices: []corev1.Service{
				oauthPublicService(func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
				}),
			},
			expectedRoutes: []routev1.Route{
				oauthExternalPublicRoute(),
				oauthInternalRoute,
			},
		},
		{
			name:           "Route strategy, PublicPrivate, no hostname",
			endpointAccess: hyperv1.PublicAndPrivate,
			oauthPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
			},

			expectedServices: []corev1.Service{
				oauthPublicService(func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
				}),
			},
			expectedRoutes: []routev1.Route{
				oauthExternalPublicRoute(func(s *routev1.Route) {
					s.Spec.Host = ""
					// The route should not be admitted by the private router.
					delete(s.Labels, "hypershift.openshift.io/hosted-control-plane")
				}),
				oauthInternalRoute,
			},
		},
		{
			name:           "Route strategy, Private",
			endpointAccess: hyperv1.Private,
			oauthPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type:  hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{},
			},
			expectedServices: []corev1.Service{
				oauthPublicService(func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
				}),
			},
			expectedRoutes: []routev1.Route{
				oauthInternalRoute,
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: targetNamespace,
					Name:      "test",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							Port:              &apiPort,
							AllowedCIDRBlocks: allowCIDR,
						},
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: tc.endpointAccess,
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{{
						Service:                   hyperv1.OAuthServer,
						ServicePublishingStrategy: tc.oauthPublishingStrategy,
					}},
				},
			}

			ctx := ctrl.LoggerInto(context.Background(), zapr.NewLogger(zaptest.NewLogger(t)))

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			r := &HostedControlPlaneReconciler{
				Client: fakeClient,
				Log:    ctrl.LoggerFrom(ctx),
			}

			if err := r.reconcileOAuthServerService(ctx, hcp, controllerutil.CreateOrUpdate); err != nil {
				t.Fatalf("reconcileOAuthServerService failed: %v", err)
			}

			var actualServices corev1.ServiceList
			if err := fakeClient.List(ctx, &actualServices); err != nil {
				t.Fatalf("failed to list services: %v", err)
			}

			if diff := testutil.MarshalYamlAndDiff(&actualServices, &corev1.ServiceList{Items: tc.expectedServices}, t); diff != "" {
				t.Errorf("actual services differ from expected: %s", diff)
			}

			var actualRoutes routev1.RouteList
			if err := fakeClient.List(ctx, &actualRoutes); err != nil {
				t.Fatalf("failed to list routes: %v", err)
			}
			if diff := testutil.MarshalYamlAndDiff(&actualRoutes, &routev1.RouteList{Items: tc.expectedRoutes}, t); diff != "" {
				t.Errorf("actual routes differ from expected: %s", diff)
			}
		})
	}
}

func TestReconcileAPIServerService(t *testing.T) {
	targetNamespace := "test"
	apiPort := int32(config.KASSVCPort)
	kasPort := "client"
	hostname := "test.example.com"
	allowCIDR := []hyperv1.CIDRBlock{"1.2.3.4/24"}
	allowCIDRString := []string{"1.2.3.4/24"}
	ipFamilyPolicy := corev1.IPFamilyPolicyPreferDualStack

	ownerRef := metav1.OwnerReference{
		APIVersion:         "hypershift.openshift.io/v1beta1",
		Kind:               "HostedControlPlane",
		Name:               "test",
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
	kasPublicService := func(m ...func(*corev1.Service)) corev1.Service {
		svc := corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: targetNamespace,
				Name:      manifests.KubeAPIServerService(targetNamespace).Name,
				Annotations: map[string]string{
					"service.beta.kubernetes.io/aws-load-balancer-type": "nlb",
					hyperv1.ExternalDNSHostnameAnnotation:               hostname,
				},
				Labels: map[string]string{
					"app": "kube-apiserver",
					"hypershift.openshift.io/control-plane-component": "kube-apiserver",
				},
				OwnerReferences: []metav1.OwnerReference{ownerRef},
			},
			Spec: corev1.ServiceSpec{
				Type:           corev1.ServiceTypeLoadBalancer,
				IPFamilyPolicy: &ipFamilyPolicy,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       apiPort,
						TargetPort: intstr.FromString(kasPort),
					},
				},
				LoadBalancerSourceRanges: allowCIDRString,
				Selector: map[string]string{
					"app": "kube-apiserver",
					"hypershift.openshift.io/control-plane-component": "kube-apiserver",
				},
			},
		}
		for _, m := range m {
			m(&svc)
		}
		return svc
	}
	kasPrivateService := func(m ...func(*corev1.Service)) corev1.Service {
		return kasPublicService(append(m, func(s *corev1.Service) {
			s.Name = manifests.KubeAPIServerPrivateService(targetNamespace).Name

			delete(s.Annotations, hyperv1.ExternalDNSHostnameAnnotation)
			s.Annotations["service.beta.kubernetes.io/aws-load-balancer-internal"] = "true"

			s.Labels = nil

			s.Spec.LoadBalancerSourceRanges = nil
		})...)
	}
	withCrossZoneAnnotation := func(svc *corev1.Service) {
		svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled"] = "true"
	}
	kasExternalPublicRoute := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      "kube-apiserver",
			Labels: map[string]string{
				"hypershift.openshift.io/hosted-control-plane": targetNamespace,
			},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: routev1.RouteSpec{
			Host: hostname,
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: manifests.KubeAPIServerService("").Name,
			},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationPassthrough,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
			},
		},
	}
	kasExternalPrivateRoute := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      "kube-apiserver-private",
			Labels: map[string]string{
				"hypershift.openshift.io/hosted-control-plane": targetNamespace,
				hyperv1.RouteVisibilityLabel:                   string(hyperv1.RouteVisibilityPrivate),
				util.InternalRouteLabel:                        "true",
			},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: routev1.RouteSpec{
			Host: hostname,
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: manifests.KubeAPIServerService("").Name,
			},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationPassthrough,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
			},
		},
	}
	kasInternalRoute := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      "kube-apiserver-internal",
			Labels: map[string]string{
				"hypershift.openshift.io/hosted-control-plane": targetNamespace,
				"hypershift.openshift.io/internal-route":       "true",
			},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: routev1.RouteSpec{
			Host: "api.test.hypershift.local",
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: manifests.KubeAPIServerService("").Name,
			},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationPassthrough,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
			},
		},
	}
	testsCases := []struct {
		name                  string
		endpointAccess        hyperv1.AWSEndpointAccessType
		apiPublishingStrategy hyperv1.ServicePublishingStrategy

		expectedServices []corev1.Service
		expectedRoutes   []routev1.Route
	}{
		{
			name:           "LB strategy, public",
			endpointAccess: hyperv1.Public,
			apiPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					Hostname: hostname,
				},
			},

			expectedServices: []corev1.Service{
				kasPublicService(),
			},
		},
		{
			name:           "LB strategy, publicPrivate",
			endpointAccess: hyperv1.PublicAndPrivate,
			apiPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					Hostname: hostname,
				},
			},

			expectedServices: []corev1.Service{
				kasPublicService(withCrossZoneAnnotation),
				kasPrivateService(withCrossZoneAnnotation),
			},
		},
		{
			name:           "LB strategy, private",
			endpointAccess: hyperv1.Private,
			apiPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					Hostname: hostname,
				},
			},

			expectedServices: []corev1.Service{
				kasPublicService(func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
					delete(s.Annotations, "external-dns.alpha.kubernetes.io/hostname")
				}),
				kasPrivateService(withCrossZoneAnnotation),
			},
		},
		{
			name:           "Route strategy, public",
			endpointAccess: hyperv1.Public,
			apiPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{
					Hostname: hostname,
				},
			},

			expectedServices: []corev1.Service{
				kasPublicService(func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
					delete(s.Annotations, "external-dns.alpha.kubernetes.io/hostname")
				}),
			},
			expectedRoutes: []routev1.Route{
				kasExternalPublicRoute,
				kasInternalRoute,
			},
		},
		{
			name:           "Route strategy, publicPrivate",
			endpointAccess: hyperv1.PublicAndPrivate,
			apiPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{
					Hostname: hostname,
				},
			},

			expectedServices: []corev1.Service{
				kasPublicService(func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
					delete(s.Annotations, "external-dns.alpha.kubernetes.io/hostname")
				}),
			},
			expectedRoutes: []routev1.Route{
				kasExternalPublicRoute,
				kasInternalRoute,
			},
		},
		{
			name:           "Route strategy, private",
			endpointAccess: hyperv1.Private,
			apiPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{
					Hostname: hostname,
				},
			},

			expectedServices: []corev1.Service{
				kasPublicService(func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
					delete(s.Annotations, "external-dns.alpha.kubernetes.io/hostname")
				}),
			},
			expectedRoutes: []routev1.Route{
				kasInternalRoute,
				kasExternalPrivateRoute,
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: targetNamespace,
					Name:      "test",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							Port:              &apiPort,
							AllowedCIDRBlocks: allowCIDR,
						},
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: tc.endpointAccess,
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{{
						Service:                   hyperv1.APIServer,
						ServicePublishingStrategy: tc.apiPublishingStrategy,
					}},
				},
			}

			ctx := ctrl.LoggerInto(context.Background(), zapr.NewLogger(zaptest.NewLogger(t)))

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			r := &HostedControlPlaneReconciler{
				Client: fakeClient,
				Log:    ctrl.LoggerFrom(ctx),
			}

			if err := r.reconcileAPIServerService(ctx, hcp, controllerutil.CreateOrUpdate); err != nil {
				t.Fatalf("reconcileAPIServerService failed: %v", err)
			}

			var actualServices corev1.ServiceList
			if err := fakeClient.List(ctx, &actualServices); err != nil {
				t.Fatalf("failed to list services: %v", err)
			}

			if diff := testutil.MarshalYamlAndDiff(&actualServices, &corev1.ServiceList{Items: tc.expectedServices}, t); diff != "" {
				t.Errorf("actual services differ from expected: %s", diff)
			}

			var actualRoutes routev1.RouteList
			if err := fakeClient.List(ctx, &actualRoutes); err != nil {
				t.Fatalf("failed to list routes: %v", err)
			}
			if diff := testutil.MarshalYamlAndDiff(&actualRoutes, &routev1.RouteList{Items: tc.expectedRoutes}, t); diff != "" {
				t.Errorf("actual routes differ from expected: %s", diff)
			}
		})
	}
}

// TestClusterAutoscalerArgs checks to make sure that fields specified in a ClusterAutoscaling spec
// become arguments to the autoscaler.
func TestClusterAutoscalerArgs(t *testing.T) {
	IgnoreLabelArgs := make([]string, 0)
	for _, v := range autoscalercommon.GetIgnoreLabels(hyperv1.AWSPlatform) {
		IgnoreLabelArgs = append(IgnoreLabelArgs, fmt.Sprintf("%s=%v", autoscaler.BalancingIgnoreLabelArg, v))
	}

	tests := map[string]struct {
		AutoscalerOptions   hyperv1.ClusterAutoscaling
		ExpectedArgs        []string
		ExpectedMissingArgs []string
	}{
		"contains only default arguments": {
			AutoscalerOptions: hyperv1.ClusterAutoscaling{},
			ExpectedArgs: []string{
				"--cloud-provider=clusterapi",
				"--node-group-auto-discovery=clusterapi:namespace=$(MY_NAMESPACE)",
				"--kubeconfig=/mnt/kubeconfig/target-kubeconfig",
				"--clusterapi-cloud-config-authoritative",
				"--skip-nodes-with-local-storage=false",
				"--alsologtostderr",
				"--v=4",
			},
			ExpectedMissingArgs: []string{
				"--max-nodes-total",
				"--max-graceful-termination-sec",
				"--max-node-provision-time",
				"--expendable-pods-priority-cutoff",
			},
		},
		"contains all optional parameters": {
			AutoscalerOptions: hyperv1.ClusterAutoscaling{
				MaxNodesTotal:        ptr.To[int32](100),
				MaxPodGracePeriod:    ptr.To[int32](300),
				MaxNodeProvisionTime: "20m",
				PodPriorityThreshold: ptr.To[int32](-5),
			},
			ExpectedArgs: []string{
				"--cloud-provider=clusterapi",
				"--node-group-auto-discovery=clusterapi:namespace=$(MY_NAMESPACE)",
				"--kubeconfig=/mnt/kubeconfig/target-kubeconfig",
				"--clusterapi-cloud-config-authoritative",
				"--skip-nodes-with-local-storage=false",
				"--alsologtostderr",
				"--v=4",
				"--max-nodes-total=100",
				"--max-graceful-termination-sec=300",
				"--max-node-provision-time=20m",
				"--expendable-pods-priority-cutoff=-5",
			},
			ExpectedMissingArgs: []string{},
		},
		"balancing ignore labels": {
			AutoscalerOptions: hyperv1.ClusterAutoscaling{},
			ExpectedArgs:      IgnoreLabelArgs,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			deployment := manifests.AutoscalerDeployment("test-ns")
			sa := manifests.AutoscalerServiceAccount("test-ns")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test-secret",
				},
			}
			hcp := &hyperv1.HostedControlPlane{}
			hcp.Name = "name"
			hcp.Namespace = "namespace"
			hcp.Spec.Platform.Type = hyperv1.AWSPlatform
			err := autoscaler.ReconcileAutoscalerDeployment(deployment, hcp, sa, secret, test.AutoscalerOptions, "clusterAutoscalerImage", "availabilityProberImage", false, config.OwnerRefFrom(hcp))
			if err != nil {
				t.Error(err)
			}

			observedArgs := sets.NewString(deployment.Spec.Template.Spec.Containers[0].Args...)
			for _, arg := range test.ExpectedArgs {
				if !observedArgs.Has(arg) {
					t.Errorf("Expected to find \"%s\" in observed arguments: %v", arg, observedArgs)
				}
			}

			for _, arg := range test.ExpectedMissingArgs {
				if observedArgs.Has(arg) {
					t.Errorf("Did not expect to find \"%s\" in observed arguments", arg)
				}
			}
		})
	}
}

func TestEtcdRestoredCondition(t *testing.T) {
	testsCases := []struct {
		name              string
		sts               *appsv1.StatefulSet
		pods              []corev1.Pod
		expectedCondition metav1.Condition
	}{
		{
			name: "single replica, pod ready - condition true",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: "thens",
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](1),
				},
				Status: appsv1.StatefulSetStatus{
					Replicas:      1,
					ReadyReplicas: 1,
				},
			},
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-0",
						Namespace: "thens",
						Labels: map[string]string{
							"app": "etcd",
						},
					},
					Status: corev1.PodStatus{
						InitContainerStatuses: []corev1.ContainerStatus{
							{
								Name:  "etcd-init",
								Ready: true,
							},
						},
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:   string(hyperv1.EtcdSnapshotRestored),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.AsExpectedReason,
			},
		},
		{
			name: "Pod not ready - condition false",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: "thens",
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](1),
				},
				Status: appsv1.StatefulSetStatus{
					Replicas:      1,
					ReadyReplicas: 1,
				},
			},
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-0",
						Namespace: "thens",
						Labels: map[string]string{
							"app": "etcd",
						},
					},
					Status: corev1.PodStatus{
						InitContainerStatuses: []corev1.ContainerStatus{
							{
								Name:  "etcd-init",
								Ready: false,
								LastTerminationState: corev1.ContainerState{
									Terminated: &corev1.ContainerStateTerminated{
										ExitCode: 1,
										Reason:   "somethingfailed",
									},
								},
							},
						},
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:   string(hyperv1.EtcdSnapshotRestored),
				Status: metav1.ConditionFalse,
				Reason: "somethingfailed",
			},
		},
		{
			name: "multiple replica, pods ready - condition true",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: "thens",
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.StatefulSetStatus{
					Replicas:      3,
					ReadyReplicas: 3,
				},
			},
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-0",
						Namespace: "thens",
						Labels: map[string]string{
							"app": "etcd",
						},
					},
					Status: corev1.PodStatus{
						InitContainerStatuses: []corev1.ContainerStatus{
							{
								Name:  "etcd-init",
								Ready: true,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-1",
						Namespace: "thens",
						Labels: map[string]string{
							"app": "etcd",
						},
					},
					Status: corev1.PodStatus{
						InitContainerStatuses: []corev1.ContainerStatus{
							{
								Name:  "etcd-init",
								Ready: true,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-2",
						Namespace: "thens",
						Labels: map[string]string{
							"app": "etcd",
						},
					},
					Status: corev1.PodStatus{
						InitContainerStatuses: []corev1.ContainerStatus{
							{
								Name:  "etcd-init",
								Ready: true,
							},
						},
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:   string(hyperv1.EtcdSnapshotRestored),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.AsExpectedReason,
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			podList := &corev1.PodList{
				Items: tc.pods,
			}
			fakeClient := fake.NewClientBuilder().WithLists(podList).Build()
			r := &HostedControlPlaneReconciler{
				Client: fakeClient,
				Log:    ctrl.LoggerFrom(context.TODO()),
			}

			conditionPtr := r.etcdRestoredCondition(context.Background(), tc.sts)
			g.Expect(conditionPtr).ToNot(BeNil())
			g.Expect(*conditionPtr).To(Equal(tc.expectedCondition))
		})
	}
}

func sampleHCP(t *testing.T) *hyperv1.HostedControlPlane {
	t.Helper()
	rawHCP := `apiVersion: hypershift.openshift.io/v1beta1
kind: HostedControlPlane
metadata:
  annotations:
    hypershift.openshift.io/cluster: cewong/cewong-dev
  finalizers:
  - hypershift.openshift.io/finalizer
  generation: 1
  labels:
    cluster.x-k8s.io/cluster-name: cewong-dev-4nvh8
  name: foo
  namespace: bar
  ownerReferences:
  - apiVersion: cluster.x-k8s.io/v1beta1
    blockOwnerDeletion: true
    controller: true
    kind: Cluster
    name: cewong-dev-4nvh8
    uid: 01657128-83b6-4ed8-8814-1d735a374d24
  resourceVersion: "216428710"
  uid: ed1353cb-758d-4c87-b302-233976f93271
spec:
  autoscaling: {}
  clusterID: 5878727a-1200-4fd5-802f-f0218b8af12c
  controllerAvailabilityPolicy: SingleReplica
  dns:
    baseDomain: hypershift.cesarwong.com
    privateZoneID: Z081271024WU1LT4DEEIV
    publicZoneID: Z0676342TNL7FZTLRDUL
  etcd:
    managed:
      storage:
        persistentVolume:
          size: 4Gi
        type: PersistentVolume
    managementType: Managed
  fips: false
  infraID: cewong-dev-4nvh8
  infrastructureAvailabilityPolicy: SingleReplica
  issuerURL: https://hypershift-ci-1-oidc.s3.us-east-1.amazonaws.com/cewong-dev-4nvh8
  machineCIDR: 10.0.0.0/16
  networkType: OVNKubernetes
  networking:
    clusterNetwork:
    - cidr: 10.132.0.0/14
    machineNetwork:
    - cidr: 10.0.0.0/16
    networkType: OVNKubernetes
    serviceNetwork:
    - cidr: 172.29.0.0/16
  olmCatalogPlacement: management
  platform:
    aws:
      cloudProviderConfig:
        subnet:
          id: subnet-099bf416521d0628a
        vpc: vpc-0d5303991a390921f
        zone: us-east-2a
      controlPlaneOperatorCreds: {}
      endpointAccess: Public
      kubeCloudControllerCreds:
        name: cloud-controller-creds
      nodePoolManagementCreds: {}
      region: us-east-2
      resourceTags:
      - key: kubernetes.io/cluster/cewong-dev-4nvh8
        value: owned
      roles:
      - arn: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-openshift-ingress
        name: cloud-credentials
        namespace: openshift-ingress-operator
      - arn: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-openshift-image-registry
        name: installer-cloud-credentials
        namespace: openshift-image-registry
      - arn: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-aws-ebs-csi-driver-controller
        name: ebs-cloud-credentials
        namespace: openshift-cluster-csi-drivers
      - arn: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-cloud-network-config-controller
        name: cloud-credentials
        namespace: openshift-cloud-network-config-controller
      rolesRef:
        controlPlaneOperatorARN: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-control-plane-operator
        imageRegistryARN: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-openshift-image-registry
        ingressARN: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-openshift-ingress
        kubeCloudControllerARN: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-cloud-controller
        networkARN: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-cloud-network-config-controller
        nodePoolManagementARN: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-node-pool
        storageARN: arn:aws:iam::820196288204:role/cewong-dev-4nvh8-aws-ebs-csi-driver-controller
    type: AWS
  pullSecret:
    name: pull-secret
  releaseImage: quay.io/openshift-release-dev/ocp-release:4.11.0-rc.4-x86_64
  secretEncryption:
    aescbc:
      activeKey:
        name: etcd-encryption-key
    type: aescbc
  services:
  - service: APIServer
    servicePublishingStrategy:
      type: LoadBalancer
  - service: OAuthServer
    servicePublishingStrategy:
      type: Route
  - service: Konnectivity
    servicePublishingStrategy:
      type: Route
  - service: Ignition
    servicePublishingStrategy:
      type: Route
  - service: OVNSbDb
    servicePublishingStrategy:
      type: Route
  sshKey:
    name: ssh-key`
	hcp := &hyperv1.HostedControlPlane{}
	if err := yaml.Unmarshal([]byte(rawHCP), hcp); err != nil {
		t.Fatal(err)
	}

	return hcp
}

func TestEventHandling(t *testing.T) {
	t.Parallel()

	hcp := sampleHCP(t)
	pullSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "pull-secret"}}
	etcdEncryptionKey := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "etcd-encryption-key"},
		Data:       map[string][]byte{"key": []byte("very-secret")},
	}
	fakeNodeTuningOperator := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-tuning-operator",
			Namespace: "bar",
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
		},
	}
	fakeNodeTuningOperatorTLS := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "node-tuning-operator-tls"},
		Data:       map[string][]byte{"key": []byte("very-secret")},
	}

	hcpGVK, err := apiutil.GVKForObject(hcp, api.Scheme)
	if err != nil {
		t.Fatalf("failed to determine gvk for %T: %v", hcp, err)
	}
	restMapper := meta.NewDefaultRESTMapper(nil)
	restMapper.Add(hcpGVK, meta.RESTScopeNamespace)
	c := &createTrackingClient{Client: fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(hcp, pullSecret, etcdEncryptionKey, fakeNodeTuningOperator, fakeNodeTuningOperatorTLS).
		WithStatusSubresource(&hyperv1.HostedControlPlane{}).
		WithRESTMapper(restMapper).
		Build(),
	}

	readyInfraStatus := infra.InfrastructureStatus{
		APIHost:          "foo",
		APIPort:          1,
		OAuthHost:        "foo",
		OAuthPort:        1,
		KonnectivityHost: "foo",
		KonnectivityPort: 1,
	}
	// Selftest, so this doesn't rot over time
	if !readyInfraStatus.IsReady() {
		t.Fatal("readyInfraStatus fixture is not actually ready")
	}
	mockCtrl := gomock.NewController(t)
	mockedProviderWithOpenshiftImageRegistryOverrides := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(testutils.InitReleaseImageOrDie("4.15.0"), nil).AnyTimes()

	r := &HostedControlPlaneReconciler{
		Client:                        c,
		ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		ReleaseProvider:               mockedProviderWithOpenshiftImageRegistryOverrides,
		UserReleaseProvider:           &fakereleaseprovider.FakeReleaseProvider{},
		reconcileInfrastructureStatus: func(context.Context, *hyperv1.HostedControlPlane) (infra.InfrastructureStatus, error) {
			return readyInfraStatus, nil
		},
		ec2Client: &fakeEC2Client{},
	}
	r.setup(controllerutil.CreateOrUpdate)
	ctx := ctrl.LoggerInto(context.Background(), zapr.NewLogger(zaptest.NewLogger(t)))

	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(hcp)}); err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}

	eventHandlerList := r.eventHandlers(c.Scheme(), c.RESTMapper())
	eventHandlersByObject := make(map[schema.GroupVersionKind]handler.EventHandler, len(eventHandlerList))
	for _, handler := range eventHandlerList {
		gvk, err := apiutil.GVKForObject(handler.obj, api.Scheme)
		if err != nil {
			t.Errorf("failed to get gvk for %T: %v", handler.obj, err)
		}
		eventHandlersByObject[gvk] = handler.handler
	}

	for _, createdObject := range c.created {
		t.Run(fmt.Sprintf("%T - %s", createdObject, createdObject.GetName()), func(t *testing.T) {
			gvk, err := apiutil.GVKForObject(createdObject, api.Scheme)
			if err != nil {
				t.Fatalf("failed to get gvk for %T: %v", createdObject, err)
			}
			handler, found := eventHandlersByObject[gvk]
			if !found {
				t.Fatalf("reconciler creates %T but has no handler for them", createdObject)
			}

			fakeQueue := &createTrackingWorkqueue{}
			handler.Create(context.Background(), event.CreateEvent{Object: createdObject}, fakeQueue)

			if len(fakeQueue.items) != 1 || fakeQueue.items[0].Namespace != hcp.Namespace || fakeQueue.items[0].Name != hcp.Name {
				t.Errorf("object %+v didn't correctly create event", createdObject)
			}
		})
	}
}

type createTrackingClient struct {
	created []client.Object
	client.Client
}

func (t *createTrackingClient) Create(ctx context.Context, o client.Object, opts ...client.CreateOption) error {
	if err := t.Client.Create(ctx, o, opts...); err != nil {
		return err
	}
	t.created = append(t.created, o)
	return nil
}

type createTrackingWorkqueue struct {
	items []reconcile.Request
	workqueue.TypedRateLimitingInterface[reconcile.Request]
}

func (c *createTrackingWorkqueue) Add(item reconcile.Request) {
	c.items = append(c.items, item)
}

func TestReconcileRouter(t *testing.T) {
	t.Parallel()

	const namespace = "test"
	routerCfg := manifests.RouterConfigurationConfigMap(namespace)
	err := ingress.ReconcileRouterConfiguration(config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
		Name:      "hcp",
		Namespace: namespace,
	}}), routerCfg, &routev1.RouteList{}, map[string]string{})
	if err != nil {
		t.Fatalf("%s", err.Error())
	}

	testCases := []struct {
		name                         string
		platformType                 hyperv1.PlatformType
		endpointAccess               hyperv1.AWSEndpointAccessType
		exposeAPIServerThroughRouter bool
		existingObjects              []client.Object
		expectedDeployments          []appsv1.Deployment
	}{
		{
			name:                         "IBM Cloud",
			platformType:                 hyperv1.IBMCloudPlatform,
			endpointAccess:               hyperv1.Public,
			exposeAPIServerThroughRouter: true,
		},
		{
			name:                         "Public HCP, uses public service host name",
			platformType:                 hyperv1.AWSPlatform,
			endpointAccess:               hyperv1.Public,
			exposeAPIServerThroughRouter: true,
			existingObjects:              []client.Object{},
			expectedDeployments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					_ = ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.HCPRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						routerCfg,
					)

					return *dep
				}(),
			},
		},
		{
			name:                         "PublicPrivate HCP, deployment gets hostname from public service",
			platformType:                 hyperv1.AWSPlatform,
			endpointAccess:               hyperv1.PublicAndPrivate,
			exposeAPIServerThroughRouter: true,
			existingObjects:              []client.Object{},
			expectedDeployments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					_ = ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.HCPRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						routerCfg,
					)

					return *dep
				}(),
			},
		},

		{
			name:                         "Private HCP, deployment gets hostname from private service",
			platformType:                 hyperv1.AWSPlatform,
			endpointAccess:               hyperv1.Private,
			exposeAPIServerThroughRouter: true,
			existingObjects:              []client.Object{},
			expectedDeployments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					_ = ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.HCPRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						routerCfg,
					)

					return *dep
				}(),
			},
		},
		{
			name:                         "Public HCP apiserver not exposed through router, nothing gets created",
			platformType:                 hyperv1.AWSPlatform,
			endpointAccess:               hyperv1.Public,
			exposeAPIServerThroughRouter: false,
		},
		{
			name:                         "PublicPrivate HCP apiserver not exposed through router, router without custom template and private router service get created",
			platformType:                 hyperv1.AWSPlatform,
			endpointAccess:               hyperv1.PublicAndPrivate,
			exposeAPIServerThroughRouter: false,
			expectedDeployments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					_ = ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.HCPRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						routerCfg,
					)

					return *dep
				}(),
			},
		},
		{
			name:                         "Private HCP apiserver not exposed through router, router without custom template and private router service get created",
			platformType:                 hyperv1.AWSPlatform,
			endpointAccess:               hyperv1.Private,
			exposeAPIServerThroughRouter: false,
			expectedDeployments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					_ = ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.HCPRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						routerCfg,
					)

					return *dep
				}(),
			},
		},
		{
			name:                         "Old router resources get cleaned up when exposed through route",
			platformType:                 hyperv1.AWSPlatform,
			endpointAccess:               hyperv1.PublicAndPrivate,
			exposeAPIServerThroughRouter: true,
			existingObjects: []client.Object{
				&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "private-router"}},
				&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "private-router"}},
				&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "private-router"}},
				&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "private-router"}},
			},
			expectedDeployments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					_ = ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.HCPRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						routerCfg,
					)

					return *dep
				}(),
			},
		},
		{
			name:                         "Old router resources get cleaned up when exposed through LB",
			platformType:                 hyperv1.AWSPlatform,
			endpointAccess:               hyperv1.PublicAndPrivate,
			exposeAPIServerThroughRouter: false,
			existingObjects: []client.Object{
				&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "private-router"}},
				&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "private-router"}},
				&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "private-router"}},
				&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "private-router"}},
			},
			expectedDeployments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					_ = ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.HCPRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						routerCfg,
					)

					return *dep
				}(),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			apiServerService := hyperv1.ServicePublishingStrategyMapping{
				Service: hyperv1.APIServer,
			}
			if tc.exposeAPIServerThroughRouter {
				apiServerService.Type = hyperv1.Route
				apiServerService.Route = &hyperv1.RoutePublishingStrategy{
					Hostname: "example.com",
				}
			}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tc.platformType,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: tc.endpointAccess,
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{apiServerService},
				},
			}

			ctx := ctrl.LoggerInto(context.Background(), zapr.NewLogger(zaptest.NewLogger(t)))
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(append(tc.existingObjects, hcp)...).Build()

			r := HostedControlPlaneReconciler{
				Client: c,
				Log:    ctrl.LoggerFrom(ctx),
			}

			releaseInfo := &releaseinfo.ReleaseImage{ImageStream: &imagev1.ImageStream{}}
			if useHCPRouter(hcp) {
				if err := r.reconcileRouter(ctx, hcp, imageprovider.New(releaseInfo), controllerutil.CreateOrUpdate); err != nil {
					t.Fatalf("reconcileRouter failed: %v", err)
				}
				if err := r.admitHCPManagedRoutes(ctx, hcp, "privateRouterHost", "publicRouterHost"); err != nil {
					t.Fatalf("admitHCPManagedRoutes failed: %v", err)
				}
				if err := r.cleanupOldRouterResources(ctx, hcp); err != nil {
					t.Fatalf("cleanupOldRouterResources failed: %v", err)
				}
			}

			var deployments appsv1.DeploymentList
			if err := c.List(ctx, &deployments); err != nil {
				t.Fatalf("failed to list deployments: %v", err)
			}
			if diff := testutil.MarshalYamlAndDiff(&deployments, &appsv1.DeploymentList{Items: tc.expectedDeployments}, t); diff != "" {
				t.Errorf("actual deployments differ from expected: %s", diff)
			}

			oldRouterResources := []client.Object{
				&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "private-router"}},
				&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "private-router"}},
				&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "private-router"}},
				&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "private-router"}},
			}
			for _, r := range oldRouterResources {
				if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); !apierrors.IsNotFound(err) {
					t.Errorf("expected %T %s to be deleted, wasn't the case (err=%v)", r, r.GetName(), err)
				}
			}
		})
	}
}

func TestNonReadyInfraTriggersRequeueAfter(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockedProviderWithOpenshiftImageRegistryOverrides := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(testutils.InitReleaseImageOrDie("4.15.0"), nil).AnyTimes()
	hcp := sampleHCP(t)
	pullSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "pull-secret"}}
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcp, pullSecret).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
	r := &HostedControlPlaneReconciler{
		Client:                        c,
		ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		ReleaseProvider:               mockedProviderWithOpenshiftImageRegistryOverrides,
		UserReleaseProvider:           &fakereleaseprovider.FakeReleaseProvider{},
		reconcileInfrastructureStatus: func(context.Context, *hyperv1.HostedControlPlane) (infra.InfrastructureStatus, error) {
			return infra.InfrastructureStatus{}, nil
		},
		ec2Client: &fakeEC2Client{},
	}
	r.setup(controllerutil.CreateOrUpdate)
	ctx := ctrl.LoggerInto(context.Background(), zapr.NewLogger(zaptest.NewLogger(t)))

	result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(hcp)})
	if err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("expected requeue after of %s when infrastructure is not ready, got %s", time.Minute, result.RequeueAfter)
	}
}

func TestReconcileHCPRouterServices(t *testing.T) {
	const namespace = "test-ns"
	publicService := func(m ...func(*corev1.Service)) *corev1.Service {
		svc := corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "router",
				Namespace: namespace,
				Annotations: map[string]string{
					"service.beta.kubernetes.io/aws-load-balancer-type": "nlb",
				},
				Labels: map[string]string{"app": "private-router"},
			},
			Spec: corev1.ServiceSpec{
				Type:     corev1.ServiceTypeLoadBalancer,
				Selector: map[string]string{"app": "private-router"},
				Ports: []corev1.ServicePort{
					{Name: "https", Port: 443, TargetPort: intstr.FromString("https"), Protocol: corev1.ProtocolTCP},
				},
			},
		}

		for _, m := range m {
			m(&svc)
		}
		return &svc
	}
	privateService := func(m ...func(*corev1.Service)) *corev1.Service {
		return publicService(append(m, func(s *corev1.Service) {
			s.Name = "private-router"
			s.Annotations["service.beta.kubernetes.io/aws-load-balancer-internal"] = "true"
		})...)
	}
	withCrossZoneAnnotation := func(svc *corev1.Service) {
		svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled"] = "true"
	}
	tests := []struct {
		name                         string
		endpointAccess               hyperv1.AWSEndpointAccessType
		exposeAPIServerThroughRouter bool
		existingObjects              []client.Object
		expectedServices             []corev1.Service
	}{
		{
			name:                         "Public HCP gets public LB only",
			endpointAccess:               hyperv1.Public,
			exposeAPIServerThroughRouter: true,
			expectedServices: []corev1.Service{
				*publicService(),
			},
		},
		{
			name:                         "PublicPrivate gets public and private LB",
			endpointAccess:               hyperv1.PublicAndPrivate,
			exposeAPIServerThroughRouter: true,
			expectedServices: []corev1.Service{
				*privateService(withCrossZoneAnnotation),
				*publicService(withCrossZoneAnnotation),
			},
		},
		{
			name:                         "Private gets private LB only",
			endpointAccess:               hyperv1.Private,
			exposeAPIServerThroughRouter: true,
			expectedServices: []corev1.Service{
				*privateService(withCrossZoneAnnotation),
			},
		},
		{
			name:                         "Public LB gets removed when switching to Private",
			endpointAccess:               hyperv1.Private,
			exposeAPIServerThroughRouter: true,
			existingObjects:              []client.Object{publicService(), privateService()},
			expectedServices: []corev1.Service{
				*privateService(withCrossZoneAnnotation),
			},
		},
		{
			name:                         "No LB created when public and not using Route",
			endpointAccess:               hyperv1.Public,
			exposeAPIServerThroughRouter: false,
			expectedServices:             nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: tc.endpointAccess,
						},
					},
				},
			}
			if tc.exposeAPIServerThroughRouter {
				hcp.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
					{
						Service: hyperv1.APIServer,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.Route,
							Route: &hyperv1.RoutePublishingStrategy{
								Hostname: "apiserver.example.com",
							},
						},
					},
				}
			}

			ctx := ctrl.LoggerInto(context.Background(), zapr.NewLogger(zaptest.NewLogger(t)))
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(append(tc.existingObjects, hcp)...).Build()

			r := HostedControlPlaneReconciler{
				Client: c,
				Log:    ctrl.LoggerFrom(ctx),
			}

			if err := r.reconcileHCPRouterServices(ctx, hcp, controllerutil.CreateOrUpdate); err != nil {
				t.Fatalf("reconcileRouter failed: %v", err)
			}

			var services corev1.ServiceList
			if err := c.List(ctx, &services); err != nil {
				t.Fatalf("failed to list services: %v", err)
			}
			if diff := testutil.MarshalYamlAndDiff(&services, &corev1.ServiceList{Items: tc.expectedServices}, t); diff != "" {
				t.Errorf("actual services differ from expected: %s", diff)
			}
		})
	}
}

func TestSetKASCustomKubeconfigStatus(t *testing.T) {
	hcp := sampleHCP(t)
	pullSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "pull-secret"}}
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcp, pullSecret).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
	ctx := ctrl.LoggerInto(context.Background(), zapr.NewLogger(zaptest.NewLogger(t)))

	tests := []struct {
		name                 string
		KubeAPIServerDNSName string
		expectedStatus       *hyperv1.KubeconfigSecretRef
	}{
		{
			name:                 "KubeAPIServerDNSName is empty",
			KubeAPIServerDNSName: "",
			expectedStatus:       nil,
		},
		{
			name:                 "KubeAPIServerDNSName has a valid value",
			KubeAPIServerDNSName: "testapi.example.com",
			expectedStatus: &hyperv1.KubeconfigSecretRef{
				Name: "custom-admin-kubeconfig",
				Key:  "kubeconfig",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			hcp.Spec.KubeAPIServerDNSName = tc.KubeAPIServerDNSName

			err := setKASCustomKubeconfigStatus(ctx, hcp, c)
			g.Expect(err).To(BeNil(), fmt.Errorf("error setting custom kubeconfig status failed: %v", err))
			g.Expect(hcp.Status.CustomKubeconfig).To(Equal(tc.expectedStatus))
		})
	}
}

func TestIncludeServingCertificates(t *testing.T) {
	ctx := context.Background()
	hcp := sampleHCP(t)
	rootCA := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "root-ca",
			Namespace: hcp.Namespace,
		},
		Data: map[string]string{
			"ca.crt": "root-ca-cert",
		},
	}

	tests := []struct {
		name           string
		servingCerts   *configv1.APIServerServingCerts
		servingSecrets []*corev1.Secret
		expectedCert   string
		expectError    bool
	}{
		{
			name:         "APIServer servingCerts is nil",
			servingCerts: &configv1.APIServerServingCerts{},
			expectedCert: "root-ca-cert",
		},
		{
			name: "APIServer servingCerts configuration with one named certificates",
			servingCerts: &configv1.APIServerServingCerts{
				NamedCertificates: []configv1.APIServerNamedServingCert{
					{
						ServingCertificate: configv1.SecretNameReference{
							Name: "serving-cert-1",
						},
					},
				},
			},
			servingSecrets: []*corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "serving-cert-1",
						Namespace: hcp.Namespace,
					},
					Data: map[string][]byte{
						"tls.crt": []byte("cert-1"),
					},
				},
			},
			expectedCert: "root-ca-cert\ncert-1",
		},
		{
			name: "APIServer servingCerts configuration with multiple named certificates",
			servingCerts: &configv1.APIServerServingCerts{
				NamedCertificates: []configv1.APIServerNamedServingCert{
					{
						ServingCertificate: configv1.SecretNameReference{
							Name: "serving-cert-1",
						},
					},
					{
						ServingCertificate: configv1.SecretNameReference{
							Name: "serving-cert-2",
						},
					},
				},
			},
			servingSecrets: []*corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "serving-cert-1",
						Namespace: hcp.Namespace,
					},
					Data: map[string][]byte{
						"tls.crt": []byte("cert-1"),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "serving-cert-2",
						Namespace: hcp.Namespace,
					},
					Data: map[string][]byte{
						"tls.crt": []byte("cert-2"),
					},
				},
			},
			expectedCert: "root-ca-cert\ncert-1\ncert-2",
		},
		{
			name: "APIServer servingCerts configuration with missing named certificate",
			servingCerts: &configv1.APIServerServingCerts{
				NamedCertificates: []configv1.APIServerNamedServingCert{
					{
						ServingCertificate: configv1.SecretNameReference{
							Name: "missing-cert",
						},
					},
				},
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{
				APIServer: &configv1.APIServerSpec{
					ServingCerts: *tc.servingCerts,
				},
			}

			fakeClient := fake.NewClientBuilder().WithObjects(rootCA).Build()
			for _, secret := range tc.servingSecrets {
				_ = fakeClient.Create(ctx, secret)
			}

			newRootCA, err := includeServingCertificates(ctx, fakeClient, hcp, rootCA)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(newRootCA.Data["ca.crt"]).To(Equal(tc.expectedCert))
			}
		})
	}
}

type fakeMessageCollector struct {
	msg string
}

func (c *fakeMessageCollector) ErrorMessages(resource client.Object) ([]string, error) {
	return []string{c.msg}, nil
}

func TestReconcileRouterServiceStatus(t *testing.T) {
	const namespace = "test-ns"
	const svcName = "test"
	tests := []struct {
		name         string
		svc          *corev1.Service
		expectedHost string
		expectMsg    bool
	}{
		{
			name: "Non-existent service",
		},
		{
			name: "Service that has not been provisioned",
			svc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: namespace},
			},
			expectMsg: true,
		},
		{
			name: "Service with host populated",
			svc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: namespace},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{
								Hostname: "test.host",
							},
						},
					},
				},
			},
			expectedHost: "test.host",
		},
		{
			name: "Service with IP populated",
			svc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: namespace},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{
								IP: "1.2.3.4",
							},
						},
					},
				},
			},
			expectedHost: "1.2.3.4",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := ctrl.LoggerInto(context.Background(), zapr.NewLogger(zaptest.NewLogger(t)))
			existing := []client.Object{}
			if tc.svc != nil {
				existing = append(existing, tc.svc)
			}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(existing...).Build()

			r := HostedControlPlaneReconciler{
				Client: c,
				Log:    ctrl.LoggerFrom(ctx),
			}
			msgCollector := &fakeMessageCollector{msg: "test message"}
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: namespace},
			}
			host, needed, msg, err := r.reconcileRouterServiceStatus(ctx, svc, msgCollector)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !needed {
				t.Fatalf("unexpected, needed == false")
			}
			if host != tc.expectedHost {
				t.Errorf("unexpected host, actual: %s, expected: %s", host, tc.expectedHost)
			}
			if tc.expectMsg {
				if msg == "" {
					t.Errorf("did not get an event message")
				}
			} else {
				if len(msg) > 0 {
					t.Errorf("got unexpected event message")
				}
			}
		})
	}
}

// TestControlPlaneComponents is a generic test which generates a fixture for each registered component's deployment/statefulset.
// This is helpful to allow to inspect the final manifest yaml result after all the pre/post-processing is applied.
func TestControlPlaneComponents(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockedProviderWithOpenshiftImageRegistryOverrides := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		Lookup(gomock.Any(), gomock.Any(), gomock.Any()).Return(testutils.InitReleaseImageOrDie("4.15.0"), nil).AnyTimes()
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		GetRegistryOverrides().Return(nil).AnyTimes()
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		GetOpenShiftImageRegistryOverrides().Return(nil).AnyTimes()
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().GetMirroredReleaseImage().Return("").AnyTimes()

	reconciler := &HostedControlPlaneReconciler{
		ReleaseProvider:               mockedProviderWithOpenshiftImageRegistryOverrides,
		ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
	}
	reconciler.registerComponents()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "hcp-namespace",
			Labels: map[string]string{
				"cluster.x-k8s.io/cluster-name": "cluster_name",
			},
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Configuration: &hyperv1.ClusterConfiguration{
				FeatureGate: &configv1.FeatureGateSpec{},
			},
			Services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service: hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type: hyperv1.Route,
					},
				},
			},
			Networking: hyperv1.ClusterNetworking{
				ClusterNetwork: []hyperv1.ClusterNetworkEntry{
					{
						CIDR: *ipnet.MustParseCIDR("10.132.0.0/14"),
					},
				},
			},
			Etcd: hyperv1.EtcdSpec{
				ManagementType: hyperv1.Managed,
			},
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS:  &hyperv1.AWSPlatformSpec{},
				Azure: &hyperv1.AzurePlatformSpec{
					SubnetID:        "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/myVnetName/subnets/mySubnetName",
					SecurityGroupID: "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/networkSecurityGroups/myNSGName",
					VnetID:          "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/myVnetName",
				},
				OpenStack: &hyperv1.OpenStackPlatformSpec{
					IdentityRef: hyperv1.OpenStackIdentityReference{
						Name: "fake-cloud-credentials-secret",
					},
				},
				PowerVS: &hyperv1.PowerVSPlatformSpec{
					VPC: &hyperv1.PowerVSVPC{},
				},
			},
			ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.16.10-x86_64",
		},
	}

	cpContext := controlplanecomponent.ControlPlaneContext{
		Context:                  context.Background(),
		ReleaseImageProvider:     testutil.FakeImageProvider(),
		UserReleaseImageProvider: testutil.FakeImageProvider(),
		ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
			Result: &dockerv1client.DockerImageConfig{
				Config: &docker10.DockerConfig{
					Labels: map[string]string{
						"io.openshift.release": "4.16.10",
					},
				},
			},
			Manifest: fakeimagemetadataprovider.FakeManifest{},
		},
		HCP:                    hcp,
		SkipPredicate:          true,
		SkipCertificateSigning: true,
	}
	for _, featureSet := range []configv1.FeatureSet{configv1.Default, configv1.TechPreviewNoUpgrade} {
		cpContext.HCP.Spec.Configuration.FeatureGate.FeatureGateSelection.FeatureSet = featureSet
		// This needs to be defined here, to avoid loopDetector reporting a no-op update, as changing the featureset will actually cause an update.
		cpContext.ApplyProvider = upsert.NewApplyProvider(true)

		for _, component := range reconciler.components {
			fakeObjects, err := componentsFakeObjects(hcp.Namespace)
			if err != nil {
				t.Fatalf("failed to generate fake objects: %v", err)
			}
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).
				WithObjects(fakeObjects...).
				WithObjects(componentsFakeDependencies(component.Name(), hcp.Namespace)...).
				Build()
			cpContext.Client = fakeClient

			// Reconcile multiple times to make sure multiple runs don't produce different results,
			// and to check if resources are making a no-op update calls.
			for range 2 {
				if err := component.Reconcile(cpContext); err != nil {
					t.Fatalf("failed to reconcile component %s: %v", component.Name(), err)
				}
			}

			var deployments appsv1.DeploymentList
			if err := fakeClient.List(context.Background(), &deployments); err != nil {
				t.Fatalf("failed to list deployments: %v", err)
			}

			var statfulsets appsv1.StatefulSetList
			if err := fakeClient.List(context.Background(), &statfulsets); err != nil {
				t.Fatalf("failed to list statfulsets: %v", err)
			}

			var cronJobs batchv1.CronJobList
			if err := fakeClient.List(context.Background(), &cronJobs); err != nil {
				t.Fatalf("failed to list cronJobs: %v", err)
			}

			var jobs batchv1.JobList
			if err := fakeClient.List(context.Background(), &jobs); err != nil {
				t.Fatalf("failed to list jobs: %v", err)
			}

			if len(deployments.Items) == 0 && len(statfulsets.Items) == 0 && len(cronJobs.Items) == 0 && len(jobs.Items) == 0 {
				t.Fatalf("expected one of deployment, statefulSet, cronJob or job to exist for component %s", component.Name())
			}

			var workload client.Object
			if len(deployments.Items) > 0 {
				workload = &deployments.Items[0]
			} else if len(statfulsets.Items) > 0 {
				workload = &statfulsets.Items[0]
			} else if len(cronJobs.Items) > 0 {
				workload = &cronJobs.Items[0]
			} else {
				workload = &jobs.Items[0]
			}

			yaml, err := util.SerializeResource(workload, api.Scheme)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var suffix = ""
			if featureSet != configv1.Default {
				suffix = fmt.Sprintf("_%s", featureSet)
			}
			testutil.CompareWithFixture(t, yaml, testutil.WithSubDir(component.Name()), testutil.WithSuffix(suffix))

			controlPaneComponent := &hyperv1.ControlPlaneComponent{
				ObjectMeta: metav1.ObjectMeta{
					Name:      component.Name(),
					Namespace: hcp.Namespace,
				},
			}
			if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(controlPaneComponent), controlPaneComponent); err != nil {
				t.Fatalf("expected ControlPlaneComponent to exist for component %s: %v", component.Name(), err)
			}

			// this is needed to ensure the fixtures match, otherwise LastTransitionTime will have a different value for each execution.
			for i := range controlPaneComponent.Status.Conditions {
				controlPaneComponent.Status.Conditions[i].LastTransitionTime = metav1.Time{}
			}

			yaml, err = util.SerializeResource(controlPaneComponent, api.Scheme)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			suffix = "_component"
			if featureSet != configv1.Default {
				suffix = fmt.Sprintf("_component_%s", featureSet)
			}
			testutil.CompareWithFixture(t, yaml, testutil.WithSubDir(component.Name()), testutil.WithSuffix(suffix))
		}

		if err := cpContext.ApplyProvider.ValidateUpdateEvents(1); err != nil {
			t.Fatalf("update loop detected: %v", err)
		}
	}

}

func TestAWSSecurityGroupTags(t *testing.T) {
	tests := []struct {
		name         string
		hcp          *hyperv1.HostedControlPlane
		expectedTags map[string]string
	}{
		{
			name: "No additional tags, no AutoNode",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{},
						},
					},
				},
			},
			expectedTags: map[string]string{
				"kubernetes.io/cluster/test-infra": "owned",
				"Name":                             "test-infra-default-sg",
			},
		},
		{
			name: "Additional tags override Name and cluster key",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "myinfra",
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "Name", Value: "custom-name"},
								{Key: "kubernetes.io/cluster/myinfra", Value: "shared"},
								{Key: "foo", Value: "bar"},
							},
						},
					},
				},
			},
			expectedTags: map[string]string{
				"Name":                          "custom-name",
				"kubernetes.io/cluster/myinfra": "shared",
				"foo":                           "bar",
			},
		},
		{
			name: "AutoNode with Karpenter AWS adds karpenter.sh/discovery",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "karpenter-infra",
					Platform: hyperv1.PlatformSpec{
						AWS: &hyperv1.AWSPlatformSpec{},
					},
					AutoNode: &hyperv1.AutoNode{
						Provisioner: &hyperv1.ProvisionerConfig{
							Name: hyperv1.ProvisionerKarpeneter,
							Karpenter: &hyperv1.KarpenterConfig{
								Platform: hyperv1.AWSPlatform,
							},
						},
					},
				},
			},
			expectedTags: map[string]string{
				"kubernetes.io/cluster/karpenter-infra": "owned",
				"Name":                                  "karpenter-infra-default-sg",
				"karpenter.sh/discovery":                "karpenter-infra",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := awsSecurityGroupTags(tc.hcp)
			if len(got) != len(tc.expectedTags) {
				t.Errorf("expected %d tags, got %d: %v", len(tc.expectedTags), len(got), got)
			}
			for k, v := range tc.expectedTags {
				if got[k] != v {
					t.Errorf("expected tag %q=%q, got %q", k, v, got[k])
				}
			}
		})
	}
}

//go:embed testdata/featuregate-generator/feature-gate.yaml
var testFeatureGateYAML string

func componentsFakeObjects(namespace string) ([]client.Object, error) {
	rootCA := manifests.RootCASecret(namespace)
	rootCA.Data = map[string][]byte{
		certs.CASignerCertMapKey: []byte("fake"),
	}
	authenticatorCertSecret := manifests.OpenshiftAuthenticatorCertSecret(namespace)
	authenticatorCertSecret.Data = map[string][]byte{
		corev1.TLSCertKey:       []byte("fake"),
		corev1.TLSPrivateKeyKey: []byte("fake"),
	}
	bootsrapCertSecret := manifests.KASMachineBootstrapClientCertSecret(namespace)
	bootsrapCertSecret.Data = map[string][]byte{
		corev1.TLSCertKey:       []byte("fake"),
		corev1.TLSPrivateKeyKey: []byte("fake"),
	}
	adminCertSecert := manifests.SystemAdminClientCertSecret(namespace)
	adminCertSecert.Data = map[string][]byte{
		corev1.TLSCertKey:       []byte("fake"),
		corev1.TLSPrivateKeyKey: []byte("fake"),
	}
	hccoCertSecert := manifests.HCCOClientCertSecret(namespace)
	hccoCertSecert.Data = map[string][]byte{
		corev1.TLSCertKey:       []byte("fake"),
		corev1.TLSPrivateKeyKey: []byte("fake"),
	}

	azureCredentialsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-credential-information",
			Namespace: "hcp-namespace",
		},
	}

	cloudCredsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fake-cloud-credentials-secret",
			Namespace: namespace,
		},
	}

	csrSigner := manifests.CSRSignerCASecret(namespace)
	csrSigner.Data = map[string][]byte{
		corev1.TLSCertKey:       []byte("fake"),
		corev1.TLSPrivateKeyKey: []byte("fake"),
	}
	fgConfigMap := &corev1.ConfigMap{}
	fgConfigMap.Name = "feature-gate"
	fgConfigMap.Namespace = namespace
	fgConfigMap.Data = map[string]string{"feature-gate.yaml": testFeatureGateYAML}

	return []client.Object{
		rootCA, authenticatorCertSecret, bootsrapCertSecret, adminCertSecert, hccoCertSecert,
		manifests.KubeControllerManagerClientCertSecret(namespace),
		manifests.KubeSchedulerClientCertSecret(namespace),
		azureCredentialsSecret,
		cloudCredsSecret,
		csrSigner,
		fgConfigMap,
	}, nil
}

func componentsFakeDependencies(componentName string, namespace string) []client.Object {
	var fakeComponents []client.Object

	// we need this to exist for components to reconcile
	fakeComponentTemplate := &hyperv1.ControlPlaneComponent{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
		},
		Status: hyperv1.ControlPlaneComponentStatus{
			Version: testutil.FakeImageProvider().Version(),
			Conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.ControlPlaneComponentAvailable),
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	// all components depend on KAS and KAS depends on etcd
	if componentName == kasv2.ComponentName {
		fakeComponentTemplate.Name = etcdv2.ComponentName
		fakeComponents = append(fakeComponents, fakeComponentTemplate.DeepCopy())
		fakeComponentTemplate.Name = fg.ComponentName
		fakeComponents = append(fakeComponents, fakeComponentTemplate.DeepCopy())
	} else {
		fakeComponentTemplate.Name = kasv2.ComponentName
		fakeComponents = append(fakeComponents, fakeComponentTemplate.DeepCopy())
	}

	if componentName != oapiv2.ComponentName {
		fakeComponentTemplate.Name = oapiv2.ComponentName
		fakeComponents = append(fakeComponents, fakeComponentTemplate.DeepCopy())
	}

	if componentName == ignitionproxyv2.ComponentName {
		fakeComponentTemplate.Name = ignitionserverv2.ComponentName
		fakeComponents = append(fakeComponents, fakeComponentTemplate.DeepCopy())
	}

	pullSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: "hcp-namespace"},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{}`),
		},
	}

	fakeComponents = append(fakeComponents, pullSecret.DeepCopy())

	return fakeComponents
}

type fakeReleaseProvider struct{}

func (*fakeReleaseProvider) GetImage(key string) string {
	return key
}

func (*fakeReleaseProvider) ImageExist(key string) (string, bool) {
	return key, true
}

func (*fakeReleaseProvider) Version() string {
	return "1.0.0"
}

func (*fakeReleaseProvider) ComponentVersions() (map[string]string, error) {
	return map[string]string{}, nil
}

func (*fakeReleaseProvider) ComponentImages() map[string]string {
	return map[string]string{}
}

func TestReconcileFeatureGateGenerationJob(t *testing.T) {

	expectedFGYaml := `apiVersion: config.openshift.io/v1
kind: FeatureGate
metadata:
  creationTimestamp: null
  name: cluster
spec: {}
status:
  featureGates: null
`
	expectedPayloadVersion := "1.0.0"

	failedJob := func() *batchv1.Job {
		job := manifests.FeatureGateGenerationJob("test")
		job.Status.Conditions = []batchv1.JobCondition{
			{
				Type:   batchv1.JobFailed,
				Status: corev1.ConditionTrue,
			},
		}
		return job
	}

	completedJob := func(payloadVersion, fgYAML string) *batchv1.Job {
		job := manifests.FeatureGateGenerationJob("test")
		job.Spec.Template.Spec.InitContainers = []corev1.Container{
			{
				Env: []corev1.EnvVar{
					{
						Name:  "PAYLOAD_VERSION",
						Value: payloadVersion,
					},
					{
						Name:  "FEATURE_GATE_YAML",
						Value: fgYAML,
					},
				},
			},
		}
		job.Status.Conditions = []batchv1.JobCondition{
			{
				Type:   batchv1.JobComplete,
				Status: corev1.ConditionTrue,
			},
		}
		return job
	}

	validConfigMap := func() *corev1.ConfigMap {
		cm := &corev1.ConfigMap{}
		cm.Name = "feature-gate"
		cm.Namespace = "test"

		cm.Data = map[string]string{"feature-gate.yaml": expectedFGYaml}
		return cm
	}

	invalidConfigMap := func() *corev1.ConfigMap {
		cm := &corev1.ConfigMap{}
		cm.Name = "feature-gate"
		cm.Namespace = "test"

		cm.Data = map[string]string{"feature-gate.yaml": "invalid-yaml"}
		return cm
	}

	runningJob := func() *batchv1.Job {
		job := manifests.FeatureGateGenerationJob("test")
		return job
	}

	tests := []struct {
		name            string
		job             *batchv1.Job
		cm              *corev1.ConfigMap
		expectedProceed bool
		expectedError   bool
		expectJobExists bool
	}{
		{
			name:            "initial creation",
			expectedProceed: false,
			expectJobExists: true,
		},
		{
			name:            "job failed",
			job:             failedJob(),
			expectedProceed: false,
			expectJobExists: false,
		},
		{
			name:            "running job",
			job:             runningJob(),
			expectedProceed: false,
			expectJobExists: true,
		},
		{
			name:            "completed job, valid configmap",
			job:             completedJob(expectedPayloadVersion, expectedFGYaml),
			cm:              validConfigMap(),
			expectJobExists: true,
			expectedProceed: true,
		},
		{
			name:            "completed job, valid configmap, different payload version",
			job:             completedJob("1.0.0-alpha", expectedFGYaml),
			cm:              validConfigMap(),
			expectJobExists: false,
			expectedProceed: false,
		},
		{
			name:            "completed job, valid configmap, different featurgateYAML",
			job:             completedJob(expectedPayloadVersion, "invalid-yaml"),
			cm:              validConfigMap(),
			expectJobExists: false,
			expectedProceed: false,
		},
		{
			name:            "completed job, missing configmap",
			job:             completedJob(expectedPayloadVersion, expectedFGYaml),
			expectJobExists: false,
			expectedProceed: false,
		},
		{
			name:            "completed job, invalid configmap",
			job:             completedJob(expectedPayloadVersion, expectedFGYaml),
			cm:              invalidConfigMap(),
			expectJobExists: false,
			expectedProceed: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
				},
			}
			clientBuilder := fake.NewClientBuilder()
			if tc.job != nil {
				clientBuilder = clientBuilder.WithObjects(tc.job)
			}
			if tc.cm != nil {
				clientBuilder = clientBuilder.WithObjects(tc.cm)
			}
			fakeClient := clientBuilder.Build()
			r := &HostedControlPlaneReconciler{
				Client:              fakeClient,
				Log:                 ctrl.LoggerFrom(context.TODO()),
				ReleaseProvider:     &fakereleaseprovider.FakeReleaseProvider{},
				UserReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{},
			}
			proceed, err := r.reconcileFeatureGateGenerationJob(context.Background(), hcp, controllerutil.CreateOrUpdate, &fakeReleaseProvider{}, &fakeReleaseProvider{})
			if tc.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(proceed).To(Equal(tc.expectedProceed))
				job := manifests.FeatureGateGenerationJob(hcp.Namespace)
				err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(job), job)
				if !apierrors.IsNotFound(err) {
					g.Expect(err).NotTo(HaveOccurred())
				}
				if tc.expectJobExists {
					g.Expect(err).NotTo(HaveOccurred())
				} else {
					g.Expect(err).To(HaveOccurred())
				}
			}
		})
	}
}
