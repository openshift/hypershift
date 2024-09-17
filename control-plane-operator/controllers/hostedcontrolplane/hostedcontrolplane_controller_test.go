package hostedcontrolplane

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/go-logr/zapr"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/autoscaler"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ingress"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	etcdv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/etcd"
	kasv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	"github.com/openshift/hypershift/support/api"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/releaseinfo"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	"go.uber.org/zap/zaptest"
	appsv1 "k8s.io/api/apps/v1"
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

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/oauth"
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
	for _, v := range autoscaler.GetIgnoreLabels() {
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

	r := &HostedControlPlaneReconciler{
		Client:                        c,
		ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		ReleaseProvider:               &fakereleaseprovider.FakeReleaseProvider{},
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
	workqueue.RateLimitingInterface
}

func (c *createTrackingWorkqueue) Add(item interface{}) {
	c.items = append(c.items, item.(reconcile.Request))
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
		t.Errorf("reconciliation failed: %v", err)
	}

	testCases := []struct {
		name                         string
		endpointAccess               hyperv1.AWSEndpointAccessType
		exposeAPIServerThroughRouter bool
		existingObjects              []client.Object
		expectedDeployments          []appsv1.Deployment
	}{
		{
			name:                         "Public HCP, uses public service host name",
			endpointAccess:               hyperv1.Public,
			exposeAPIServerThroughRouter: true,
			existingObjects:              []client.Object{},
			expectedDeployments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					err := ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.HCPRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						routerCfg,
					)
					if err != nil {
						return appsv1.Deployment{}
					}

					return *dep
				}(),
			},
		},
		{
			name:                         "PublicPrivate HCP, deployment gets hostname from public service",
			endpointAccess:               hyperv1.PublicAndPrivate,
			exposeAPIServerThroughRouter: true,
			existingObjects:              []client.Object{},
			expectedDeployments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					err := ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.HCPRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						routerCfg,
					)
					if err != nil {
						return appsv1.Deployment{}
					}

					return *dep
				}(),
			},
		},

		{
			name:                         "Private HCP, deployment gets hostname from private service",
			endpointAccess:               hyperv1.Private,
			exposeAPIServerThroughRouter: true,
			existingObjects:              []client.Object{},
			expectedDeployments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					err := ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.HCPRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						routerCfg,
					)
					if err != nil {
						return appsv1.Deployment{}
					}

					return *dep
				}(),
			},
		},
		{
			name:                         "Public HCP apiserver not exposed through router, nothing gets created",
			endpointAccess:               hyperv1.Public,
			exposeAPIServerThroughRouter: false,
		},
		{
			name:                         "PublicPrivate HCP apiserver not exposed through router, router without custom template and private router service get created",
			endpointAccess:               hyperv1.PublicAndPrivate,
			exposeAPIServerThroughRouter: false,
			expectedDeployments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					err := ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.HCPRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						routerCfg,
					)
					if err != nil {
						return appsv1.Deployment{}
					}

					return *dep
				}(),
			},
		},
		{
			name:                         "Private HCP apiserver not exposed through router, router without custom template and private router service get created",
			endpointAccess:               hyperv1.Private,
			exposeAPIServerThroughRouter: false,
			expectedDeployments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					err := ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.HCPRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						routerCfg,
					)
					if err != nil {
						return appsv1.Deployment{}
					}

					return *dep
				}(),
			},
		},
		{
			name:                         "Old router resources get cleaned up when exposed through route",
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
					err := ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.HCPRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						routerCfg,
					)
					if err != nil {
						return appsv1.Deployment{}
					}

					return *dep
				}(),
			},
		},
		{
			name:                         "Old router resources get cleaned up when exposed through LB",
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
					err := ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.HCPRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						routerCfg,
					)
					if err != nil {
						return appsv1.Deployment{}
					}

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
						Type: hyperv1.AWSPlatform,
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
	hcp := sampleHCP(t)
	pullSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "pull-secret"}}
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcp, pullSecret).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
	r := &HostedControlPlaneReconciler{
		Client:                        c,
		ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		ReleaseProvider:               &fakereleaseprovider.FakeReleaseProvider{},
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
	reconciler := &HostedControlPlaneReconciler{
		ReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{},
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
				AWS: &hyperv1.AWSPlatformSpec{},
				Azure: &hyperv1.AzurePlatformSpec{
					SubnetID:        "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/myVnetName/subnets/mySubnetName",
					SecurityGroupID: "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/networkSecurityGroups/myNSGName",
					VnetID:          "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/myVnetName",
					Credentials: corev1.LocalObjectReference{
						Name: "fake-cloud-credentials-secret",
					},
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
		},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-credential-information",
			Namespace: "hcp-namespace",
		},
	}

	cpContext := controlplanecomponent.ControlPlaneContext{
		Context:                  context.Background(),
		CreateOrUpdateProviderV2: upsert.NewV2(false),
		ReleaseImageProvider:     testutil.FakeImageProvider(),
		UserReleaseImageProvider: testutil.FakeImageProvider(),
		HCP:                      hcp,
		SkipPredicate:            true,
	}
	for _, featureSet := range []configv1.FeatureSet{configv1.Default, configv1.TechPreviewNoUpgrade} {
		cpContext.HCP.Spec.Configuration.FeatureGate.FeatureGateSelection.FeatureSet = featureSet

		for _, component := range reconciler.components {
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).
				WithObjects(componentsFakeObjects(hcp.Namespace)...).
				WithObjects(componentsFakeDependencies(component.Name(), hcp.Namespace)...).
				WithObjects(secret).
				Build()
			cpContext.Client = fakeClient

			// Reconcile multiple times to make sure multiple runs don't produce different results.
			for i := 0; i < 2; i++ {
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

			if len(deployments.Items) == 0 && len(statfulsets.Items) == 0 {
				t.Fatalf("expected one of deployment or statefulSet to exist for component %s", component.Name())
			}

			var workload client.Object
			if len(deployments.Items) > 0 {
				workload = &deployments.Items[0]
			} else {
				workload = &statfulsets.Items[0]
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
	}
}

func componentsFakeObjects(namespace string) []client.Object {
	rootCA := manifests.RootCAConfigMap(namespace)
	rootCA.Data = map[string]string{
		certs.CASignerCertMapKey: "fake",
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

	cloudCredsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fake-cloud-credentials-secret",
			Namespace: namespace,
		},
	}

	return []client.Object{
		rootCA, authenticatorCertSecret, bootsrapCertSecret, adminCertSecert, hccoCertSecert,
		manifests.KubeControllerManagerClientCertSecret(namespace),
		manifests.KubeSchedulerClientCertSecret(namespace),
		cloudCredsSecret,
	}
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

	// all components depend on KAS and KAS depends on etcd.
	if componentName == kasv2.ComponentName {
		fakeComponentTemplate.Name = etcdv2.ComponentName
	} else {
		fakeComponentTemplate.Name = kasv2.ComponentName
	}
	fakeComponents = append(fakeComponents, fakeComponentTemplate.DeepCopy())

	if componentName != oapiv2.ComponentName {
		fakeComponentTemplate.Name = oapiv2.ComponentName
		fakeComponents = append(fakeComponents, fakeComponentTemplate.DeepCopy())
	}

	return fakeComponents
}
