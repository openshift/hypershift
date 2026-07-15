package hostedcontrolplane

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	endpointresolverv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/endpoint_resolver"
	etcdv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/etcd"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/fg"
	ignitionserverv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/ignitionserver"
	ignitionproxyv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/ignitionserver_proxy"
	kasv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	metricsproxyv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/metrics_proxy"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	routerv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/router"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/awsapi"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/capabilities"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	"github.com/openshift/hypershift/support/certs"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/netutil"
	"github.com/openshift/hypershift/support/releaseinfo"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/reference"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/image/docker10"
	routev1 "github.com/openshift/api/route/v1"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/clock"
	testingclock "k8s.io/utils/clock/testing"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	"github.com/docker/distribution"
	"github.com/go-logr/zapr"
	"github.com/opencontainers/go-digest"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap/zaptest"
)

func TestReconcileKubeadminPassword(t *testing.T) {
	t.Parallel()
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
				Log:    ctrl.LoggerFrom(t.Context()),
			}
			err := r.reconcileKubeadminPassword(t.Context(), tc.hcp, tc.hcp.Spec.Configuration != nil && tc.hcp.Spec.Configuration.OAuth != nil, controllerutil.CreateOrUpdate)
			g.Expect(err).NotTo(HaveOccurred())

			actualSecret := common.KubeadminPasswordSecret(targetNamespace)
			err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(actualSecret), actualSecret)
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

func TestReconcileIgnitionServer(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockedProviderWithOpenshiftImageRegistryOverrides := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		Lookup(gomock.Any(), gomock.Any(), gomock.Any()).Return(testutils.InitReleaseImageOrDie("4.20.0"), nil).AnyTimes()
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		GetRegistryOverrides().Return(map[string]string{"registry": "override"}).AnyTimes()
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		GetOpenShiftImageRegistryOverrides().Return(map[string][]string{"registry": {"override"}}).AnyTimes()
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().GetMirroredReleaseImage().Return("").AnyTimes()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcp",
			Namespace: "hcp-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Configuration: &hyperv1.ClusterConfiguration{
				FeatureGate: &configv1.FeatureGateSpec{
					FeatureGateSelection: configv1.FeatureGateSelection{
						FeatureSet: configv1.Default,
					},
				},
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
				APIServer: &hyperv1.APIServerNetworking{
					Port:             ptr.To[int32](2040),
					AdvertiseAddress: ptr.To("1.2.3.4"),
				},
			},
			Etcd: hyperv1.EtcdSpec{
				ManagementType: hyperv1.Managed,
			},
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.IBMCloudPlatform,
				IBMCloud: &hyperv1.IBMCloudPlatformSpec{
					ProviderType: configv1.IBMCloudProviderTypeVPC,
				},
				GCP: &hyperv1.GCPPlatformSpec{
					Project: "my-project",
					Region:  "us-central1",
					NetworkConfig: hyperv1.GCPNetworkConfig{
						Network: hyperv1.GCPResourceReference{
							Name: "my-network",
						},
					},
				},
			},
		},
	}

	cpContext := controlplanecomponent.ControlPlaneContext{
		Context:                  t.Context(),
		ApplyProvider:            upsert.NewApplyProvider(true),
		ReleaseImageProvider:     testutil.FakeImageProvider(),
		UserReleaseImageProvider: testutil.FakeImageProvider(),
		SkipPredicate:            false,
		SkipCertificateSigning:   false,
		HCP:                      hcp,
	}

	tests := []struct {
		name        string
		annotations map[string]string
		caCert      *corev1.Secret
		servingCert *corev1.Secret
	}{
		{
			name:        "No certs, no extra annotations",
			annotations: map[string]string{},
			caCert:      nil,
			servingCert: nil,
		},
		{
			name: "Premade certs, DisablePKIReconciliation annotation present",
			annotations: map[string]string{
				hyperv1.DisablePKIReconciliationAnnotation: "true",
			},
			caCert: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ignition-server-ca-cert",
					Namespace: "hcp-namespace",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       []byte("fake"),
					corev1.TLSPrivateKeyKey: []byte("fake"),
				},
			},
			servingCert: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ignition-server-serving-cert",
					Namespace: "hcp-namespace",
				},
				Data: map[string][]byte{
					corev1.TLSCertKey:       []byte("fake"),
					corev1.TLSPrivateKeyKey: []byte("fake"),
				},
			},
		},
		{
			name: "No certs, DisablePKIReconciliation annotation present",
			annotations: map[string]string{
				hyperv1.DisablePKIReconciliationAnnotation: "true",
			},
			caCert:      nil,
			servingCert: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hcp.ObjectMeta.Annotations = tt.annotations

			ignitionComponent := ignitionserverv2.NewComponent(mockedProviderWithOpenshiftImageRegistryOverrides, "")
			fakeObjects, err := componentsFakeObjects(hcp.Namespace, configv1.Default)
			if err != nil {
				t.Fatalf("failed to generate fake objects: %v", err)
			}
			fakeDeps := componentsFakeDependencies(ignitionComponent.Name(), hcp.Namespace)

			ignitionCerts := []client.Object{}
			if tt.caCert != nil {
				ignitionCerts = append(ignitionCerts, tt.caCert)
			}
			if tt.servingCert != nil {
				ignitionCerts = append(ignitionCerts, tt.servingCert)
			}

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).
				WithObjects(fakeObjects...).
				WithObjects(fakeDeps...).
				WithObjects(ignitionCerts...).
				Build()
			cpContext.Client = fakeClient

			if err := ignitionComponent.Reconcile(cpContext); err != nil {
				t.Fatalf("failed to reconcile: %v", err)
			}

			gotCACert := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ignition-server-ca-cert",
					Namespace: "hcp-namespace",
				},
			}
			gotServingCert := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ignition-server-serving-cert",
					Namespace: "hcp-namespace",
				},
			}

			_, hasPKIAnnotation := tt.annotations[hyperv1.DisablePKIReconciliationAnnotation]
			expectCACert := !hasPKIAnnotation || tt.caCert != nil
			expectServingCert := !hasPKIAnnotation || tt.servingCert != nil

			err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(gotCACert), gotCACert)
			if err != nil && expectCACert {
				t.Fatalf("ignition-server-ca-cert does not exist")
			} else if err == nil && !expectCACert {
				t.Fatalf("ignition-server-ca-cert exists")
			}

			err = fakeClient.Get(t.Context(), client.ObjectKeyFromObject(gotServingCert), gotServingCert)
			if err != nil && expectServingCert {
				t.Fatalf("ignition-server-serving-cert does not exist")
			} else if err == nil && !expectServingCert {
				t.Fatalf("ignition-server-serving-cert exists")
			}

			if tt.caCert != nil {
				wantKey := string(tt.caCert.Data[corev1.TLSCertKey])
				gotKey := string(gotCACert.Data[corev1.TLSCertKey])
				if wantKey != gotKey {
					t.Fatalf("ignition-server-ca-cert data mismatch: want %v, got %v", wantKey, gotKey)
				}
			}
			if tt.servingCert != nil {
				wantKey := string(tt.servingCert.Data[corev1.TLSCertKey])
				gotKey := string(gotServingCert.Data[corev1.TLSCertKey])
				if wantKey != gotKey {
					t.Fatalf("ignition-server-serving-cert data mismatch: want %v, got %v", wantKey, gotKey)
				}
			}
		})
	}
}

func TestEtcdRestoredCondition(t *testing.T) {
	t.Parallel()
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
				Log:    ctrl.LoggerFrom(t.Context()),
			}

			conditionPtr := r.etcdRestoredCondition(t.Context(), tc.sts)
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
`
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
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		GetRegistryOverrides().Return(map[string]string{"registry": "override"}).AnyTimes()
	mockEC2 := awsapi.NewMockEC2API(mockCtrl)
	mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), gomock.Any()).Return(&ec2.DescribeVpcEndpointsOutput{}, fmt.Errorf("not ready")).AnyTimes()

	r := &HostedControlPlaneReconciler{
		Client:                        c,
		ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		ReleaseProvider:               mockedProviderWithOpenshiftImageRegistryOverrides,
		UserReleaseProvider:           &fakereleaseprovider.FakeReleaseProvider{},
		ImageMetadataProvider:         &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProviderHCCO{},
		reconcileInfrastructureStatus: func(context.Context, *hyperv1.HostedControlPlane) (infra.InfrastructureStatus, error) {
			return readyInfraStatus, nil
		},
		SetDefaultSecurityContext: false,
		ec2Client:                 mockEC2,
	}
	r.setup(controllerutil.CreateOrUpdate)

	ctx := ctrl.LoggerInto(t.Context(), zapr.NewLogger(zaptest.NewLogger(t)))

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
			handler.Create(t.Context(), event.CreateEvent{Object: createdObject}, fakeQueue)

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

func TestNonReadyInfraTriggersRequeueAfter(t *testing.T) {
	t.Parallel()
	mockCtrl := gomock.NewController(t)
	mockedProviderWithOpenshiftImageRegistryOverrides := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(testutils.InitReleaseImageOrDie("4.15.0"), nil).AnyTimes()
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		GetRegistryOverrides().Return(map[string]string{"registry": "override"}).AnyTimes()
	mockEC2 := awsapi.NewMockEC2API(mockCtrl)
	mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), gomock.Any()).Return(&ec2.DescribeVpcEndpointsOutput{}, fmt.Errorf("not ready")).AnyTimes()
	hcp := sampleHCP(t)
	pullSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "pull-secret"}}
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcp, pullSecret).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()
	r := &HostedControlPlaneReconciler{
		Client:                        c,
		ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		ReleaseProvider:               mockedProviderWithOpenshiftImageRegistryOverrides,
		UserReleaseProvider:           &fakereleaseprovider.FakeReleaseProvider{},
		ImageMetadataProvider:         &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProviderHCCO{},
		reconcileInfrastructureStatus: func(context.Context, *hyperv1.HostedControlPlane) (infra.InfrastructureStatus, error) {
			return infra.InfrastructureStatus{}, nil
		},
		ec2Client: mockEC2,
	}
	r.setup(controllerutil.CreateOrUpdate)
	ctx := ctrl.LoggerInto(t.Context(), zapr.NewLogger(zaptest.NewLogger(t)))

	result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(hcp)})
	if err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Errorf("expected requeue after of %s when infrastructure is not ready, got %s", time.Minute, result.RequeueAfter)
	}
}

func TestSetKASCustomKubeconfigStatus(t *testing.T) {
	hcp := sampleHCP(t)
	pullSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "pull-secret"}}
	customKubeconfigSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace, Name: "custom-admin-kubeconfig"}}
	ctx := ctrl.LoggerInto(t.Context(), zapr.NewLogger(zaptest.NewLogger(t)))

	tests := []struct {
		name                 string
		KubeAPIServerDNSName string
		secretExists         bool
		expectedStatus       *hyperv1.KubeconfigSecretRef
	}{
		{
			name:                 "When KubeAPIServerDNSName is empty it should clear the status",
			KubeAPIServerDNSName: "",
			secretExists:         false,
			expectedStatus:       nil,
		},
		{
			name:                 "When KubeAPIServerDNSName is set but secret does not exist it should not advertise the secret in status",
			KubeAPIServerDNSName: "testapi.example.com",
			secretExists:         false,
			expectedStatus:       nil,
		},
		{
			name:                 "When KubeAPIServerDNSName is set and secret exists it should set the status",
			KubeAPIServerDNSName: "testapi.example.com",
			secretExists:         true,
			expectedStatus: &hyperv1.KubeconfigSecretRef{
				Name: "custom-admin-kubeconfig",
				Key:  "kubeconfig",
			},
		},
		{
			name:                 "When KubeAPIServerDNSName is empty and secret exists it should clear the status",
			KubeAPIServerDNSName: "",
			secretExists:         true,
			expectedStatus:       nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			hcp.Spec.KubeAPIServerDNSName = tc.KubeAPIServerDNSName

			objs := []client.Object{hcp, pullSecret}
			if tc.secretExists {
				objs = append(objs, customKubeconfigSecret)
			}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).WithStatusSubresource(&hyperv1.HostedControlPlane{}).Build()

			err := setKASCustomKubeconfigStatus(ctx, hcp, c)
			g.Expect(err).To(BeNil(), fmt.Errorf("error setting custom kubeconfig status failed: %w", err))
			g.Expect(hcp.Status.CustomKubeconfig).To(Equal(tc.expectedStatus))
		})
	}
}

func TestIncludeServingCertificates(t *testing.T) {
	ctx := t.Context()
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

// TestControlPlaneComponents is a generic test which generates a fixture for each registered component's deployment/statefulset.
// This is helpful to allow to inspect the final manifest yaml result after all the pre/post-processing is applied.
func TestControlPlaneComponents(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockedProviderWithOpenshiftImageRegistryOverrides := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		Lookup(gomock.Any(), gomock.Any(), gomock.Any()).Return(testutils.InitReleaseImageOrDie("4.15.0"), nil).AnyTimes()
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		GetRegistryOverrides().Return(map[string]string{"registry": "override"}).AnyTimes()
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().
		GetOpenShiftImageRegistryOverrides().Return(map[string][]string{"registry": {"override"}}).AnyTimes()
	mockedProviderWithOpenshiftImageRegistryOverrides.EXPECT().GetMirroredReleaseImage().Return("").AnyTimes()

	tests := []struct {
		name           string
		featureSet     configv1.FeatureSet
		platformType   *hyperv1.PlatformType
		hcpAnnotations map[string]string
		mutateHCP      func(hcp *hyperv1.HostedControlPlane)
		setup          func(t *testing.T)
		subDirSuffix   string
	}{
		{
			name:         "Default feature set, default platform type",
			featureSet:   configv1.Default,
			platformType: nil,
		},
		{
			name:         "TechPreviewNoUpgrade feature set, default platform type",
			featureSet:   configv1.TechPreviewNoUpgrade,
			platformType: nil,
		},
		{
			name:         "Default feature set, IBM Cloud platform type",
			featureSet:   configv1.Default,
			platformType: ptr.To(hyperv1.IBMCloudPlatform),
		},
		{
			name:         "TechPreviewNoUpgrade feature set, GCP platform type",
			featureSet:   configv1.TechPreviewNoUpgrade,
			platformType: ptr.To(hyperv1.GCPPlatform),
		},
		{
			name:         "Default feature set, Azure platform with ARO Swift",
			featureSet:   configv1.Default,
			platformType: ptr.To(hyperv1.AzurePlatform),
			hcpAnnotations: map[string]string{
				hyperv1.SwiftPodNetworkInstanceAnnotation: "swift-network-instance",
			},
			mutateHCP: func(hcp *hyperv1.HostedControlPlane) {
				// Configure Swift API fields for ARO-HCP
				hcp.Spec.Platform.Azure.Private = hyperv1.AzurePrivateSpec{
					Type: hyperv1.AzurePrivateTypeSwift,
					Swift: hyperv1.AzureSwiftSpec{
						PodNetworkInstance: "swift-network-instance",
					},
				}
				hcp.Spec.Platform.Azure.Topology = hyperv1.AzureTopologyPublicAndPrivate
				// Configure Azure KMS for ARO-HCP
				hcp.Spec.Platform.Azure.Cloud = "AzurePublicCloud"
				hcp.Spec.SecretEncryption = &hyperv1.SecretEncryptionSpec{
					Type: hyperv1.KMS,
					KMS: &hyperv1.KMSSpec{
						Provider: hyperv1.AZURE,
						Azure: &hyperv1.AzureKMSSpec{
							ActiveKey: hyperv1.AzureKMSKey{
								KeyVaultName: "test-kms-keyvault",
								KeyName:      "test-key",
								KeyVersion:   "1",
							},
							KMS: hyperv1.ManagedIdentity{
								CredentialsSecretName: "test-kms-creds",
							},
							KeyVaultAccess: hyperv1.AzureKeyVaultPrivate,
						},
					},
				}
				// Configure Azure ManagedIdentities for ARO-HCP
				hcp.Spec.Platform.Azure.AzureAuthenticationConfig = hyperv1.AzureAuthenticationConfiguration{
					AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities,
					ManagedIdentities: &hyperv1.AzureResourceManagedIdentities{
						ControlPlane: hyperv1.ControlPlaneManagedIdentities{
							ManagedIdentitiesKeyVault: hyperv1.ManagedAzureKeyVault{
								Name:     "test-keyvault",
								TenantID: "00000000-0000-0000-0000-000000000000",
							},
							CloudProvider:        hyperv1.ManagedIdentity{ClientID: "00000000-0000-0000-0000-000000000002", CredentialsSecretName: "cloud-provider-creds"},
							NodePoolManagement:   hyperv1.ManagedIdentity{ClientID: "00000000-0000-0000-0000-000000000003", CredentialsSecretName: "nodepool-mgmt-creds"},
							ControlPlaneOperator: hyperv1.ManagedIdentity{ClientID: "00000000-0000-0000-0000-000000000004", CredentialsSecretName: "cpo-creds"},
							ImageRegistry:        hyperv1.ManagedIdentity{ClientID: "00000000-0000-0000-0000-000000000005", CredentialsSecretName: "image-registry-creds"},
							Ingress:              hyperv1.ManagedIdentity{ClientID: "00000000-0000-0000-0000-000000000006", CredentialsSecretName: "ingress-creds"},
							Network:              hyperv1.ManagedIdentity{ClientID: "00000000-0000-0000-0000-000000000007", CredentialsSecretName: "network-creds"},
							Disk:                 hyperv1.ManagedIdentity{ClientID: "00000000-0000-0000-0000-000000000008", CredentialsSecretName: "disk-creds"},
							File:                 hyperv1.ManagedIdentity{ClientID: "00000000-0000-0000-0000-000000000009", CredentialsSecretName: "file-creds"},
						},
						DataPlane: hyperv1.DataPlaneManagedIdentities{
							ImageRegistryMSIClientID: "00000000-0000-0000-0000-00000000000a",
							DiskMSIClientID:          "00000000-0000-0000-0000-00000000000b",
							FileMSIClientID:          "00000000-0000-0000-0000-00000000000c",
						},
					},
				}
			},
			setup:        azureutil.SetAsAroHCPTest,
			subDirSuffix: "AROSwift",
		},
	}

	for _, tt := range tests {
		reconciler := &HostedControlPlaneReconciler{
			ReleaseProvider:               mockedProviderWithOpenshiftImageRegistryOverrides,
			ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		}
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hcp",
				Namespace: "hcp-namespace",
				Labels: map[string]string{
					"cluster.x-k8s.io/cluster-name": "cluster_name",
				},
				Annotations: map[string]string{},
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				IssuerURL: "https://test-oidc-bucket.s3.us-east-1.amazonaws.com/test-cluster",
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
					AWS: &hyperv1.AWSPlatformSpec{
						RolesRef: hyperv1.AWSRolesRef{
							NodePoolManagementARN: "arn:aws:iam::123456789012:role/test-node-pool-management-role",
						},
						TerminationHandlerQueueURL: "https://sqs.us-east-1.amazonaws.com/123456789012/test-queue",
					},
					Azure: &hyperv1.AzurePlatformSpec{
						SubnetID:        "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/myVnetName/subnets/mySubnetName",
						SecurityGroupID: "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/networkSecurityGroups/myNSGName",
						VnetID:          "/subscriptions/mySubscriptionID/resourceGroups/myResourceGroupName/providers/Microsoft.Network/virtualNetworks/myVnetName",
						AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
							AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeWorkloadIdentities,
							WorkloadIdentities: &hyperv1.AzureWorkloadIdentities{
								CloudProvider: hyperv1.WorkloadIdentity{ClientID: hyperv1.AzureClientID("myClientID")},
							},
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
					IBMCloud: &hyperv1.IBMCloudPlatformSpec{
						ProviderType: configv1.IBMCloudProviderTypeVPC,
					},
					GCP: &hyperv1.GCPPlatformSpec{
						Project: "my-project",
						Region:  "us-central1",
						NetworkConfig: hyperv1.GCPNetworkConfig{
							Network: hyperv1.GCPResourceReference{
								Name: "my-network",
							},
						},
					},
				},
				ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.16.10-x86_64",
			},
		}
		if tt.platformType != nil {
			hcp.Spec.Platform.Type = *tt.platformType
			if *tt.platformType == hyperv1.IBMCloudPlatform {
				hcp.Spec.Networking.APIServer = &hyperv1.APIServerNetworking{
					Port:             ptr.To[int32](2040),
					AdvertiseAddress: ptr.To("1.2.3.4"),
				}
			}
		}

		// Merge any additional HCP annotations from the test case
		for k, v := range tt.hcpAnnotations {
			hcp.Annotations[k] = v
		}

		// Apply any HCP mutations from the test case
		if tt.mutateHCP != nil {
			tt.mutateHCP(hcp)
		}

		// Run any setup function for the test case
		if tt.setup != nil {
			tt.setup(t)
		}

		reconciler.registerComponents(hcp)

		cpContext := controlplanecomponent.ControlPlaneContext{
			Context:                  t.Context(),
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
			HCP:                            hcp,
			SkipPredicate:                  true,
			SkipCertificateSigning:         true,
			NativeSidecarContainersEnabled: true,
		}

		cpContext.HCP.Spec.Configuration.FeatureGate.FeatureGateSelection.FeatureSet = tt.featureSet
		// This needs to be defined here, to avoid loopDetector reporting a no-op update, as changing the featureset will actually cause an update.
		cpContext.ApplyProvider = upsert.NewApplyProvider(true)

		for _, component := range reconciler.components {
			fakeObjects, err := componentsFakeObjects(hcp.Namespace, tt.featureSet)
			if err != nil {
				t.Fatalf("failed to generate fake objects: %v", err)
			}

			createdObjects := []client.Object{}
			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).
				WithObjects(fakeObjects...).
				WithObjects(componentsFakeDependencies(component.Name(), hcp.Namespace)...).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						createdObjects = append(createdObjects, obj)
						return client.Create(ctx, obj, opts...)
					},
				}).
				Build()
			cpContext.Client = fakeClient

			// Reconcile multiple times to make sure multiple runs don't produce different results,
			// and to check if resources are making a no-op update calls.
			for range 2 {
				if err := component.Reconcile(cpContext); err != nil {
					t.Fatalf("failed to reconcile component %s: %v", component.Name(), err)
				}
			}

			for _, obj := range createdObjects {
				if obj.GetNamespace() != hcp.Namespace {
					t.Fatalf("expected object %s to be in namespace %s, got %s", obj.GetName(), hcp.Namespace, obj.GetNamespace())
				}

				switch typedObj := obj.(type) {
				case *hyperv1.ControlPlaneComponent:
					// this is needed to ensure the fixtures match, otherwise LastTransitionTime will have a different value for each execution.
					for i := range typedObj.Status.Conditions {
						typedObj.Status.Conditions[i].LastTransitionTime = metav1.Time{}
					}
				}

				yaml, err := k8sutil.SerializeResource(obj, api.Scheme)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				kind := obj.GetObjectKind().GroupVersionKind().Kind
				if kind == "" {
					t.Fatalf("object %s has no kind set", obj.GetName())
				}

				suffix := fmt.Sprintf("_%s_%s", obj.GetName(), strings.ToLower(kind))
				subDir := component.Name()
				if tt.featureSet != configv1.Default {
					subDir = fmt.Sprintf("%s/%s", component.Name(), tt.featureSet)
				}
				if tt.platformType != nil {
					subDir = fmt.Sprintf("%s/%s", component.Name(), *tt.platformType)
				}
				if tt.subDirSuffix != "" {
					subDir = fmt.Sprintf("%s/%s", component.Name(), tt.subDirSuffix)
				}
				testutil.CompareWithFixture(t, yaml, testutil.WithSubDir(subDir), testutil.WithSuffix(suffix))
			}

		}

		if err := cpContext.ApplyProvider.ValidateUpdateEvents(1); err != nil {
			t.Fatalf("update loop detected: %v", err)
		}
	}
}

func TestAWSSecurityGroupTags(t *testing.T) {
	t.Parallel()
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
					AutoNode: hyperv1.AutoNode{
						Provisioner: hyperv1.ProvisionerConfig{
							Name: hyperv1.ProvisionerKarpenter,
							Karpenter: hyperv1.KarpenterConfig{
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

//go:embed testdata/featuregate-generator/feature-gate-tech-preview-no-upgrade.yaml
var testFeatureGateTechPreviewNoUpgradeYAML string

func componentsFakeObjects(namespace string, featureSet configv1.FeatureSet) ([]client.Object, error) {
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
	kasBootstrapContainerCertSecret := manifests.KASBootstrapContainerClientCertSecret(namespace)
	kasBootstrapContainerCertSecret.Data = map[string][]byte{
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
	if featureSet == configv1.TechPreviewNoUpgrade {
		fgConfigMap.Data = map[string]string{"feature-gate.yaml": testFeatureGateTechPreviewNoUpgradeYAML}
	} else {
		fgConfigMap.Data = map[string]string{"feature-gate.yaml": testFeatureGateYAML}
	}

	privateRouterSvc := manifests.PrivateRouterService(namespace)
	privateRouterSvc.Spec.ClusterIP = "172.30.0.100"

	return []client.Object{
		rootCA, authenticatorCertSecret, bootsrapCertSecret, adminCertSecert, hccoCertSecert, kasBootstrapContainerCertSecret,
		manifests.KubeControllerManagerClientCertSecret(namespace),
		manifests.KubeSchedulerClientCertSecret(namespace),
		azureCredentialsSecret,
		cloudCredsSecret,
		csrSigner,
		fgConfigMap,
		privateRouterSvc,
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
				{
					Type:   string(hyperv1.ControlPlaneComponentRolloutComplete),
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

	if componentName == metricsproxyv2.ComponentName {
		fakeComponentTemplate.Name = endpointresolverv2.ComponentName
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

func TestControlPlaneComponentsAvailable(t *testing.T) {
	t.Parallel()
	testNamespace := "test-namespace"

	testCases := []struct {
		name           string
		components     []hyperv1.ControlPlaneComponent
		expectError    bool
		expectedMsg    string
		setupClientErr bool
	}{
		{
			name:        "When no components exist, it should return message indicating components not created",
			components:  []hyperv1.ControlPlaneComponent{},
			expectError: false,
			expectedMsg: "Control plane components have not been created yet",
		},
		{
			name: "When all components are available, it should return empty message",
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{
							{
								Type:   string(hyperv1.ControlPlaneComponentAvailable),
								Status: metav1.ConditionTrue,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-controller-manager",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{
							{
								Type:   string(hyperv1.ControlPlaneComponentAvailable),
								Status: metav1.ConditionTrue,
							},
						},
					},
				},
			},
			expectError: false,
			expectedMsg: "",
		},
		{
			name: "When component has no Available condition, it should list component as not available",
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{},
					},
				},
			},
			expectError: false,
			expectedMsg: "Waiting for components to be available: kube-apiserver",
		},
		{
			name: "When component Available condition is False, it should list component as not available",
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{
							{
								Type:   string(hyperv1.ControlPlaneComponentAvailable),
								Status: metav1.ConditionFalse,
								Reason: "Deploying",
							},
						},
					},
				},
			},
			expectError: false,
			expectedMsg: "Waiting for components to be available: kube-apiserver",
		},
		{
			name: "When multiple components are not available, it should list all unavailable components",
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{
							{
								Type:   string(hyperv1.ControlPlaneComponentAvailable),
								Status: metav1.ConditionFalse,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-controller-manager",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{
							{
								Type:   string(hyperv1.ControlPlaneComponentAvailable),
								Status: metav1.ConditionTrue,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-scheduler",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{},
					},
				},
			},
			expectError: false,
			expectedMsg: "Waiting for components to be available: kube-apiserver, kube-scheduler",
		},
		{
			name: "When some components are available and others are not, it should list only unavailable components",
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{
							{
								Type:   string(hyperv1.ControlPlaneComponentAvailable),
								Status: metav1.ConditionTrue,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "oauth-server",
						Namespace: testNamespace,
					},
					Status: hyperv1.ControlPlaneComponentStatus{
						Conditions: []metav1.Condition{
							{
								Type:   string(hyperv1.ControlPlaneComponentAvailable),
								Status: metav1.ConditionFalse,
							},
						},
					},
				},
			},
			expectError: false,
			expectedMsg: "Waiting for components to be available: oauth-server",
		},
		{
			name:           "When client fails to list components, it should return error",
			components:     []hyperv1.ControlPlaneComponent{},
			setupClientErr: true,
			expectError:    true,
			expectedMsg:    "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
			}

			// Convert components to runtime objects
			objs := []client.Object{hcp}
			for i := range tc.components {
				objs = append(objs, &tc.components[i])
			}

			// Setup client with or without error interceptor
			var c client.Client
			if tc.setupClientErr {
				c = fake.NewClientBuilder().
					WithScheme(api.Scheme).
					WithObjects(objs...).
					WithInterceptorFuncs(interceptor.Funcs{
						List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
							return fmt.Errorf("simulated list error")
						},
					}).
					Build()
			} else {
				c = fake.NewClientBuilder().
					WithScheme(api.Scheme).
					WithObjects(objs...).
					Build()
			}

			// Create reconciler
			r := &HostedControlPlaneReconciler{
				Client: c,
				Log:    zapr.NewLogger(zaptest.NewLogger(t)),
			}

			// Execute the function under test
			msg, err := r.controlPlaneComponentsAvailable(context.Background(), hcp)

			// Verify results
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("failed to list control plane components"))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(msg).To(Equal(tc.expectedMsg))
			}
		})
	}
}

func TestRemoveHCPIngressFromRoutes(t *testing.T) {
	t.Parallel()
	const namespace = "test-ns"

	tests := []struct {
		name           string
		hcp            *hyperv1.HostedControlPlane
		capabilities   capabilities.CapabiltyChecker
		existingRoutes []*routev1.Route
		expectedRoutes []routev1.Route
		expectError    bool
		errorContains  string
	}{
		{
			name: "When CapabilityRoute is absent, it should skip route processing",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
			},
			capabilities: fakecapabilities.NewSupportAllExcept(capabilities.CapabilityRoute),
			existingRoutes: []*routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "example.com"},
						},
					},
				},
			},
			expectedRoutes: []routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "example.com"},
						},
					},
				},
			},
		},
		{
			name: "Route with HCPRouteLabel is skipped",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
			},
			existingRoutes: []*routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hcp-managed-route",
						Namespace: namespace,
						Labels: map[string]string{
							netutil.HCPRouteLabel: namespace,
						},
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "example.com"},
							{RouterName: "other-router", Host: "other.com"},
						},
					},
				},
			},
			expectedRoutes: []routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hcp-managed-route",
						Namespace: namespace,
						Labels: map[string]string{
							netutil.HCPRouteLabel: namespace,
						},
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "example.com"},
							{RouterName: "other-router", Host: "other.com"},
						},
					},
				},
			},
		},
		{
			name: "Route without HCPRouteLabel has router ingress removed",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
			},
			existingRoutes: []*routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "example.com"},
							{RouterName: "other-router", Host: "other.com"},
						},
					},
				},
			},
			expectedRoutes: []routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "other-router", Host: "other.com"},
						},
					},
				},
			},
		},
		{
			name: "Route without router ingress is unchanged",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
			},
			existingRoutes: []*routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "other-router", Host: "other.com"},
							{RouterName: "another-router", Host: "another.com"},
						},
					},
				},
			},
			expectedRoutes: []routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "other-router", Host: "other.com"},
							{RouterName: "another-router", Host: "another.com"},
						},
					},
				},
			},
		},
		{
			name: "Route with only router ingress has all ingress removed",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
			},
			existingRoutes: []*routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "router-only-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "example.com"},
						},
					},
				},
			},
			expectedRoutes: []routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "router-only-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{},
					},
				},
			},
		},
		{
			name: "Multiple routes handled correctly",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
			},
			existingRoutes: []*routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hcp-managed",
						Namespace: namespace,
						Labels: map[string]string{
							netutil.HCPRouteLabel: namespace,
						},
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "hcp.example.com"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route-1",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "route1.example.com"},
							{RouterName: "other-router", Host: "route1.other.com"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route-2",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "other-router", Host: "route2.other.com"},
						},
					},
				},
			},
			expectedRoutes: []routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hcp-managed",
						Namespace: namespace,
						Labels: map[string]string{
							netutil.HCPRouteLabel: namespace,
						},
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "hcp.example.com"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route-1",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "other-router", Host: "route1.other.com"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "regular-route-2",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "other-router", Host: "route2.other.com"},
						},
					},
				},
			},
		},
		{
			name: "Route with empty ingress list is unchanged",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
			},
			existingRoutes: []*routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "empty-ingress-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{},
					},
				},
			},
			expectedRoutes: []routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "empty-ingress-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{},
					},
				},
			},
		},
		{
			name: "Route with multiple router ingress entries removes all",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
			},
			existingRoutes: []*routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "multi-router-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "router", Host: "example1.com"},
							{RouterName: "router", Host: "example2.com"},
							{RouterName: "other-router", Host: "other.com"},
							{RouterName: "router", Host: "example3.com"},
						},
					},
				},
			},
			expectedRoutes: []routev1.Route{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "multi-router-route",
						Namespace: namespace,
					},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{
							{RouterName: "other-router", Host: "other.com"},
						},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			ctx := ctrl.LoggerInto(context.Background(), zapr.NewLogger(zaptest.NewLogger(t)))

			// Convert existing routes to client.Object slice
			existingObjects := make([]client.Object, 0, len(tc.existingRoutes))
			for _, route := range tc.existingRoutes {
				existingObjects = append(existingObjects, route)
			}
			existingObjects = append(existingObjects, tc.hcp)

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(existingObjects...).
				WithStatusSubresource(&routev1.Route{}).
				Build()

			caps := tc.capabilities
			if caps == nil {
				caps = &fakecapabilities.FakeSupportAllCapabilities{}
			}

			r := &HostedControlPlaneReconciler{
				Client:                        fakeClient,
				Log:                           ctrl.LoggerFrom(ctx),
				ManagementClusterCapabilities: caps,
			}

			err := r.removeHCPIngressFromRoutes(ctx, tc.hcp)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				if tc.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.errorContains))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())

				// Verify routes match expected state
				var actualRoutes routev1.RouteList
				err = fakeClient.List(ctx, &actualRoutes, client.InNamespace(namespace))
				g.Expect(err).NotTo(HaveOccurred())

				// Sort routes by name for comparison
				sort.Slice(actualRoutes.Items, func(i, j int) bool {
					return actualRoutes.Items[i].Name < actualRoutes.Items[j].Name
				})
				sort.Slice(tc.expectedRoutes, func(i, j int) bool {
					return tc.expectedRoutes[i].Name < tc.expectedRoutes[j].Name
				})

				g.Expect(len(actualRoutes.Items)).To(Equal(len(tc.expectedRoutes)))

				for i := range actualRoutes.Items {
					actual := actualRoutes.Items[i]
					expected := tc.expectedRoutes[i]

					g.Expect(actual.Name).To(Equal(expected.Name))
					g.Expect(actual.Labels).To(Equal(expected.Labels))
					g.Expect(actual.Status.Ingress).To(HaveLen(len(expected.Status.Ingress)))

					// Compare ingress entries
					for j := range actual.Status.Ingress {
						g.Expect(actual.Status.Ingress[j].RouterName).To(Equal(expected.Status.Ingress[j].RouterName))
						g.Expect(actual.Status.Ingress[j].Host).To(Equal(expected.Status.Ingress[j].Host))
					}
				}
			}
		})
	}
}

func TestReconcileAvailabilityStatus(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                      string
		conditions                []metav1.Condition
		kubeConfigAvailable       bool
		healthCheckErr            error
		componentsNotAvailableMsg string
		componentsErr             error
		generation                int64
		expectedReady             bool
		expectedReason            string
		expectedMessage           string
	}{
		{
			name:                "When no conditions exist, it should report status unknown",
			conditions:          []metav1.Condition{},
			kubeConfigAvailable: true,
			expectedReady:       false,
			expectedReason:      hyperv1.StatusUnknownReason,
			expectedMessage:     "",
		},
		{
			name: "When infrastructure is not ready, it should report infrastructure reason",
			conditions: []metav1.Condition{
				{
					Type:    string(hyperv1.InfrastructureReady),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.WaitingOnInfrastructureReadyReason,
					Message: "Load balancer not provisioned",
				},
				{
					Type:   string(hyperv1.EtcdAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.EtcdQuorumAvailableReason,
				},
				{
					Type:   string(hyperv1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			kubeConfigAvailable: true,
			expectedReady:       false,
			expectedReason:      hyperv1.WaitingOnInfrastructureReadyReason,
			expectedMessage:     "Load balancer not provisioned",
		},
		{
			name: "When kubeconfig is not available, it should report waiting for kubeconfig",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:   string(hyperv1.EtcdAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.EtcdQuorumAvailableReason,
				},
				{
					Type:   string(hyperv1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			kubeConfigAvailable: false,
			expectedReady:       false,
			expectedReason:      hyperv1.KubeconfigWaitingForCreateReason,
			expectedMessage:     "Waiting for hosted control plane kubeconfig to be created",
		},
		{
			name: "When etcd is not available, it should report etcd reason",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:    string(hyperv1.EtcdAvailable),
					Status:  metav1.ConditionFalse,
					Reason:  hyperv1.EtcdWaitingForQuorumReason,
					Message: "Waiting for etcd to reach quorum",
				},
				{
					Type:   string(hyperv1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			kubeConfigAvailable: true,
			expectedReady:       false,
			expectedReason:      hyperv1.EtcdWaitingForQuorumReason,
			expectedMessage:     "Waiting for etcd to reach quorum",
		},
		{
			name: "When KAS is not available, it should report KAS reason",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:   string(hyperv1.EtcdAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.EtcdQuorumAvailableReason,
				},
				{
					Type:    string(hyperv1.KubeAPIServerAvailable),
					Status:  metav1.ConditionFalse,
					Reason:  "DeploymentNotReady",
					Message: "KAS deployment has 0/1 ready replicas",
				},
			},
			kubeConfigAvailable: true,
			expectedReady:       false,
			expectedReason:      "DeploymentNotReady",
			expectedMessage:     "KAS deployment has 0/1 ready replicas",
		},
		{
			name: "When health check fails, it should report KAS load balancer not reachable",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:   string(hyperv1.EtcdAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.EtcdQuorumAvailableReason,
				},
				{
					Type:   string(hyperv1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			kubeConfigAvailable: true,
			healthCheckErr:      fmt.Errorf("connection refused"),
			expectedReady:       false,
			expectedReason:      hyperv1.KASLoadBalancerNotReachableReason,
			expectedMessage:     "connection refused",
		},
		{
			name: "When components check returns error, it should report components not available with error",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:   string(hyperv1.EtcdAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.EtcdQuorumAvailableReason,
				},
				{
					Type:   string(hyperv1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			kubeConfigAvailable: true,
			componentsErr:       fmt.Errorf("failed to list components"),
			expectedReady:       false,
			expectedReason:      hyperv1.ControlPlaneComponentsNotAvailable,
			expectedMessage:     "Failed to check control plane component availability: failed to list components",
		},
		{
			name: "When components are not available, it should report components not available with message",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:   string(hyperv1.EtcdAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.EtcdQuorumAvailableReason,
				},
				{
					Type:   string(hyperv1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			kubeConfigAvailable:       true,
			componentsNotAvailableMsg: "Waiting for components to be available: kube-scheduler",
			expectedReady:             false,
			expectedReason:            hyperv1.ControlPlaneComponentsNotAvailable,
			expectedMessage:           "Waiting for components to be available: kube-scheduler",
		},
		{
			name: "When all conditions are met, it should report available and ready",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
				{
					Type:   string(hyperv1.EtcdAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.EtcdQuorumAvailableReason,
				},
				{
					Type:   string(hyperv1.KubeAPIServerAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			kubeConfigAvailable: true,
			expectedReady:       true,
			expectedReason:      hyperv1.AsExpectedReason,
			expectedMessage:     "",
		},
		{
			name: "When infrastructure is true but etcd and KAS conditions are nil, it should report available",
			conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.InfrastructureReady),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AsExpectedReason,
				},
			},
			kubeConfigAvailable: true,
			expectedReady:       true,
			expectedReason:      hyperv1.AsExpectedReason,
			expectedMessage:     "",
		},
		{
			name:            "When generation is set, it should propagate to ObservedGeneration",
			conditions:      []metav1.Condition{},
			generation:      42,
			expectedReady:   false,
			expectedReason:  hyperv1.StatusUnknownReason,
			expectedMessage: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			ready, condition := reconcileAvailabilityStatus(
				tc.conditions,
				tc.kubeConfigAvailable,
				tc.healthCheckErr,
				tc.componentsNotAvailableMsg,
				tc.componentsErr,
				tc.generation,
			)

			g.Expect(ready).To(Equal(tc.expectedReady))
			g.Expect(condition.Type).To(Equal(string(hyperv1.HostedControlPlaneAvailable)))
			g.Expect(condition.Reason).To(Equal(tc.expectedReason))
			g.Expect(condition.Message).To(Equal(tc.expectedMessage))
			if tc.expectedReady {
				g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			} else {
				g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			}
			g.Expect(condition.ObservedGeneration).To(Equal(tc.generation))
		})
	}
}

func TestEtcdStatefulSetCondition(t *testing.T) {
	t.Parallel()
	testNamespace := "test-namespace"

	testCases := []struct {
		name              string
		sts               *appsv1.StatefulSet
		pvcs              []corev1.PersistentVolumeClaim
		expectedCondition metav1.Condition
		expectError       bool
	}{
		{
			name: "When all replicas are ready, it should report quorum available",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: testNamespace,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.StatefulSetStatus{
					ReadyReplicas: 3,
				},
			},
			expectedCondition: metav1.Condition{
				Type:   string(hyperv1.EtcdAvailable),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.EtcdQuorumAvailableReason,
			},
		},
		{
			name: "When majority of replicas are ready, it should report quorum available",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: testNamespace,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.StatefulSetStatus{
					ReadyReplicas: 2,
				},
			},
			expectedCondition: metav1.Condition{
				Type:   string(hyperv1.EtcdAvailable),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.EtcdQuorumAvailableReason,
			},
		},
		{
			name: "When single replica is ready out of one, it should report quorum available",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: testNamespace,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](1),
				},
				Status: appsv1.StatefulSetStatus{
					ReadyReplicas: 1,
				},
			},
			expectedCondition: metav1.Condition{
				Type:   string(hyperv1.EtcdAvailable),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.EtcdQuorumAvailableReason,
			},
		},
		{
			name: "When no replicas are ready and PVCs are bound, it should report waiting for quorum",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: testNamespace,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.StatefulSetStatus{
					ReadyReplicas: 0,
				},
			},
			pvcs: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "data-etcd-0",
						Namespace: testNamespace,
						Labels:    map[string]string{"app": "etcd"},
					},
					Status: corev1.PersistentVolumeClaimStatus{
						Phase: corev1.ClaimBound,
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:    string(hyperv1.EtcdAvailable),
				Status:  metav1.ConditionFalse,
				Reason:  hyperv1.EtcdWaitingForQuorumReason,
				Message: "Waiting for etcd to reach quorum",
			},
		},
		{
			name: "When no replicas are ready and no PVCs exist, it should report waiting for quorum",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "etcd",
					Namespace: testNamespace,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas: ptr.To[int32](3),
				},
				Status: appsv1.StatefulSetStatus{
					ReadyReplicas: 0,
				},
			},
			expectedCondition: metav1.Condition{
				Type:    string(hyperv1.EtcdAvailable),
				Status:  metav1.ConditionFalse,
				Reason:  hyperv1.EtcdWaitingForQuorumReason,
				Message: "Waiting for etcd to reach quorum",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			clientBuilder := fake.NewClientBuilder()
			if len(tc.pvcs) > 0 {
				pvcList := &corev1.PersistentVolumeClaimList{Items: tc.pvcs}
				clientBuilder = clientBuilder.WithLists(pvcList)
			}
			fakeClient := clientBuilder.Build()

			r := &HostedControlPlaneReconciler{
				Client: fakeClient,
				Log:    ctrl.LoggerFrom(t.Context()),
			}

			condition, err := r.etcdStatefulSetCondition(t.Context(), tc.sts)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(condition).ToNot(BeNil())
			g.Expect(condition.Type).To(Equal(tc.expectedCondition.Type))
			g.Expect(condition.Status).To(Equal(tc.expectedCondition.Status))
			g.Expect(condition.Reason).To(Equal(tc.expectedCondition.Reason))
			if tc.expectedCondition.Message != "" {
				g.Expect(condition.Message).To(Equal(tc.expectedCondition.Message))
			}
		})
	}
}

func TestRemoveCloudResources(t *testing.T) {
	t.Parallel()
	testNamespace := "test-namespace"

	testCases := []struct {
		name              string
		hcp               *hyperv1.HostedControlPlane
		cvoDeployment     *appsv1.Deployment
		expectedDone      bool
		expectedError     bool
		expectedCondition *metav1.Condition
	}{
		{
			name: "When CloudResourcesDestroyed is True, it should return done",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.CloudResourcesDestroyed),
							Status: metav1.ConditionTrue,
							Reason: hyperv1.AsExpectedReason,
						},
					},
				},
			},
			expectedDone: true,
		},
		{
			name: "When CloudResourcesDestroyed reason is CloudResourcesCleanupSkipped, it should return done",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:    string(hyperv1.CloudResourcesDestroyed),
							Status:  metav1.ConditionFalse,
							Reason:  string(hyperv1.CloudResourcesCleanupSkippedReason),
							Message: "Cleanup was skipped by annotation",
						},
					},
				},
			},
			expectedDone: true,
		},
		{
			name: "When CloudResourcesDestroyed reason is CloudResourcesDeletionTimedOut, it should return done",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:    string(hyperv1.CloudResourcesDestroyed),
							Status:  metav1.ConditionFalse,
							Reason:  string(hyperv1.CloudResourcesDeletionTimedOutReason),
							Message: "Giving up on cloud resource deletion after 10m",
						},
					},
				},
			},
			expectedDone: true,
		},
		{
			name: "When CVO is scaled down and deletion has timed out, it should set CloudResourcesDeletionTimedOut condition",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:               string(hyperv1.CVOScaledDown),
							Status:             metav1.ConditionTrue,
							Reason:             "CVOScaledDown",
							LastTransitionTime: metav1.NewTime(time.Now().Add(-15 * time.Minute)),
						},
					},
				},
			},
			expectedDone: true,
			expectedCondition: &metav1.Condition{
				Type:   string(hyperv1.CloudResourcesDestroyed),
				Status: metav1.ConditionFalse,
				Reason: string(hyperv1.CloudResourcesDeletionTimedOutReason),
			},
		},
		{
			name: "When CVO is scaled down and deletion has timed out with existing condition, it should include last status in message",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:               string(hyperv1.CVOScaledDown),
							Status:             metav1.ConditionTrue,
							Reason:             "CVOScaledDown",
							LastTransitionTime: metav1.NewTime(time.Now().Add(-15 * time.Minute)),
						},
						{
							Type:               string(hyperv1.CloudResourcesDestroyed),
							Status:             metav1.ConditionFalse,
							Reason:             "InProgress",
							Message:            "Deleting load balancers",
							LastTransitionTime: metav1.NewTime(time.Now().Add(-15 * time.Minute)),
						},
					},
				},
			},
			expectedDone: true,
			expectedCondition: &metav1.Condition{
				Type:   string(hyperv1.CloudResourcesDestroyed),
				Status: metav1.ConditionFalse,
				Reason: string(hyperv1.CloudResourcesDeletionTimedOutReason),
			},
		},
		{
			name: "When CVO is scaled down and deletion has not timed out, it should return not done",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:               string(hyperv1.CVOScaledDown),
							Status:             metav1.ConditionTrue,
							Reason:             "CVOScaledDown",
							LastTransitionTime: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
						},
					},
				},
			},
			expectedDone: false,
		},
		{
			name: "When CVO deployment exists with replicas, it should scale it down",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
			},
			cvoDeployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-version-operator",
					Namespace: testNamespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: ptr.To[int32](1),
				},
				Status: appsv1.DeploymentStatus{
					Replicas: 1,
				},
			},
			expectedDone: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			objs := []client.Object{tc.hcp}
			if tc.cvoDeployment != nil {
				objs = append(objs, tc.cvoDeployment)
			}

			c := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objs...).
				WithStatusSubresource(&hyperv1.HostedControlPlane{}).
				Build()

			r := &HostedControlPlaneReconciler{
				Client: c,
			}

			var originalCloudResourcesCond *metav1.Condition
			if cond := meta.FindStatusCondition(tc.hcp.Status.Conditions, string(hyperv1.CloudResourcesDestroyed)); cond != nil {
				copied := *cond
				originalCloudResourcesCond = &copied
			}

			ctx := ctrl.LoggerInto(t.Context(), zapr.NewLogger(zaptest.NewLogger(t)))
			done, err := r.removeCloudResources(ctx, tc.hcp)

			if tc.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(done).To(Equal(tc.expectedDone))

			if tc.expectedCondition != nil {
				updatedHCP := &hyperv1.HostedControlPlane{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(tc.hcp), updatedHCP)).To(Succeed())
				condition := meta.FindStatusCondition(updatedHCP.Status.Conditions, tc.expectedCondition.Type)
				g.Expect(condition).ToNot(BeNil())
				g.Expect(condition.Status).To(Equal(tc.expectedCondition.Status))
				g.Expect(condition.Reason).To(Equal(tc.expectedCondition.Reason))
				g.Expect(condition.Message).To(ContainSubstring("Giving up on cloud resource deletion"))

				if originalCloudResourcesCond != nil &&
					originalCloudResourcesCond.Message != "" &&
					originalCloudResourcesCond.Reason != string(hyperv1.CloudResourcesDeletionTimedOutReason) {
					g.Expect(condition.Message).To(ContainSubstring("last status:"))
					g.Expect(condition.Message).To(ContainSubstring(originalCloudResourcesCond.Message))
				}
			}

			if tc.cvoDeployment != nil {
				updatedCVO := &appsv1.Deployment{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(tc.cvoDeployment), updatedCVO)).To(Succeed())
				g.Expect(updatedCVO.Spec.Replicas).ToNot(BeNil())
				g.Expect(*updatedCVO.Spec.Replicas).To(Equal(int32(0)))
			}
		})
	}
}
func TestReconcileEtcdStatus(t *testing.T) {
	testNamespace := "test-namespace"

	testCases := []struct {
		name              string
		hcp               *hyperv1.HostedControlPlane
		existingObjects   []client.Object
		expectedCondType  string
		expectedCondition metav1.Condition
		expectError       bool
	}{
		{
			name: "When etcd management type is Unmanaged, it should set EtcdAvailable to True",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 3,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Unmanaged,
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.EtcdAvailable),
				Status:             metav1.ConditionTrue,
				Reason:             "EtcdRunning",
				Message:            "Etcd cluster is assumed to be running in unmanaged state",
				ObservedGeneration: 3,
			},
		},
		{
			name: "When etcd management type is Managed and StatefulSet is not found, it should set EtcdAvailable to False with NotFound reason",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 5,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
					},
				},
			},
			existingObjects: []client.Object{},
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.EtcdAvailable),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.EtcdStatefulSetNotFoundReason,
				ObservedGeneration: 5,
			},
		},
		{
			name: "When etcd management type is Managed and StatefulSet exists with quorum, it should set EtcdAvailable to True",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 2,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
					},
				},
			},
			existingObjects: []client.Object{
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd",
						Namespace: testNamespace,
					},
					Spec: appsv1.StatefulSetSpec{
						Replicas: ptr.To[int32](3),
					},
					Status: appsv1.StatefulSetStatus{
						ReadyReplicas: 3,
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.EtcdAvailable),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.EtcdQuorumAvailableReason,
				ObservedGeneration: 2,
			},
		},
		{
			name: "When etcd management type is Managed and StatefulSet has no quorum, it should set EtcdAvailable to False",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 4,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
					},
				},
			},
			existingObjects: []client.Object{
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd",
						Namespace: testNamespace,
					},
					Spec: appsv1.StatefulSetSpec{
						Replicas: ptr.To[int32](3),
					},
					Status: appsv1.StatefulSetStatus{
						ReadyReplicas: 0,
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.EtcdAvailable),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.EtcdWaitingForQuorumReason,
				ObservedGeneration: 4,
			},
		},
		{
			name: "When etcd management type is Managed and Get returns unexpected error, it should return error",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 1,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
					},
				},
			},
			expectError: true,
		},
		{
			name: "When etcd management type is Managed with RestoreSnapshotURL and StatefulSet has ready pods, it should set EtcdSnapshotRestored condition",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 6,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
						Managed: &hyperv1.ManagedEtcdSpec{
							Storage: hyperv1.ManagedEtcdStorageSpec{
								RestoreSnapshotURL: []string{"https://example.com/snapshot"},
							},
						},
					},
				},
			},
			existingObjects: []client.Object{
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd",
						Namespace: testNamespace,
					},
					Spec: appsv1.StatefulSetSpec{
						Replicas: ptr.To[int32](1),
					},
					Status: appsv1.StatefulSetStatus{
						ReadyReplicas: 1,
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "etcd-0",
						Namespace: testNamespace,
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
				Type:               string(hyperv1.EtcdAvailable),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.EtcdQuorumAvailableReason,
				ObservedGeneration: 6,
			},
		},
		{
			name: "When etcd management type is empty, it should set condition to Unknown",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 1,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Etcd: hyperv1.EtcdSpec{
						ManagementType: "",
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.EtcdAvailable),
				Status:             metav1.ConditionUnknown,
				Reason:             hyperv1.StatusUnknownReason,
				ObservedGeneration: 1,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			var c client.Client
			if tc.expectError {
				c = fake.NewClientBuilder().
					WithScheme(api.Scheme).
					WithInterceptorFuncs(interceptor.Funcs{
						Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
							return fmt.Errorf("simulated get error")
						},
					}).
					Build()
			} else {
				c = fake.NewClientBuilder().
					WithScheme(api.Scheme).
					WithObjects(tc.existingObjects...).
					Build()
			}

			r := &HostedControlPlaneReconciler{
				Client: c,
				Log:    zapr.NewLogger(zaptest.NewLogger(t)),
			}

			err := r.reconcileEtcdStatus(t.Context(), tc.hcp)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			cond := meta.FindStatusCondition(tc.hcp.Status.Conditions, string(hyperv1.EtcdAvailable))
			g.Expect(cond).ToNot(BeNil())
			g.Expect(cond.Type).To(Equal(tc.expectedCondition.Type))
			g.Expect(cond.Status).To(Equal(tc.expectedCondition.Status))
			g.Expect(cond.Reason).To(Equal(tc.expectedCondition.Reason))
			g.Expect(cond.ObservedGeneration).To(Equal(tc.expectedCondition.ObservedGeneration))
			if tc.expectedCondition.Message != "" {
				g.Expect(cond.Message).To(Equal(tc.expectedCondition.Message))
			}

			// For the restore snapshot test case, also verify EtcdSnapshotRestored condition
			if tc.name == "When etcd management type is Managed with RestoreSnapshotURL and StatefulSet has ready pods, it should set EtcdSnapshotRestored condition" {
				restoreCond := meta.FindStatusCondition(tc.hcp.Status.Conditions, string(hyperv1.EtcdSnapshotRestored))
				g.Expect(restoreCond).ToNot(BeNil())
				g.Expect(restoreCond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(restoreCond.Reason).To(Equal(hyperv1.AsExpectedReason))
			}
		})
	}
}

func TestReconcileKASStatus(t *testing.T) {
	testNamespace := "test-namespace"

	testCases := []struct {
		name              string
		hcp               *hyperv1.HostedControlPlane
		existingObjects   []client.Object
		expectedCondition metav1.Condition
		expectError       bool
	}{
		{
			name: "When KAS deployment is not found, it should set KubeAPIServerAvailable to False with NotFound reason",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 3,
				},
			},
			existingObjects: []client.Object{},
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.KubeAPIServerAvailable),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.NotFoundReason,
				Message:            "Kube APIServer deployment not found",
				ObservedGeneration: 3,
			},
		},
		{
			name: "When KAS deployment exists and is Available, it should set KubeAPIServerAvailable to True",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 5,
				},
			},
			existingObjects: []client.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
					},
					Status: appsv1.DeploymentStatus{
						Conditions: []appsv1.DeploymentCondition{
							{
								Type:   appsv1.DeploymentAvailable,
								Status: corev1.ConditionTrue,
							},
						},
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.KubeAPIServerAvailable),
				Status:             metav1.ConditionTrue,
				Reason:             hyperv1.AsExpectedReason,
				Message:            "Kube APIServer deployment is available",
				ObservedGeneration: 5,
			},
		},
		{
			name: "When KAS deployment exists but Available condition is False, it should set KubeAPIServerAvailable to False with WaitingForAvailable reason",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 7,
				},
			},
			existingObjects: []client.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
					},
					Status: appsv1.DeploymentStatus{
						Conditions: []appsv1.DeploymentCondition{
							{
								Type:   appsv1.DeploymentAvailable,
								Status: corev1.ConditionFalse,
							},
						},
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.KubeAPIServerAvailable),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.WaitingForAvailableReason,
				Message:            "Waiting for Kube APIServer deployment to become available",
				ObservedGeneration: 7,
			},
		},
		{
			name: "When KAS deployment exists but has no Available condition, it should set KubeAPIServerAvailable to False with StatusUnknown reason",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 2,
				},
			},
			existingObjects: []client.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
					},
					Status: appsv1.DeploymentStatus{
						Conditions: []appsv1.DeploymentCondition{
							{
								Type:   appsv1.DeploymentProgressing,
								Status: corev1.ConditionTrue,
							},
						},
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.KubeAPIServerAvailable),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.StatusUnknownReason,
				ObservedGeneration: 2,
			},
		},
		{
			name: "When KAS deployment exists with empty conditions, it should set KubeAPIServerAvailable to False with StatusUnknown reason",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 1,
				},
			},
			existingObjects: []client.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
					},
					Status: appsv1.DeploymentStatus{},
				},
			},
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.KubeAPIServerAvailable),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.StatusUnknownReason,
				ObservedGeneration: 1,
			},
		},
		{
			name: "When Get returns unexpected error, it should return error",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 1,
				},
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			var c client.Client
			if tc.expectError {
				c = fake.NewClientBuilder().
					WithScheme(api.Scheme).
					WithInterceptorFuncs(interceptor.Funcs{
						Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
							return fmt.Errorf("simulated get error")
						},
					}).
					Build()
			} else {
				c = fake.NewClientBuilder().
					WithScheme(api.Scheme).
					WithObjects(tc.existingObjects...).
					Build()
			}

			r := &HostedControlPlaneReconciler{
				Client: c,
				Log:    zapr.NewLogger(zaptest.NewLogger(t)),
			}

			err := r.reconcileKASStatus(t.Context(), tc.hcp)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			cond := meta.FindStatusCondition(tc.hcp.Status.Conditions, string(hyperv1.KubeAPIServerAvailable))
			g.Expect(cond).ToNot(BeNil())
			g.Expect(cond.Type).To(Equal(tc.expectedCondition.Type))
			g.Expect(cond.Status).To(Equal(tc.expectedCondition.Status))
			g.Expect(cond.Reason).To(Equal(tc.expectedCondition.Reason))
			g.Expect(cond.ObservedGeneration).To(Equal(tc.expectedCondition.ObservedGeneration))
			if tc.expectedCondition.Message != "" {
				g.Expect(cond.Message).To(Equal(tc.expectedCondition.Message))
			}
		})
	}
}

func TestReconcileDegradedStatus(t *testing.T) {
	testNamespace := "test-namespace"

	testCases := []struct {
		name              string
		hcp               *hyperv1.HostedControlPlane
		existingObjects   []client.Object
		expectedCondition metav1.Condition
		expectError       bool
	}{
		{
			name: "When no CPO-managed deployments exist, it should set Degraded to False",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 2,
				},
			},
			existingObjects: []client.Object{},
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.HostedControlPlaneDegraded),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.AsExpectedReason,
				ObservedGeneration: 2,
			},
		},
		{
			name: "When all CPO-managed deployments are fully available, it should set Degraded to False",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 3,
				},
			},
			existingObjects: []client.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
						Labels: map[string]string{
							controlplanecomponent.ManagedByLabel: "control-plane-operator",
						},
					},
					Status: appsv1.DeploymentStatus{
						UnavailableReplicas: 0,
					},
				},
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-controller-manager",
						Namespace: testNamespace,
						Labels: map[string]string{
							controlplanecomponent.ManagedByLabel: "control-plane-operator",
						},
					},
					Status: appsv1.DeploymentStatus{
						UnavailableReplicas: 0,
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.HostedControlPlaneDegraded),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.AsExpectedReason,
				ObservedGeneration: 3,
			},
		},
		{
			name: "When a single CPO-managed deployment has unavailable replicas, it should set Degraded to True",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 4,
				},
			},
			existingObjects: []client.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
						Labels: map[string]string{
							controlplanecomponent.ManagedByLabel: "control-plane-operator",
						},
					},
					Status: appsv1.DeploymentStatus{
						UnavailableReplicas: 2,
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.HostedControlPlaneDegraded),
				Status:             metav1.ConditionTrue,
				Reason:             "UnavailableReplicas",
				Message:            "kube-apiserver deployment has 2 unavailable replicas",
				ObservedGeneration: 4,
			},
		},
		{
			name: "When multiple CPO-managed deployments have unavailable replicas, it should aggregate all errors in message",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 5,
				},
			},
			existingObjects: []client.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: testNamespace,
						Labels: map[string]string{
							controlplanecomponent.ManagedByLabel: "control-plane-operator",
						},
					},
					Status: appsv1.DeploymentStatus{
						UnavailableReplicas: 1,
					},
				},
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-controller-manager",
						Namespace: testNamespace,
						Labels: map[string]string{
							controlplanecomponent.ManagedByLabel: "control-plane-operator",
						},
					},
					Status: appsv1.DeploymentStatus{
						UnavailableReplicas: 3,
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.HostedControlPlaneDegraded),
				Status:             metav1.ConditionTrue,
				Reason:             "UnavailableReplicas",
				ObservedGeneration: 5,
			},
		},
		{
			name: "When deployments exist without the CPO managed-by label, it should ignore them and set Degraded to False",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 6,
				},
			},
			existingObjects: []client.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "unmanaged-deployment",
						Namespace: testNamespace,
						Labels: map[string]string{
							"app": "something-else",
						},
					},
					Status: appsv1.DeploymentStatus{
						UnavailableReplicas: 5,
					},
				},
			},
			expectedCondition: metav1.Condition{
				Type:               string(hyperv1.HostedControlPlaneDegraded),
				Status:             metav1.ConditionFalse,
				Reason:             hyperv1.AsExpectedReason,
				ObservedGeneration: 6,
			},
		},
		{
			name: "When List returns unexpected error, it should return error",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 1,
				},
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			var c client.Client
			if tc.expectError {
				c = fake.NewClientBuilder().
					WithScheme(api.Scheme).
					WithInterceptorFuncs(interceptor.Funcs{
						List: func(ctx context.Context, client client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
							return fmt.Errorf("simulated list error")
						},
					}).
					Build()
			} else {
				c = fake.NewClientBuilder().
					WithScheme(api.Scheme).
					WithObjects(tc.existingObjects...).
					Build()
			}

			r := &HostedControlPlaneReconciler{
				Client: c,
				Log:    zapr.NewLogger(zaptest.NewLogger(t)),
			}

			err := r.reconcileDegradedStatus(t.Context(), tc.hcp)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			cond := meta.FindStatusCondition(tc.hcp.Status.Conditions, string(hyperv1.HostedControlPlaneDegraded))
			g.Expect(cond).ToNot(BeNil())
			g.Expect(cond.Type).To(Equal(tc.expectedCondition.Type))
			g.Expect(cond.Status).To(Equal(tc.expectedCondition.Status))
			g.Expect(cond.Reason).To(Equal(tc.expectedCondition.Reason))
			g.Expect(cond.ObservedGeneration).To(Equal(tc.expectedCondition.ObservedGeneration))
			if tc.expectedCondition.Message != "" {
				g.Expect(cond.Message).To(ContainSubstring(tc.expectedCondition.Message))
			}

			// For the multi-deployment case, verify that both deployment names appear in the message
			if tc.name == "When multiple CPO-managed deployments have unavailable replicas, it should aggregate all errors in message" {
				g.Expect(cond.Message).To(ContainSubstring("kube-apiserver"))
				g.Expect(cond.Message).To(ContainSubstring("kube-controller-manager"))
			}
		})
	}
}

func TestReconcileInfrastructureStatusCondition(t *testing.T) {
	testNamespace := "test-namespace"

	testCases := []struct {
		name                string
		hcp                 *hyperv1.HostedControlPlane
		infraStatus         infra.InfrastructureStatus
		infraErr            error
		expectedCondStatus  metav1.ConditionStatus
		expectedCondReason  string
		expectedEndpoint    hyperv1.APIEndpoint
		expectOAuthCallback bool
	}{
		{
			name: "When infrastructure is ready, it should set InfrastructureReady to True and populate endpoint",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 3,
				},
			},
			infraStatus: infra.InfrastructureStatus{
				APIHost:          "api.example.com",
				APIPort:          6443,
				KonnectivityHost: "konnectivity.example.com",
				KonnectivityPort: 8091,
			},
			expectedCondStatus: metav1.ConditionTrue,
			expectedCondReason: hyperv1.AsExpectedReason,
			expectedEndpoint: hyperv1.APIEndpoint{
				Host: "api.example.com",
				Port: 6443,
			},
		},
		{
			name: "When infrastructure is not ready, it should set InfrastructureReady to False",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 4,
				},
			},
			infraStatus: infra.InfrastructureStatus{
				APIHost: "",
				APIPort: 0,
				Message: "Load balancer pending",
			},
			expectedCondStatus: metav1.ConditionFalse,
			expectedCondReason: hyperv1.WaitingOnInfrastructureReadyReason,
		},
		{
			name: "When infrastructure status returns error, it should set InfrastructureReady to Unknown",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 5,
				},
			},
			infraErr:           fmt.Errorf("failed to get infrastructure status"),
			expectedCondStatus: metav1.ConditionUnknown,
			expectedCondReason: hyperv1.InfraStatusFailureReason,
		},
		{
			name: "When infrastructure is not ready with empty message, it should use default provisioning message",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 6,
				},
			},
			infraStatus: infra.InfrastructureStatus{
				APIHost: "",
				APIPort: 0,
			},
			expectedCondStatus: metav1.ConditionFalse,
			expectedCondReason: hyperv1.WaitingOnInfrastructureReadyReason,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			r := &HostedControlPlaneReconciler{
				Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
				Log:    zapr.NewLogger(zaptest.NewLogger(t)),
				reconcileInfrastructureStatus: func(ctx context.Context, hcp *hyperv1.HostedControlPlane) (infra.InfrastructureStatus, error) {
					return tc.infraStatus, tc.infraErr
				},
			}

			r.reconcileInfrastructureStatusCondition(t.Context(), tc.hcp)

			cond := meta.FindStatusCondition(tc.hcp.Status.Conditions, string(hyperv1.InfrastructureReady))
			g.Expect(cond).ToNot(BeNil())
			g.Expect(cond.Status).To(Equal(tc.expectedCondStatus))
			g.Expect(cond.Reason).To(Equal(tc.expectedCondReason))
			g.Expect(cond.ObservedGeneration).To(Equal(tc.hcp.Generation))

			if tc.expectedCondStatus == metav1.ConditionTrue {
				g.Expect(tc.hcp.Status.ControlPlaneEndpoint).To(Equal(tc.expectedEndpoint))
			}

			if tc.expectedCondStatus == metav1.ConditionFalse && tc.infraStatus.Message == "" {
				g.Expect(cond.Message).To(Equal("Cluster infrastructure is still provisioning"))
			}
			if tc.expectedCondStatus == metav1.ConditionFalse && tc.infraStatus.Message != "" {
				g.Expect(cond.Message).To(Equal(tc.infraStatus.Message))
			}
		})
	}
}

func TestReconcileExternalDNSStatusCondition(t *testing.T) {
	testNamespace := "test-namespace"

	testCases := []struct {
		name               string
		hcp                *hyperv1.HostedControlPlane
		expectedCondStatus metav1.ConditionStatus
		expectedCondReason string
		expectedMessage    string
	}{
		{
			name: "When no external DNS hostname is configured, it should set ExternalDNSReachable to Unknown",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 1,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							Port: ptr.To[int32](6443),
						},
					},
				},
			},
			expectedCondStatus: metav1.ConditionUnknown,
			expectedCondReason: hyperv1.StatusUnknownReason,
			expectedMessage:    "External DNS is not configured",
		},
		{
			name: "When HCP is private (no PublicZoneID), it should set ExternalDNSReachable to Unknown",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 2,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: hyperv1.Private,
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
								LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
									Hostname: "api.example.com",
								},
							},
						},
					},
				},
			},
			expectedCondStatus: metav1.ConditionUnknown,
			expectedCondReason: hyperv1.StatusUnknownReason,
			expectedMessage:    "External DNS is not configured",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			r := &HostedControlPlaneReconciler{
				Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
				Log:    zapr.NewLogger(zaptest.NewLogger(t)),
			}

			r.reconcileExternalDNSStatusCondition(t.Context(), tc.hcp)

			cond := meta.FindStatusCondition(tc.hcp.Status.Conditions, string(hyperv1.ExternalDNSReachable))
			g.Expect(cond).ToNot(BeNil())
			g.Expect(cond.Status).To(Equal(tc.expectedCondStatus))
			g.Expect(cond.Reason).To(Equal(tc.expectedCondReason))
			g.Expect(cond.ObservedGeneration).To(Equal(tc.hcp.Generation))
			if tc.expectedMessage != "" {
				g.Expect(cond.Message).To(Equal(tc.expectedMessage))
			}
		})
	}
}

func TestReconcileAvailabilityAndReadyStatus(t *testing.T) {
	testNamespace := "test-namespace"

	testCases := []struct {
		name               string
		hcp                *hyperv1.HostedControlPlane
		existingObjects    []client.Object
		expectedReady      bool
		expectedCondStatus metav1.ConditionStatus
		expectedCondReason string
	}{
		{
			name: "When no status conditions exist and no kubeconfig, it should set not ready with Unknown reason",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 1,
				},
			},
			expectedReady:      false,
			expectedCondStatus: metav1.ConditionFalse,
			expectedCondReason: hyperv1.StatusUnknownReason,
		},
		{
			name: "When infrastructure condition is False, it should propagate infrastructure failure",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 2,
				},
				Status: hyperv1.HostedControlPlaneStatus{
					KubeConfig: &hyperv1.KubeconfigSecretRef{
						Name: "admin-kubeconfig",
						Key:  "kubeconfig",
					},
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.InfrastructureReady),
							Status: metav1.ConditionFalse,
							Reason: hyperv1.WaitingOnInfrastructureReadyReason,
						},
						{
							Type:   string(hyperv1.EtcdAvailable),
							Status: metav1.ConditionTrue,
							Reason: hyperv1.EtcdQuorumAvailableReason,
						},
						{
							Type:   string(hyperv1.KubeAPIServerAvailable),
							Status: metav1.ConditionTrue,
							Reason: hyperv1.AsExpectedReason,
						},
					},
				},
			},
			expectedReady:      false,
			expectedCondStatus: metav1.ConditionFalse,
			expectedCondReason: hyperv1.WaitingOnInfrastructureReadyReason,
		},
		{
			name: "When health check fails but no conditions are set, it should report KAS LB not reachable",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 3,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					// No service strategy for APIServer - health check returns error
					Services: []hyperv1.ServicePublishingStrategyMapping{},
				},
				Status: hyperv1.HostedControlPlaneStatus{
					KubeConfig: &hyperv1.KubeconfigSecretRef{
						Name: "admin-kubeconfig",
						Key:  "kubeconfig",
					},
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.InfrastructureReady),
							Status: metav1.ConditionTrue,
							Reason: hyperv1.AsExpectedReason,
						},
						{
							Type:   string(hyperv1.EtcdAvailable),
							Status: metav1.ConditionTrue,
							Reason: hyperv1.EtcdQuorumAvailableReason,
						},
						{
							Type:   string(hyperv1.KubeAPIServerAvailable),
							Status: metav1.ConditionTrue,
							Reason: hyperv1.AsExpectedReason,
						},
					},
				},
			},
			expectedReady:      false,
			expectedCondStatus: metav1.ConditionFalse,
			expectedCondReason: hyperv1.KASLoadBalancerNotReachableReason,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			objs := []client.Object{}
			objs = append(objs, tc.existingObjects...)

			c := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objs...).
				Build()

			r := &HostedControlPlaneReconciler{
				Client: c,
				Log:    zapr.NewLogger(zaptest.NewLogger(t)),
			}

			r.reconcileAvailabilityAndReadyStatus(t.Context(), tc.hcp)

			g.Expect(tc.hcp.Status.Ready).To(Equal(tc.expectedReady))

			cond := meta.FindStatusCondition(tc.hcp.Status.Conditions, string(hyperv1.HostedControlPlaneAvailable))
			g.Expect(cond).ToNot(BeNil())
			g.Expect(cond.Status).To(Equal(tc.expectedCondStatus))
			g.Expect(cond.Reason).To(Equal(tc.expectedCondReason))
			g.Expect(cond.ObservedGeneration).To(Equal(tc.hcp.Generation))
		})
	}
}

func TestReconcileKubeadminPasswordStatus(t *testing.T) {
	testNamespace := "test-namespace"

	testCases := []struct {
		name                string
		hcp                 *hyperv1.HostedControlPlane
		existingObjects     []client.Object
		expectedPasswordRef *corev1.LocalObjectReference
		expectError         bool
	}{
		{
			name: "When explicit OAuth config is specified, it should clear kubeadmin password status",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						OAuth: &configv1.OAuthSpec{
							IdentityProviders: []configv1.IdentityProvider{
								{
									Name: "test-idp",
									IdentityProviderConfig: configv1.IdentityProviderConfig{
										Type: configv1.IdentityProviderTypeOpenID,
									},
								},
							},
						},
					},
				},
				Status: hyperv1.HostedControlPlaneStatus{
					KubeadminPassword: &corev1.LocalObjectReference{
						Name: "old-kubeadmin-password",
					},
				},
			},
			expectedPasswordRef: nil,
		},
		{
			name: "When no OAuth config and kubeadmin password secret exists, it should set password status",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
			},
			existingObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kubeadmin-password",
						Namespace: testNamespace,
					},
					Data: map[string][]byte{
						"password": []byte("test-password"),
					},
				},
			},
			expectedPasswordRef: &corev1.LocalObjectReference{
				Name: "kubeadmin-password",
			},
		},
		{
			name: "When no OAuth config and kubeadmin password secret does not exist, it should leave password status nil",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
			},
			existingObjects:     []client.Object{},
			expectedPasswordRef: nil,
		},
		{
			name: "When no OAuth config and Get returns unexpected error, it should return error",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: testNamespace,
				},
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			var c client.Client
			if tc.expectError {
				c = fake.NewClientBuilder().
					WithScheme(api.Scheme).
					WithInterceptorFuncs(interceptor.Funcs{
						Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
							return fmt.Errorf("simulated get error")
						},
					}).
					Build()
			} else {
				c = fake.NewClientBuilder().
					WithScheme(api.Scheme).
					WithObjects(tc.existingObjects...).
					Build()
			}

			r := &HostedControlPlaneReconciler{
				Client: c,
				Log:    zapr.NewLogger(zaptest.NewLogger(t)),
			}

			err := r.reconcileKubeadminPasswordStatus(t.Context(), tc.hcp)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			if tc.expectedPasswordRef == nil {
				g.Expect(tc.hcp.Status.KubeadminPassword).To(BeNil())
			} else {
				g.Expect(tc.hcp.Status.KubeadminPassword).ToNot(BeNil())
				g.Expect(tc.hcp.Status.KubeadminPassword.Name).To(Equal(tc.expectedPasswordRef.Name))
			}
		})
	}
}

// fakeVersionImageMetadataProvider is a simple test double for ImageMetadataProvider
// that returns deterministic results without contacting a registry.
type fakeVersionImageMetadataProvider struct {
	fakeDigest string
	fakeRef    *reference.DockerImageReference
	digestErr  error
}

func (f *fakeVersionImageMetadataProvider) ImageMetadata(_ context.Context, _ string, _ []byte) (*dockerv1client.DockerImageConfig, error) {
	return &dockerv1client.DockerImageConfig{}, nil
}

func (f *fakeVersionImageMetadataProvider) GetManifest(_ context.Context, _ string, _ []byte) (distribution.Manifest, error) {
	return nil, nil
}

func (f *fakeVersionImageMetadataProvider) GetDigest(_ context.Context, _ string, _ []byte) (digest.Digest, *reference.DockerImageReference, error) {
	if f.digestErr != nil {
		return "", nil, f.digestErr
	}
	return digest.Digest(f.fakeDigest), f.fakeRef, nil
}

func (f *fakeVersionImageMetadataProvider) GetMetadata(_ context.Context, _ string, _ []byte) (*dockerv1client.DockerImageConfig, []distribution.Descriptor, distribution.BlobStore, error) {
	return &dockerv1client.DockerImageConfig{}, nil, nil, nil
}

func (f *fakeVersionImageMetadataProvider) GetOverride(_ context.Context, _ string, _ []byte) (*reference.DockerImageReference, error) {
	return f.fakeRef, nil
}

func TestReconcileControlPlaneVersionStatus(t *testing.T) {
	testNamespace := "test-namespace"
	fakeClock := testingclock.NewFakeClock(time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC))

	testCases := []struct {
		name            string
		hcp             *hyperv1.HostedControlPlane
		existingObjects []client.Object
		digestErr       error
		expectError     bool
		expectedDesired configv1.Release
		expectedState   configv1.UpdateState
	}{
		{
			name: "When pull secret exists and components are listed successfully with first population, it should create Partial history entry",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 1,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.20.0",
				},
			},
			existingObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pull-secret",
						Namespace: testNamespace,
					},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: []byte("{}"),
					},
				},
			},
			expectedDesired: configv1.Release{
				Version: "4.20.0",
				Image:   "quay.io/openshift-release-dev/ocp-release@sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			},
			expectedState: configv1.PartialUpdate,
		},
		{
			name: "When pull secret is missing, it should return error",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 1,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.20.0",
				},
			},
			existingObjects: []client.Object{},
			expectError:     true,
		},
		{
			name: "When GetDigest fails, it should return error",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 1,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.20.0",
				},
			},
			existingObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pull-secret",
						Namespace: testNamespace,
					},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: []byte("{}"),
					},
				},
			},
			digestErr:   fmt.Errorf("failed to resolve digest"),
			expectError: true,
		},
		{
			name: "When component list fails, it should set partial version and return error",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  testNamespace,
					Generation: 2,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.20.0",
				},
			},
			existingObjects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pull-secret",
						Namespace: testNamespace,
					},
					Data: map[string][]byte{
						corev1.DockerConfigJsonKey: []byte("{}"),
					},
				},
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			releaseImage := testutils.InitReleaseImageOrDie("4.20.0")

			resolvedRef := &reference.DockerImageReference{
				Registry:  "quay.io",
				Namespace: "openshift-release-dev",
				Name:      "ocp-release",
				ID:        "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			}

			imgProvider := &fakeVersionImageMetadataProvider{
				fakeDigest: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
				fakeRef:    resolvedRef,
				digestErr:  tc.digestErr,
			}

			clientBuilder := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(tc.existingObjects...)

			// For the "component list fails" case, intercept the List call to fail
			if tc.name == "When component list fails, it should set partial version and return error" {
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if _, ok := list.(*hyperv1.ControlPlaneComponentList); ok {
							return fmt.Errorf("simulated list error")
						}
						return c.List(ctx, list, opts...)
					},
				})
				// Also need to add status subresource for patching
				clientBuilder = clientBuilder.WithStatusSubresource(tc.hcp)
			}

			c := clientBuilder.Build()

			// For the component list failure case, we need the HCP to exist in the fake client
			if tc.name == "When component list fails, it should set partial version and return error" {
				g.Expect(c.Create(t.Context(), tc.hcp)).To(Succeed())
			}

			r := &HostedControlPlaneReconciler{
				Client:                c,
				Log:                   zapr.NewLogger(zaptest.NewLogger(t)),
				ImageMetadataProvider: imgProvider,
				clock:                 fakeClock,
			}

			originalHCP := tc.hcp.DeepCopy()
			err := r.reconcileControlPlaneVersionStatus(t.Context(), tc.hcp, originalHCP, releaseImage)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())

				// For the component list failure case, verify partial status was still populated
				if tc.name == "When component list fails, it should set partial version and return error" {
					g.Expect(tc.hcp.Status.ControlPlaneVersion.Desired.Version).To(Equal("4.20.0"))
					g.Expect(tc.hcp.Status.ControlPlaneVersion.Desired.Image).ToNot(BeEmpty())
					g.Expect(tc.hcp.Status.ControlPlaneVersion.History).ToNot(BeEmpty())
				}
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(tc.hcp.Status.ControlPlaneVersion.Desired.Version).To(Equal(tc.expectedDesired.Version))
			g.Expect(tc.hcp.Status.ControlPlaneVersion.Desired.Image).To(Equal(tc.expectedDesired.Image))
			g.Expect(tc.hcp.Status.ControlPlaneVersion.History).ToNot(BeEmpty())
			g.Expect(tc.hcp.Status.ControlPlaneVersion.History[0].State).To(Equal(tc.expectedState))
		})
	}
}

func TestReconcileDeletion(t *testing.T) {
	tests := []struct {
		name           string
		setupEC2Mock   func(*gomock.Controller) *awsapi.MockEC2API
		wantErr        bool
		wantCondStatus metav1.ConditionStatus
	}{
		{
			name: "When destroyAWSDefaultSecurityGroup returns UnauthorizedOperation, it should skip gracefully and not return error",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(&ec2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{
						{GroupId: aws.String("sg-123")},
					},
				}, nil)
				m.EXPECT().DeleteSecurityGroup(gomock.Any(), gomock.Any()).Return(nil,
					&smithy.GenericAPIError{Code: "UnauthorizedOperation", Message: "not authorized"})
				return m
			},
			wantErr:        false,
			wantCondStatus: metav1.ConditionFalse,
		},
		{
			name: "When destroyAWSDefaultSecurityGroup returns DependencyViolation, it should skip gracefully and not return error",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(&ec2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{
						{GroupId: aws.String("sg-123")},
					},
				}, nil)
				m.EXPECT().DeleteSecurityGroup(gomock.Any(), gomock.Any()).Return(nil,
					&smithy.GenericAPIError{Code: "DependencyViolation", Message: "resource has dependent object"})
				return m
			},
			wantErr:        false,
			wantCondStatus: metav1.ConditionFalse,
		},
		{
			name: "When destroyAWSDefaultSecurityGroup returns unexpected error, it should propagate the error",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(&ec2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{
						{GroupId: aws.String("sg-123")},
					},
				}, nil)
				m.EXPECT().DeleteSecurityGroup(gomock.Any(), gomock.Any()).Return(nil,
					&smithy.GenericAPIError{Code: "InternalError", Message: "something broke"})
				return m
			},
			wantErr:        true,
			wantCondStatus: metav1.ConditionFalse,
		},
		{
			name: "When destroyAWSDefaultSecurityGroup succeeds, it should set condition to true",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				// First call: find the SG
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(&ec2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{
						{GroupId: aws.String("sg-123")},
					},
				}, nil)
				m.EXPECT().DeleteSecurityGroup(gomock.Any(), gomock.Any()).Return(&ec2.DeleteSecurityGroupOutput{}, nil)
				// Second call: verify SG is gone
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(&ec2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{},
				}, nil)
				return m
			},
			wantErr:        false,
			wantCondStatus: metav1.ConditionTrue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			mockCtrl := gomock.NewController(t)
			mockEC2 := tt.setupEC2Mock(mockCtrl)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
					Annotations: map[string]string{
						hyperv1.CleanupCloudResourcesAnnotation: "true",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS:  &hyperv1.AWSPlatformSpec{},
					},
				},
				Status: hyperv1.HostedControlPlaneStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.CloudResourcesDestroyed),
							Status: metav1.ConditionTrue,
							Reason: hyperv1.AsExpectedReason,
						},
					},
				},
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(hcp).
				WithStatusSubresource(&hyperv1.HostedControlPlane{}).
				Build()

			ctx := ctrl.LoggerInto(t.Context(), ctrl.Log.WithName("test"))

			// Re-read from fake client so the object has a ResourceVersion for OptimisticLock
			g.Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(hcp), hcp)).To(Succeed())
			originalHCP := hcp.DeepCopy()

			r := &HostedControlPlaneReconciler{
				Client:    fakeClient,
				Log:       ctrl.Log.WithName("test"),
				ec2Client: mockEC2,
			}

			_, err := r.reconcileDeletion(ctx, hcp, originalHCP)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			cond := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.AWSDefaultSecurityGroupDeleted))
			g.Expect(cond).ToNot(BeNil())
			g.Expect(cond.Status).To(Equal(tt.wantCondStatus))
		})
	}
}

func TestHealthCheckKASEndpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		handler   http.HandlerFunc
		cancelCtx bool
		wantErr   bool
		errSubstr string
	}{
		{
			name: "When endpoint returns 200 OK, it should succeed",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
		},
		{
			name: "When endpoint returns 503, it should return an unhealthy error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			},
			wantErr:   true,
			errSubstr: "is not healthy",
		},
		{
			name: "When context is canceled, it should return an error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			cancelCtx: true,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			server := httptest.NewTLSServer(tt.handler)
			defer server.Close()

			host, portStr, err := net.SplitHostPort(server.Listener.Addr().String())
			g.Expect(err).ToNot(HaveOccurred())
			port, err := strconv.Atoi(portStr)
			g.Expect(err).ToNot(HaveOccurred())

			ctx := t.Context()
			if tt.cancelCtx {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			err = healthCheckKASEndpoint(ctx, host, port)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				if tt.errSubstr != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}

	t.Run("When the ingress point contains invalid characters, it should return a request creation error", func(t *testing.T) {
		g := NewWithT(t)
		err := healthCheckKASEndpoint(t.Context(), "host\x7f", 443)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("invalid control character"))
	})
}

// TestRouterComponentComesAfterRouteCreatingComponents verifies that the router
// component is registered after all components that create Route objects.
// The router's adaptConfig reads existing Routes to generate the HAProxy config;
// if a route-creating component (e.g. ignition-server) is registered after the
// router, the route won't exist during the first reconcile and the HAProxy config
// will be missing that backend. See OCPBUGS-98213.
func TestRouterComponentComesAfterRouteCreatingComponents(t *testing.T) {
	t.Parallel()

	mockCtrl := gomock.NewController(t)
	mockedProvider := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockCtrl)
	mockedProvider.EXPECT().GetRegistryOverrides().Return(map[string]string{}).AnyTimes()
	mockedProvider.EXPECT().GetOpenShiftImageRegistryOverrides().Return(map[string][]string{}).AnyTimes()

	reconciler := &HostedControlPlaneReconciler{
		ReleaseProvider:               mockedProvider,
		ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
	}

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
			},
			Etcd: hyperv1.EtcdSpec{
				ManagementType: hyperv1.Managed,
			},
			Services: []hyperv1.ServicePublishingStrategyMapping{
				{
					Service: hyperv1.Ignition,
					ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
						Type: hyperv1.Route,
					},
				},
			},
		},
	}

	reconciler.registerComponents(hcp)

	// Build a map of component name -> position in the list.
	positions := make(map[string]int, len(reconciler.components))
	for i, c := range reconciler.components {
		positions[c.Name()] = i
	}

	routerPos, ok := positions[routerv2.ComponentName]
	if !ok {
		t.Fatal("router component not found in registered components")
	}

	// These components create Route objects via their manifest adapters.
	// The router must come after all of them so its HAProxy config includes
	// every route on the first reconcile pass.
	routeCreatingComponents := []string{
		ignitionserverv2.ComponentName,
		metricsproxyv2.ComponentName,
	}
	for _, name := range routeCreatingComponents {
		pos, ok := positions[name]
		if !ok {
			t.Fatalf("route-creating component %q not found in registered components", name)
		}
		if routerPos < pos {
			t.Errorf("router component (position %d) must be registered after %s (position %d) "+
				"so that the HAProxy config includes %s's route on the first reconcile pass",
				routerPos, name, pos, name)
		}
	}
}

// Compile-time assertion that fakeVersionImageMetadataProvider satisfies the interface.
var _ util.ImageMetadataProvider = &fakeVersionImageMetadataProvider{}

// Compile-time assertion for clock interface used by tests.
var _ clock.Clock = &testingclock.FakeClock{}
