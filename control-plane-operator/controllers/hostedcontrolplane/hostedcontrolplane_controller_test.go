package hostedcontrolplane

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/zapr"
	. "github.com/onsi/gomega"
	imagev1 "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	"github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/autoscaler"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/ingress"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/releaseinfo"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/testutil"
	"go.uber.org/zap/zaptest"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
	"sigs.k8s.io/yaml"
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
				Client: fakeClient,
				Log:    ctrl.LoggerFrom(context.TODO()),
			}

			globalConfig, err := globalconfig.ParseGlobalConfig(context.Background(), tc.hcp.Spec.Configuration)
			g.Expect(err).NotTo(HaveOccurred())

			err = r.reconcileKubeadminPassword(context.Background(), tc.hcp, globalConfig.OAuth != nil, controllerutil.CreateOrUpdate)
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

func TestReconcileAPIServerService(t *testing.T) {
	targetNamespace := "test"
	apiPort := int32(1234)
	hostname := "test.example.com"
	allowCIDR := []hyperv1.CIDRBlock{"1.2.3.4/24"}
	allowCIDRString := []string{"1.2.3.4/24"}

	ownerRef := metav1.OwnerReference{
		APIVersion:         "hypershift.openshift.io/v1alpha1",
		Kind:               "HostedControlPlane",
		Name:               "test",
		Controller:         pointer.Bool(true),
		BlockOwnerDeletion: pointer.Bool(true),
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
				Type: corev1.ServiceTypeLoadBalancer,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       apiPort,
						TargetPort: intstr.FromInt(6443),
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
	kasPublicRoute := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      "kube-apiserver",
			Labels: map[string]string{
				"hypershift.openshift.io/hosted-control-plane": targetNamespace,
			},
			Annotations: map[string]string{
				"external-dns.alpha.kubernetes.io/hostname": hostname,
			},
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
			},
		},
		Spec: routev1.RouteSpec{
			Host: "kubernetes.default",
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
				kasPublicService(),
				kasPrivateService(),
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
				kasPrivateService(),
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
				kasPublicRoute,
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
				kasPublicRoute,
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
				MaxNodesTotal:        pointer.Int32Ptr(100),
				MaxPodGracePeriod:    pointer.Int32Ptr(300),
				MaxNodeProvisionTime: "20m",
				PodPriorityThreshold: pointer.Int32Ptr(-5),
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
			err := autoscaler.ReconcileAutoscalerDeployment(deployment, hcp, sa, secret, test.AutoscalerOptions, "clusterAutoscalerImage", "availabilityProberImage", false)
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
					Replicas: pointer.Int32Ptr(1),
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
					Replicas: pointer.Int32Ptr(1),
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
					Replicas: pointer.Int32Ptr(3),
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
	rawHCP := `apiVersion: hypershift.openshift.io/v1alpha1
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

	hcpGVK, err := apiutil.GVKForObject(hcp, api.Scheme)
	if err != nil {
		t.Fatalf("failed to determine gvk for %T: %v", hcp, err)
	}
	restMapper := meta.NewDefaultRESTMapper(nil)
	restMapper.Add(hcpGVK, meta.RESTScopeNamespace)
	c := &createTrackingClient{Client: fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(hcp, pullSecret, etcdEncryptionKey).
		WithRESTMapper(restMapper).
		Build(),
	}

	readyInfraStatus := InfrastructureStatus{
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
		reconcileInfrastructureStatus: func(context.Context, *hyperv1.HostedControlPlane) (InfrastructureStatus, error) {
			return readyInfraStatus, nil
		},
	}
	r.setup(controllerutil.CreateOrUpdate)
	ctx := ctrl.LoggerInto(context.Background(), zapr.NewLogger(zaptest.NewLogger(t)))

	if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(hcp)}); err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}

	eventHandlerList := r.eventHandlers()
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

			if injectScheme, ok := handler.(inject.Scheme); ok {
				if err := injectScheme.InjectScheme(api.Scheme); err != nil {
					t.Fatalf("failed to inject scheme into handler: %v", err)
				}
			}
			if injectMapper, ok := handler.(inject.Mapper); ok {
				if err := injectMapper.InjectMapper(c.RESTMapper()); err != nil {
					t.Fatalf("failed to inject mapper into handler: %v", err)
				}
			}

			fakeQueue := &createTrackingWorkqueue{}
			handler.Create(event.CreateEvent{Object: createdObject}, fakeQueue)

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
					{Name: "kube-apiserver", Port: 6443, TargetPort: intstr.FromString("https"), Protocol: corev1.ProtocolTCP},
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
	testCases := []struct {
		name                         string
		endpointAccess               hyperv1.AWSEndpointAccessType
		exposeAPIServerThroughRouter bool
		existingObjects              []client.Object
		expectedServices             []corev1.Service
		expectedDeploynments         []appsv1.Deployment
	}{
		{
			name:                         "Public HCP gets public LB ony",
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
				*privateService(),
				*publicService(),
			},
		},
		{
			name:                         "Private gets private LB only",
			endpointAccess:               hyperv1.Private,
			exposeAPIServerThroughRouter: true,
			expectedServices: []corev1.Service{
				*privateService(),
			},
		},
		{
			name:                         "Public HCP, deployment is created when service has Ingress hostname set",
			endpointAccess:               hyperv1.Public,
			exposeAPIServerThroughRouter: true,
			existingObjects: []client.Object{publicService(func(s *corev1.Service) {
				s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{
					Hostname: "a27252241e22343d4a704f1ca560e4aa-9ab9cf5317a99da5.elb.ca-central-1.amazonaws.com",
				}}
			})},
			expectedServices: []corev1.Service{
				*publicService(func(s *corev1.Service) {
					s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{
						Hostname: "a27252241e22343d4a704f1ca560e4aa-9ab9cf5317a99da5.elb.ca-central-1.amazonaws.com",
					}}
				}),
			},
			expectedDeploynments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.PrivateRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						"a27252241e22343d4a704f1ca560e4aa-9ab9cf5317a99da5.elb.ca-central-1.amazonaws.com",
						true,
					)

					return *dep
				}(),
			},
		},
		{
			name:                         "PublicPrivate HCP, deployment gets hostname from public service",
			endpointAccess:               hyperv1.PublicAndPrivate,
			exposeAPIServerThroughRouter: true,
			existingObjects: []client.Object{
				publicService(func(s *corev1.Service) {
					s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{
						Hostname: "a27252241e22343d4a704f1ca560e4aa-9ab9cf5317a99da5.elb.ca-central-1.amazonaws.com",
					}}
				}),
				privateService(func(s *corev1.Service) {
					s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{
						Hostname: "private-lb",
					}}
				}),
			},
			expectedServices: []corev1.Service{
				*privateService(func(s *corev1.Service) {
					s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{
						Hostname: "private-lb",
					}}
				}),
				*publicService(func(s *corev1.Service) {
					s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{
						Hostname: "a27252241e22343d4a704f1ca560e4aa-9ab9cf5317a99da5.elb.ca-central-1.amazonaws.com",
					}}
				}),
			},
			expectedDeploynments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.PrivateRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						"a27252241e22343d4a704f1ca560e4aa-9ab9cf5317a99da5.elb.ca-central-1.amazonaws.com",
						true,
					)

					return *dep
				}(),
			},
		},
		{
			name:                         "Private HCP, deployment gets hostname from private service",
			endpointAccess:               hyperv1.Private,
			exposeAPIServerThroughRouter: true,
			existingObjects: []client.Object{
				privateService(func(s *corev1.Service) {
					s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{
						Hostname: "private-lb",
					}}
				}),
			},
			expectedServices: []corev1.Service{
				*privateService(func(s *corev1.Service) {
					s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{
						Hostname: "private-lb",
					}}
				}),
			},
			expectedDeploynments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.PrivateRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						"private-lb",
						true,
					)

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
			expectedServices: []corev1.Service{
				*privateService(),
			},
			expectedDeploynments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.PrivateRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						"private-lb",
						false,
					)

					return *dep
				}(),
			},
		},
		{
			name:                         "Private HCP apiserver not exposed through router, router without custom template and porivate router service get created",
			endpointAccess:               hyperv1.Private,
			exposeAPIServerThroughRouter: false,
			expectedServices: []corev1.Service{
				*privateService(),
			},
			expectedDeploynments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.PrivateRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						"private-lb",
						false,
					)

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
				publicService(func(s *corev1.Service) {
					s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{
						Hostname: "a27252241e22343d4a704f1ca560e4aa-9ab9cf5317a99da5.elb.ca-central-1.amazonaws.com",
					}}
				}),
				privateService(func(s *corev1.Service) {
					s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{
						Hostname: "private-lb",
					}}
				}),
			},
			expectedServices: []corev1.Service{
				*privateService(func(s *corev1.Service) {
					s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{
						Hostname: "private-lb",
					}}
				}),
				*publicService(func(s *corev1.Service) {
					s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{
						Hostname: "a27252241e22343d4a704f1ca560e4aa-9ab9cf5317a99da5.elb.ca-central-1.amazonaws.com",
					}}
				}),
			},
			expectedDeploynments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.PrivateRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						"a27252241e22343d4a704f1ca560e4aa-9ab9cf5317a99da5.elb.ca-central-1.amazonaws.com",
						true,
					)

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
			expectedServices: []corev1.Service{
				*privateService(),
			},
			expectedDeploynments: []appsv1.Deployment{
				func() appsv1.Deployment {
					dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      "router",
					}}
					ingress.ReconcileRouterDeployment(dep,
						config.OwnerRefFrom(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{
							Name:      "hcp",
							Namespace: namespace,
						}}),
						ingress.PrivateRouterConfig(&hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}, false),
						"",
						"private-lb",
						false,
					)

					return *dep
				}(),
			},
		},
	}

	for _, tc := range testCases {
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

			ctx := ctrl.LoggerInto(context.Background(), zapr.NewLogger(zaptest.NewLogger(t)))
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(append(tc.existingObjects, hcp)...).Build()

			r := HostedControlPlaneReconciler{
				Client: c,
				Log:    ctrl.LoggerFrom(ctx),
			}

			if err := r.reconcileRouter(ctx, hcp, &releaseinfo.ReleaseImage{ImageStream: &imagev1.ImageStream{}}, controllerutil.CreateOrUpdate, tc.exposeAPIServerThroughRouter); err != nil {
				t.Fatalf("reconcileRouter failed: %v", err)
			}

			var services corev1.ServiceList
			if err := c.List(ctx, &services); err != nil {
				t.Fatalf("failed to list services: %v", err)
			}
			if diff := testutil.MarshalYamlAndDiff(&services, &corev1.ServiceList{Items: tc.expectedServices}, t); diff != "" {
				t.Errorf("actual services differ from expected: %s", diff)
			}

			var deployments appsv1.DeploymentList
			if err := c.List(ctx, &deployments); err != nil {
				t.Fatalf("failed to list deployments: %v", err)
			}
			if diff := testutil.MarshalYamlAndDiff(&deployments, &appsv1.DeploymentList{Items: tc.expectedDeploynments}, t); diff != "" {
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
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcp, pullSecret).Build()
	r := &HostedControlPlaneReconciler{
		Client:                        c,
		ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		ReleaseProvider:               &fakereleaseprovider.FakeReleaseProvider{},
		reconcileInfrastructureStatus: func(context.Context, *hyperv1.HostedControlPlane) (InfrastructureStatus, error) {
			return InfrastructureStatus{}, nil
		},
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
