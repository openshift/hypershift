package hostedcontrolplane

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/upsert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileKubeadminPassword(t *testing.T) {
	targetNamespace := "test"
	OAuthConfig := `
apiVersion: config.openshift.io/v1
kind: OAuth
metadata:
  name: "example"
spec:
  identityProviders:
  - openID:
      claims:
        email:
        - email
        name:
        - clientid1-secret-name
        preferredUsername:
        - preferred_username
      clientID: clientid1
      clientSecret:
        name: clientid1-secret-name
      issuer: https://example.com/identity
    mappingMethod: lookup
    name: IAM
    type: OpenID
`

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
						Items: []runtime.RawExtension{
							{
								Raw: []byte(OAuthConfig),
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
				Client:                 fakeClient,
				Log:                    ctrl.LoggerFrom(context.TODO()),
				CreateOrUpdateProvider: upsert.New(false),
			}

			globalConfig, err := globalconfig.ParseGlobalConfig(context.Background(), tc.hcp.Spec.Configuration)
			g.Expect(err).NotTo(HaveOccurred())

			err = r.reconcileKubeadminPassword(context.Background(), tc.hcp, globalConfig.OAuth != nil)
			g.Expect(err).NotTo(HaveOccurred())

			actualSecret := common.KubeadminPasswordSecret(targetNamespace)
			err = fakeClient.Get(context.Background(), client.ObjectKeyFromObject(actualSecret), actualSecret)
			if tc.expectedOutputSecret != nil {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(actualSecret.Data).To(HaveKey("password"))
				g.Expect(actualSecret.Data["password"]).ToNot(BeEmpty())
			} else {
				if !errors.IsNotFound(err) {
					g.Expect(err).NotTo(HaveOccurred())
				}
			}
		})
	}
}

func TestReconcileAPIServerService(t *testing.T) {
	targetNamespace := "test"
	apiPort := int32(1234)
	hostname := "test.example.com"
	allowCIDR := []hyperv1.CIDRBlock{"1.2.3.4/24"}
	allowCIDRString := []string{"1.2.3.4/24"}
	testsCases := []struct {
		name             string
		hcp              *hyperv1.HostedControlPlane
		expectedServices []*corev1.Service
	}{
		{
			name: "EndpointAccess PublicAndPrivate, ServicePublishingStrategy LoadBalancer, hostname, custom port, and allowed CIDR blocks",
			hcp: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: targetNamespace,
					Name:      "test",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					APIPort:              &apiPort,
					APIAllowedCIDRBlocks: allowCIDR,
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: hyperv1.PublicAndPrivate,
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
								LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
									Hostname: hostname,
								},
							},
						},
					},
				},
			},
			expectedServices: []*corev1.Service{
				{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: targetNamespace,
						Name:      manifests.KubeAPIServerService(targetNamespace).Name,
						Annotations: map[string]string{
							"service.beta.kubernetes.io/aws-load-balancer-type": "nlb",
							hyperv1.ExternalDNSHostnameAnnotation:               hostname,
						},
					},
					Spec: corev1.ServiceSpec{
						Type: corev1.ServiceTypeLoadBalancer,
						Ports: []corev1.ServicePort{
							{
								Protocol:   corev1.ProtocolTCP,
								Port:       apiPort,
								TargetPort: intstr.FromInt(int(apiPort)),
							},
						},
						LoadBalancerSourceRanges: allowCIDRString,
					},
				},
				{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Namespace: targetNamespace,
						Name:      manifests.KubeAPIServerPrivateService(targetNamespace).Name,
						Annotations: map[string]string{
							"service.beta.kubernetes.io/aws-load-balancer-type":     "nlb",
							"service.beta.kubernetes.io/aws-load-balancer-internal": "true",
						},
					},
					Spec: corev1.ServiceSpec{
						Type: corev1.ServiceTypeLoadBalancer,
						Ports: []corev1.ServicePort{
							{
								Protocol:   corev1.ProtocolTCP,
								Port:       6443,
								TargetPort: intstr.FromInt(6443),
							},
						},
					},
				},
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			fakeClient := fake.NewClientBuilder().Build()
			r := &HostedControlPlaneReconciler{
				Client:                 fakeClient,
				Log:                    ctrl.LoggerFrom(context.TODO()),
				CreateOrUpdateProvider: upsert.New(false),
			}

			err := r.reconcileAPIServerService(context.Background(), tc.hcp)
			g.Expect(err).NotTo(HaveOccurred())
			var actualService corev1.Service
			for _, expectedService := range tc.expectedServices {
				err = r.Get(context.Background(), client.ObjectKeyFromObject(expectedService), &actualService)
				g.Expect(err).NotTo(HaveOccurred())
				actualService.Spec.Selector = nil
				g.Expect(actualService.Spec).To(Equal(expectedService.Spec))
				g.Expect(actualService.Annotations).To(Equal(expectedService.Annotations))
			}
		})
	}
}
